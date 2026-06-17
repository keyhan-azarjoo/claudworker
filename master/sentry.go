package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type SentryProj struct {
	Name       string `json:"name"`
	Unresolved int    `json:"unresolved"` // remaining to solve
	Resolved   int    `json:"resolved"`   // solved
	Capped     bool   `json:"capped"`     // true if counts hit the 100 fetch cap (show "+")
}

var (
	snCache []SentryProj
	snAt    time.Time
	snMu    sync.Mutex
)

func sentryBase(cfg *Config) string {
	b := strings.TrimRight(secret(cfg, "SENTRY_API_BASE"), "/")
	if b == "" {
		b = "https://sentry.io"
	}
	b = strings.TrimSuffix(b, "/api/0")
	return b
}

func sentryGET(cfg *Config, path string) []byte {
	token := secret(cfg, "SENTRY_TOKEN")
	if token == "" {
		return nil
	}
	req, _ := http.NewRequest("GET", sentryBase(cfg)+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 12 * time.Second}).Do(req)
	if err != nil {
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil
	}
	b, _ := io.ReadAll(resp.Body)
	return b
}

func sentryCount(cfg *Config, org, slug, query string) (int, bool) {
	b := sentryGET(cfg, "/api/0/projects/"+org+"/"+slug+"/issues/?query="+query+"&limit=100&statsPeriod=90d")
	if b == nil {
		return 0, false
	}
	var arr []map[string]any
	if json.Unmarshal(b, &arr) != nil {
		return 0, false
	}
	return len(arr), len(arr) >= 100
}

// sentryCached is the non-blocking read for the live stream.
func sentryCached() []SentryProj {
	snMu.Lock()
	defer snMu.Unlock()
	return snCache
}

// startSentryWatch refreshes the Sentry snapshot every 10m in the background.
func startSentryWatch(cfg *Config) {
	go func() {
		for {
			sentryStats(cfg)
			time.Sleep(10 * time.Minute)
		}
	}()
}

// sentryStats fetches per-project issue counts (persisted next to users.json). Does NOT hold the
// lock during HTTP, so the stream never stalls.
func sentryStats(cfg *Config) []SentryProj {
	snapPath := filepath.Join(cfg.root, "sentry_snapshot.json")
	snMu.Lock()
	fresh := time.Since(snAt) < 10*time.Minute && snCache != nil
	snMu.Unlock()
	if fresh {
		return sentryCached()
	}
	org := secret(cfg, "SENTRY_ORG")
	token := secret(cfg, "SENTRY_TOKEN")
	if org == "" || token == "" {
		if b, e := os.ReadFile(snapPath); e == nil { // fall back to last persisted snapshot
			var cached []SentryProj
			if json.Unmarshal(b, &cached) == nil {
				snMu.Lock()
				snCache = cached
				snMu.Unlock()
			}
		}
		return sentryCached()
	}
	pb := sentryGET(cfg, "/api/0/organizations/"+org+"/projects/")
	var projs []struct {
		Slug string `json:"slug"`
		Name string `json:"name"`
	}
	if pb == nil || json.Unmarshal(pb, &projs) != nil {
		return sentryCached()
	}
	out := []SentryProj{}
	for _, p := range projs {
		un, cap1 := sentryCount(cfg, org, p.Slug, "is:unresolved")
		re, _ := sentryCount(cfg, org, p.Slug, "is:resolved")
		out = append(out, SentryProj{Name: p.Name, Unresolved: un, Resolved: re, Capped: cap1})
	}
	if out != nil {
		snMu.Lock()
		snCache, snAt = out, time.Now()
		snMu.Unlock()
		if b, e := json.MarshalIndent(out, "", "  "); e == nil {
			_ = os.WriteFile(snapPath, b, 0o600)
		}
	}
	return sentryCached()
}

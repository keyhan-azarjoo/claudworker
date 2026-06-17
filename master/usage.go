package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// UsageInfo holds both rolling windows + minutes until each resets. Week (7-day) is the PRIMARY metric.
type UsageInfo struct {
	Hour         int  `json:"hour"`         // 5-hour utilization %
	Week         int  `json:"week"`         // 7-day utilization % (PRIMARY)
	HourResetMin int  `json:"hourResetMin"` // minutes until the 5h window resets (-1 unknown)
	WeekResetMin int  `json:"weekResetMin"` // minutes until the 7d window resets (-1 unknown)
	OK           bool `json:"ok"`
}

func usagePct(cfg *Config) int { return usageInfoCached(keychainToken()).Week }

// keychainToken reads the Claude Code OAuth access token from the macOS keychain.
func keychainToken() string {
	out, err := exec.Command("security", "find-generic-password", "-s", "Claude Code-credentials", "-w").Output()
	if err != nil {
		return ""
	}
	var cred struct {
		ClaudeAiOauth struct {
			AccessToken string `json:"accessToken"`
		} `json:"claudeAiOauth"`
	}
	if json.Unmarshal([]byte(strings.TrimSpace(string(out))), &cred) == nil && cred.ClaudeAiOauth.AccessToken != "" {
		return cred.ClaudeAiOauth.AccessToken
	}
	return ""
}

// queryUsageFull fetches the usage endpoint (rate-limited! always call via the cache).
func queryUsageFull(token string) UsageInfo {
	u := UsageInfo{Hour: -1, Week: -1, HourResetMin: -1, WeekResetMin: -1}
	if token == "" {
		return u
	}
	req, err := http.NewRequest("GET", "https://api.anthropic.com/api/oauth/usage", nil)
	if err != nil {
		return u
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return u
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return u
	}
	body, _ := io.ReadAll(resp.Body)
	var r struct {
		FiveHour struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    any     `json:"resets_at"`
		} `json:"five_hour"`
		SevenDay struct {
			Utilization float64 `json:"utilization"`
			ResetsAt    any     `json:"resets_at"`
		} `json:"seven_day"`
	}
	if json.Unmarshal(body, &r) != nil {
		return u
	}
	u.Hour = pctOf(r.FiveHour.Utilization)
	u.Week = pctOf(r.SevenDay.Utilization)
	u.HourResetMin = resetMinutes(r.FiveHour.ResetsAt)
	u.WeekResetMin = resetMinutes(r.SevenDay.ResetsAt)
	u.OK = u.Hour >= 0 || u.Week >= 0
	return u
}

func pctOf(v float64) int {
	if v <= 0 {
		return 0
	}
	if v <= 1 {
		v *= 100
	}
	return int(v + 0.5)
}

// resetMinutes parses resets_at (unix seconds or RFC3339) into minutes-from-now; -1 if unknown.
func resetMinutes(v any) int {
	var t time.Time
	switch x := v.(type) {
	case float64:
		t = time.Unix(int64(x), 0)
	case string:
		if p, e := time.Parse(time.RFC3339, x); e == nil {
			t = p
		} else {
			return -1
		}
	default:
		return -1
	}
	m := int(time.Until(t).Minutes())
	if m < 0 {
		return 0
	}
	return m
}

// findToken recursively finds an access-token string in a parsed creds object (for profile creds files).
func findToken(v any) string {
	if m, ok := v.(map[string]any); ok {
		for k, val := range m {
			lk := strings.ToLower(k)
			if strings.Contains(lk, "access") && strings.Contains(lk, "token") {
				if s, ok := val.(string); ok && s != "" {
					return s
				}
			}
			if s := findToken(val); s != "" {
				return s
			}
		}
	}
	return ""
}

// cache: the usage endpoint is strictly rate-limited → query at most once / 5min (failure 2min).
var (
	uCache = map[string]UsageInfo{}
	uAt    = map[string]time.Time{}
	uMu    sync.Mutex
)

var uInflight sync.Map // token -> bool: a usage refresh is in flight

// usageInfoCached returns the cached usage INSTANTLY (stale-while-revalidate). The rate-limited
// /api/oauth/usage call runs in the background, so it never blocks the SSE/first reload.
func usageInfoCached(token string) UsageInfo {
	empty := UsageInfo{Hour: -1, Week: -1, HourResetMin: -1, WeekResetMin: -1}
	if token == "" {
		return empty
	}
	uMu.Lock()
	v, has := uCache[token]
	t := uAt[token]
	uMu.Unlock()
	ttl := 300 * time.Second
	if has && !v.OK {
		ttl = 120 * time.Second
	}
	if has && time.Since(t) < ttl {
		return v
	}
	// stale or cold → refresh in the background; return what we have now (or empty the first time)
	if _, busy := uInflight.LoadOrStore(token, true); !busy {
		go func() {
			defer uInflight.Delete(token)
			nv := queryUsageFull(token)
			uMu.Lock()
			if !nv.OK { // keep last good value through a transient rate-limit
				if old, had := uCache[token]; had && old.OK {
					nv = old
				}
			}
			uCache[token] = nv
			uAt[token] = time.Now()
			uMu.Unlock()
		}()
	}
	if has {
		return v
	}
	return empty
}

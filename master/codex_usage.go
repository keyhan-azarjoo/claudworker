package main

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// Codex (ChatGPT) exposes no usage-% command, but it writes a `token_count` event with
// `rate_limits` into its local session logs on every run: primary = 5-hour window,
// secondary = weekly, each with used_percent + resets_at (unix). We read the freshest one
// straight off disk — no extra API calls — so the dashboard shows codex usage like Claude.

type codexUsageInfo struct {
	HourPct, WeekPct           int
	HourResetMin, WeekResetMin int
	OK                         bool
}

type cxWin struct {
	UsedPercent float64 `json:"used_percent"`
	WindowMin   int     `json:"window_minutes"`
	ResetsAt    int64   `json:"resets_at"`
}

var (
	cxuMu    sync.Mutex
	cxuCache = map[string]codexUsageInfo{}
	cxuAt    = map[string]time.Time{}
)

func codexUsageCached(configDir string) codexUsageInfo {
	cxuMu.Lock()
	if t, ok := cxuAt[configDir]; ok && time.Since(t) < 90*time.Second {
		v := cxuCache[configDir]
		cxuMu.Unlock()
		return v
	}
	cxuMu.Unlock()
	v := readCodexUsage(configDir)
	cxuMu.Lock()
	if !v.OK { // keep last good value if this read found nothing fresh
		if old, had := cxuCache[configDir]; had && old.OK {
			v = old
		}
	}
	cxuCache[configDir], cxuAt[configDir] = v, time.Now()
	cxuMu.Unlock()
	return v
}

func readCodexUsage(configDir string) codexUsageInfo {
	base := expandHome(configDir)
	if configDir == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".codex")
	}
	sessDir := filepath.Join(base, "sessions")
	var files []string
	_ = filepath.WalkDir(sessDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(p, ".jsonl") {
			files = append(files, p)
		}
		return nil
	})
	if len(files) == 0 {
		return codexUsageInfo{}
	}
	// newest first
	sort.Slice(files, func(i, j int) bool {
		fi, _ := os.Stat(files[i])
		fj, _ := os.Stat(files[j])
		return fi != nil && fj != nil && fi.ModTime().After(fj.ModTime())
	})
	if len(files) > 40 {
		files = files[:40]
	}
	now := time.Now().Unix()
	toMin := func(resets int64) int {
		if resets <= 0 {
			return -1
		}
		m := int((resets - now) / 60)
		if m < 0 {
			m = 0
		}
		return m
	}
	for _, f := range files { // first (newest) file with a populated rate_limit wins
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var found *codexUsageInfo
		for _, ln := range strings.Split(string(b), "\n") {
			if !strings.Contains(ln, "token_count") || !strings.Contains(ln, "rate_limits") {
				continue
			}
			var ev struct {
				Payload struct {
					RateLimits struct {
						Primary   *cxWin `json:"primary"`
						Secondary *cxWin `json:"secondary"`
					} `json:"rate_limits"`
				} `json:"payload"`
			}
			if json.Unmarshal([]byte(ln), &ev) != nil {
				continue
			}
			rl := ev.Payload.RateLimits
			// only trust a reading whose window hasn't already reset (resets_at in the future) —
			// avoids showing stale percentages from an old, since-reset window.
			validP := rl.Primary != nil && rl.Primary.ResetsAt > now
			validS := rl.Secondary != nil && rl.Secondary.ResetsAt > now
			if !validP && !validS {
				continue
			}
			info := codexUsageInfo{OK: true, HourResetMin: -1, WeekResetMin: -1}
			if validP {
				info.HourPct = int(rl.Primary.UsedPercent + 0.5)
				info.HourResetMin = toMin(rl.Primary.ResetsAt)
			}
			if validS {
				info.WeekPct = int(rl.Secondary.UsedPercent + 0.5)
				info.WeekResetMin = toMin(rl.Secondary.ResetsAt)
			}
			found = &info // keep the LAST good event in the file (freshest)
		}
		if found != nil {
			return *found
		}
	}
	return codexUsageInfo{}
}

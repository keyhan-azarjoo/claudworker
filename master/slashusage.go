package main

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"sync"
	"time"
)

// Source of truth for Claude usage = the REAL /usage slash command (NOT the HTTP usage endpoint,
// which reports different numbers). scripts/usage_probe.py drives the claude TUI in a pty, runs
// /usage, and returns the parsed "Current session" + "Current week" % and reset minutes. The probe
// is heavy (~15s, spawns a full TUI), so we cache it with a long TTL, refresh async (never block the
// SSE), and serialise probes so only one runs at a time.

type SlashUsage struct {
	Session         int  `json:"session"`         // "Current session" % (5-hour window)
	Week            int  `json:"week"`            // "Current week" % (PRIMARY)
	SessionResetMin int  `json:"sessionResetMin"` // minutes until the session window resets
	WeekResetMin    int  `json:"weekResetMin"`    // minutes until the weekly window resets
	NotReady        bool `json:"notReady"`        // profile needs an interactive login (TUI onboarding)
	OK              bool `json:"ok"`
}

var (
	suCache   = map[string]SlashUsage{}
	suAt      = map[string]time.Time{}
	suMu      sync.Mutex
	suProbeMu sync.Mutex // only ONE heavy /usage probe runs at a time
	suProbing sync.Map   // configDir -> in flight
)

// slashUsageCached returns the cached /usage INSTANTLY (stale-while-revalidate). A cold profile
// returns {OK:false} on the first call and fills in within ~15s.
func slashUsageCached(cfg *Config, configDir string) SlashUsage {
	suMu.Lock()
	v, has := suCache[configDir]
	age := time.Since(suAt[configDir])
	suMu.Unlock()
	ttl := 10 * time.Minute
	if has && (!v.OK || v.NotReady) {
		ttl = 4 * time.Minute // retry not-ready / failed profiles a bit sooner
	}
	if has && age < ttl {
		return v
	}
	if _, busy := suProbing.LoadOrStore(configDir, true); !busy {
		go func() {
			defer suProbing.Delete(configDir)
			suProbeMu.Lock() // serialise: probes are expensive
			defer suProbeMu.Unlock()
			nv := probeSlashUsage(cfg, configDir)
			suMu.Lock()
			// keep last good numbers through a transient failure
			if !nv.OK {
				if old, had := suCache[configDir]; had && old.OK {
					old.NotReady = nv.NotReady
					nv = old
				}
			}
			suCache[configDir] = nv
			suAt[configDir] = time.Now()
			suMu.Unlock()
		}()
	}
	if has {
		return v
	}
	return SlashUsage{Session: -1, Week: -1, SessionResetMin: -1, WeekResetMin: -1}
}

func probeSlashUsage(cfg *Config, configDir string) SlashUsage {
	out := SlashUsage{Session: -1, Week: -1, SessionResetMin: -1, WeekResetMin: -1}
	script := filepath.Join(cfg.root, "scripts", "usage_probe.py")
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "python3", script, configDir).Output()
	if err != nil && len(b) == 0 {
		return out
	}
	_ = json.Unmarshal(b, &out)
	return out
}

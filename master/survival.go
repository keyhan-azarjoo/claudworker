package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ── Survival mode ──────────────────────────────────────────────────────────────────────────────
// Owner rule: when ALL Claude accounts have hit their limit (no Claude model left), DON'T run the
// normal fleet — wait for the reset. The ONE exception: keep a SMALL codex model alive for
// reporting + small, non-damaging tasks (and anything the owner explicitly asks for). Always keep a
// 2% safety margin below the limit. A small model can do real damage on heavy code work, so in
// survival we only allow SAFE jobs (read/report/test) — never the code-changing engineers/deploys.

const survivalSafetyMargin = 2 // always keep ≥2% headroom below an account's cap

// safeInSurvival lists the jobs a small fallback model may run without risk of damage:
// reporting, triage, read-only review, and tests that don't change or ship code.
var safeInSurvival = map[string]bool{
	"inbox":           true, // telegram reporting / answering the owner
	"sentry-catch":    true, // read + file (no code change)
	"sentry-clean":    true, // resolve/close (no code change)
	"reliability":     true, // health glance + routing
	"jira-poll":       true, // light triage
	"planner":         true, // planning/docs only
	"code-review":     true, // read-only review
	"security-review": true, // read-only review
	"e2e-test":        true, // tests — no damage
	"visual-test":     true, // tests — no damage
	"scenario-gen":    true, // content generation only
	"app-map":         true, // read-only map
}

// claudeAnyAvailable reports whether at least one Claude account can take work right now
// (logged in, within schedule, under its cap incl. the 2% safety margin).
func claudeAnyAvailable(cfg *Config) bool {
	for i := range cfg.Accounts {
		a := cfg.Accounts[i]
		if a.Engine == "codex" {
			continue
		}
		if availableWithMargin(cfg, &a) {
			return true
		}
	}
	// the implicit default (keychain) account too
	if len(cfg.Accounts) == 0 {
		return availableWithMargin(cfg, nil)
	}
	return false
}

// codexAnyAvailable reports whether a codex account is logged in and not cooling down.
func codexAnyAvailable(cfg *Config) bool {
	for i := range cfg.Accounts {
		a := cfg.Accounts[i]
		if a.Engine == "codex" && !isAcctPaused(a.Label) && !inCooldown(a.Label) && codexLoggedIn(a.ConfigDir) {
			return true
		}
	}
	return false
}

// availableWithMargin is available() but reserving the 2% safety headroom on the weekly usage.
func availableWithMargin(cfg *Config, acct *Account) bool {
	if !available(cfg, acct) {
		return false
	}
	if acct == nil || acct.Engine == "codex" || acct.ConfigDir != "" && !claudeLoggedIn(acct.ConfigDir) {
		return available(cfg, acct)
	}
	tok := tokenFor(acct)
	if tok == "" {
		return available(cfg, acct)
	}
	ui := usageInfoCached(tok)
	if ui.OK && ui.Week >= effectiveLimit(cfg, acct)-survivalSafetyMargin {
		return false // inside the 2% safety band → treat as unavailable
	}
	return true
}

// survivalActive: no Claude left, but codex can still keep the lights on with a small model.
// survivalActive is CACHED (20s) + refreshes in the background, because the underlying check runs the
// `claude auth status` / codex CLI for EVERY account (~1-2s each) — doing that inline on every dashboard
// frame was the ~4.6s "dashboard loads late" stall. Never block the caller: return the last value, refresh async.
var (
	survMu  sync.Mutex
	survVal bool
	survAt  time.Time
	survRef int32
)

func survivalActive(cfg *Config) bool {
	survMu.Lock()
	v, fresh := survVal, !survAt.IsZero() && time.Since(survAt) < 20*time.Second
	survMu.Unlock()
	if fresh {
		return v
	}
	go refreshSurvival(cfg)
	return v // stale (or false on the very first call) — fills in within ~2s, never blocks the frame
}

func refreshSurvival(cfg *Config) {
	if !atomic.CompareAndSwapInt32(&survRef, 0, 1) {
		return
	}
	defer atomic.StoreInt32(&survRef, 0)
	val := !claudeAnyAvailable(cfg) && codexAnyAvailable(cfg)
	survMu.Lock()
	survVal, survAt = val, time.Now()
	survMu.Unlock()
}

// allowJobNow gates a job against survival mode: in survival, only SAFE (report/test/read) jobs run.
func allowJobNow(cfg *Config, name string) bool {
	if !survivalActive(cfg) {
		return true
	}
	return safeInSurvival[strings.ToLower(name)]
}

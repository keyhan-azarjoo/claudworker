package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// acctstate persists per-account login + usage so the dashboard is DURABLE:
//   - Last-known-good usage is remembered, so a PAUSED account (or one whose token is briefly
//     rate-limited / expired) still shows its bars (marked ~stale) instead of a bare "—".
//   - "Logged in on the agent site" is sticky: once an account is signed in here, it stays signed
//     in on the dashboard even if the owner later logs OUT of Claude Code in VS Code (which clears
//     the shared keychain). Only an EXPLICIT logout on the agent site (the 🗑/logout button) signs
//     it out. A read-only copy of the token is kept so usage keeps working after a VS Code logout.
//   - We never REFRESH/rotate the OAuth token ourselves — that could invalidate the CLI's own
//     refresh token and break the real login (never-break rule). We only read what's there.

type acctState struct {
	Week          int    `json:"week"`
	HourField     int    `json:"hour"`
	WeekReset     int    `json:"weekReset"`
	HourReset     int    `json:"hourReset"`
	TS            int64  `json:"ts"`       // unix sec of the last good usage
	Token         string `json:"token"`    // read-only copy, for usage after a VS Code logout
	LoggedIn      bool   `json:"loggedIn"` // has been signed in on the agent site
	SiteLoggedOut bool   `json:"siteOut"`  // explicitly logged out HERE (overrides LoggedIn)
}

var (
	asMu    sync.Mutex
	asCache map[string]*acctState
	asRoot  string
	cfgRef  *Config // set once at serve() start; lets cfg-less helpers reach durable state
)

func stateDir(cfg *Config) string { return filepath.Join(cfg.root, "state") }
func statePath(cfg *Config) string { return filepath.Join(stateDir(cfg), "accounts.json") }

func loadAcctState(cfg *Config) map[string]*acctState {
	asMu.Lock()
	defer asMu.Unlock()
	if asCache != nil && asRoot == cfg.root {
		return asCache
	}
	asCache = map[string]*acctState{}
	asRoot = cfg.root
	if b, err := os.ReadFile(statePath(cfg)); err == nil {
		_ = json.Unmarshal(b, &asCache)
	}
	return asCache
}

func saveAcctState(cfg *Config) {
	asMu.Lock()
	defer asMu.Unlock()
	if asCache == nil {
		return
	}
	_ = os.MkdirAll(stateDir(cfg), 0o700)
	if b, err := json.MarshalIndent(asCache, "", "  "); err == nil {
		_ = osWriteBytes(statePath(cfg), b)
		_ = os.Chmod(statePath(cfg), 0o600) // token copy lives here — keep it private
	}
}

func acctGet(cfg *Config, label string) *acctState {
	m := loadAcctState(cfg)
	asMu.Lock()
	s := m[label]
	asMu.Unlock()
	return s
}

// rememberUsage stores a fresh, successful usage read as the account's last-known-good.
func rememberUsage(cfg *Config, label string, ui UsageInfo, token string) {
	m := loadAcctState(cfg)
	asMu.Lock()
	s := m[label]
	if s == nil {
		s = &acctState{}
		m[label] = s
	}
	s.Week, s.HourField = ui.Week, ui.Hour
	s.WeekReset, s.HourReset = ui.WeekResetMin, ui.HourResetMin
	s.TS = time.Now().Unix()
	if token != "" {
		s.Token = token
	}
	s.LoggedIn = true
	s.SiteLoggedOut = false
	asMu.Unlock()
	saveAcctState(cfg)
}

// rememberLogin marks an account signed-in on the agent site (sticky) and keeps a token copy.
func rememberLogin(cfg *Config, label, token string) {
	m := loadAcctState(cfg)
	asMu.Lock()
	s := m[label]
	if s == nil {
		s = &acctState{}
		m[label] = s
	}
	s.LoggedIn = true
	s.SiteLoggedOut = false
	if token != "" {
		s.Token = token
	}
	asMu.Unlock()
	saveAcctState(cfg)
}

// siteLogout records an EXPLICIT logout on the agent site (signs it out here + drops the token copy).
func siteLogout(cfg *Config, label string) {
	m := loadAcctState(cfg)
	asMu.Lock()
	s := m[label]
	if s == nil {
		s = &acctState{}
		m[label] = s
	}
	s.SiteLoggedOut = true
	s.LoggedIn = false
	s.Token = ""
	asMu.Unlock()
	saveAcctState(cfg)
}

// siteLogin clears an explicit site-logout (so re-logging-in on the site works again).
func siteLogin(cfg *Config, label string) {
	m := loadAcctState(cfg)
	asMu.Lock()
	s := m[label]
	if s == nil {
		s = &acctState{}
		m[label] = s
	}
	s.SiteLoggedOut = false
	s.LoggedIn = true
	asMu.Unlock()
	saveAcctState(cfg)
}

// durableLoggedIn: sticky login — true if live-logged-in, OR previously signed in here and NOT
// explicitly logged out on the site (so a VS Code logout doesn't sign the dashboard out).
func durableLoggedIn(cfg *Config, label string, liveLoggedIn bool) bool {
	s := acctGet(cfg, label)
	if s != nil && s.SiteLoggedOut {
		return false
	}
	if liveLoggedIn {
		return true
	}
	return s != nil && s.LoggedIn
}

// storedToken returns the read-only token copy kept for usage after a VS Code logout ("" if none).
func storedToken(cfg *Config, label string) string {
	s := acctGet(cfg, label)
	if s != nil && !s.SiteLoggedOut {
		return s.Token
	}
	return ""
}

// lastUsage returns the last-known-good usage for an account (ok=false if never recorded).
func lastUsage(cfg *Config, label string) (UsageInfo, bool) {
	s := acctGet(cfg, label)
	if s == nil || s.TS == 0 {
		return UsageInfo{}, false
	}
	return UsageInfo{Hour: s.HourField, Week: s.Week, HourResetMin: s.HourReset, WeekResetMin: s.WeekReset, OK: true}, true
}

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var runnerCounter uint64

// tokenFor returns the OAuth token for an account. On macOS, Claude stores each profile's token
// in the KEYCHAIN — the default under "Claude Code-credentials" and a CLAUDE_CONFIG_DIR profile
// under "Claude Code-credentials-<first 8 hex of sha256(absolute config dir)>". So a SECONDARY
// claude account's token IS readable (it has its own keychain slot) → its usage works too.
func tokenFor(acct *Account) string {
	cd := ""
	if acct != nil {
		cd = acct.ConfigDir
	}
	// keychain token — auto-refreshed if it has expired (write-back keeps the CLI in sync)
	if t := freshClaudeToken(cd); t != "" {
		return t
	}
	// fallback: file-based creds (non-macOS / older setups)
	if cd != "" {
		if b, err := os.ReadFile(filepath.Join(expandHome(cd), ".credentials.json")); err == nil {
			var m map[string]any
			if json.Unmarshal(b, &m) == nil {
				if s := findToken(m); s != "" {
					return s
				}
			}
		}
	}
	// final fallback: the durable read-only copy kept from when this account was last logged in
	// (so usage + login survive the owner logging out of Claude Code in VS Code). cfgRef set by main.
	if acct != nil && cfgRef != nil {
		if s := storedToken(cfgRef, acct.Label); s != "" {
			return s
		}
	}
	return ""
}

// keychainTokenFor reads the OAuth token from the right macOS keychain slot for a profile.
func keychainTokenFor(configDir string) string {
	svc := "Claude Code-credentials"
	if configDir != "" {
		h := sha256.Sum256([]byte(expandHome(configDir)))
		svc = "Claude Code-credentials-" + hex.EncodeToString(h[:])[:8]
	}
	out, err := exec.Command("security", "find-generic-password", "-s", svc, "-w").Output()
	if err != nil {
		return ""
	}
	var m map[string]any
	if json.Unmarshal(out, &m) != nil {
		return ""
	}
	return findToken(m)
}

// claudeLoggedIn is the AUTHORITATIVE logged-in check for a Claude profile: `claude auth status`
// (respects CLAUDE_CONFIG_DIR) instead of guessing a credentials file path — on macOS claude may
// store the token in the keychain, so a logged-in secondary account had no .credentials.json and
// wrongly showed "logged out". Cached ~15s (it spawns a subprocess).
var (
	clInMu  sync.Mutex
	clInVal = map[string]bool{}
	clInAt  = map[string]time.Time{}
)

func claudeLoggedIn(configDir string) bool {
	key := configDir
	clInMu.Lock()
	if t, ok := clInAt[key]; ok && time.Since(t) < 15*time.Second {
		v := clInVal[key]
		clInMu.Unlock()
		return v
	}
	clInMu.Unlock()
	cmd := exec.Command("claude", "auth", "status")
	if configDir != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+expandHome(configDir))
	}
	out, _ := cmd.Output()
	var s struct {
		LoggedIn bool `json:"loggedIn"`
	}
	_ = json.Unmarshal(out, &s)
	clInMu.Lock()
	clInVal[key], clInAt[key] = s.LoggedIn, time.Now()
	clInMu.Unlock()
	return s.LoggedIn
}

// claudeEmail returns the REAL signed-in Claude account email for a profile
// (its own configDir), or the Mac's default login when configDir is empty.
// Returns "" if not logged in / unknown. This is what we DISPLAY, so the card
// shows who is actually logged in (e.g. colab.spt@gmail.com) instead of a stale
// hand-typed label. Each profile dir is isolated, so account-mates sharing a
// login never see each other's local Claude sessions — those stay on this Mac.
func claudeEmail(configDir string) string {
	var path string
	if configDir != "" {
		path = filepath.Join(expandHome(configDir), ".claude.json")
	} else {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".claude.json")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	var m struct {
		OauthAccount struct {
			EmailAddress string `json:"emailAddress"`
		} `json:"oauthAccount"`
	}
	if json.Unmarshal(b, &m) != nil {
		return ""
	}
	return m.OauthAccount.EmailAddress
}

// defaultWeeklyCap = the weekly utilization % at which to auto-pause when nothing is set.
// The owner's rule: pause ~2% before Claude's weekly "yellow" warning. We treat 100% as the
// weekly limit, so the default pauses at 98% (2% of headroom). Tune per-account via cap%.
const defaultWeeklyCap = 98

// bannerUsage returns the UsageInfo for the account chosen to drive the top banner
// (cfg.BannerAccount), falling back to the Mac's default (keychain) login.
func bannerUsage(cfg *Config) UsageInfo {
	if cfg != nil && cfg.BannerAccount != "" {
		for i := range cfg.Accounts {
			a := cfg.Accounts[i]
			if a.Label == cfg.BannerAccount && a.Engine != "codex" {
				if tok := tokenFor(&a); tok != "" {
					return usageInfoCached(tok)
				}
			}
		}
	}
	return usageInfoCached(keychainToken())
}

// clearCreds logs an account OUT in place (deletes its saved token) but KEEPS the account
// in the list — so logout shows "not logged in" instead of removing the slot. The default
// keychain login is left alone (clearing it would sign the whole Mac out of Claude).
func clearCreds(acct *Account) {
	if acct == nil {
		return
	}
	if acct.Engine == "codex" {
		dir := expandHome(acct.ConfigDir)
		if acct.ConfigDir == "" {
			home, _ := os.UserHomeDir()
			dir = filepath.Join(home, ".codex")
		}
		_ = os.Remove(filepath.Join(dir, "auth.json"))
		return
	}
	if acct.ConfigDir != "" {
		_ = os.Remove(filepath.Join(expandHome(acct.ConfigDir), ".credentials.json"))
	}
}

// codexLoggedIn reports whether a Codex profile actually has saved auth (auth.json).
// A freshly ADDED codex account has none → it must show "logged out", not assumed logged-in.
func codexLoggedIn(configDir string) bool {
	var path string
	if configDir != "" {
		path = filepath.Join(expandHome(configDir), "auth.json")
	} else {
		home, _ := os.UserHomeDir()
		path = filepath.Join(home, ".codex", "auth.json")
	}
	if b, err := os.ReadFile(path); err == nil && len(b) > 2 {
		return true
	}
	return false
}

// capOverride: once the owner manually RESUMES an account that hit its weekly cap, we set an
// override so available() stops re-pausing it until the weekly quota resets (new week).
var (
	capOverrideMap map[string]bool = map[string]bool{}
	capOverrideMu  sync.Mutex
)

func setCapOverride(label string) {
	capOverrideMu.Lock()
	capOverrideMap[label] = true
	capOverrideMu.Unlock()
}
func clearCapOverride(label string) {
	capOverrideMu.Lock()
	delete(capOverrideMap, label)
	capOverrideMu.Unlock()
}
func capOverride(label string) bool {
	capOverrideMu.Lock()
	defer capOverrideMu.Unlock()
	return capOverrideMap[label]
}

// pauseLimit is the % at which this account should stop. Per-account override, else global,
// else the default (2% before the weekly yellow warning).
func pauseLimit(cfg *Config, acct *Account) int {
	if acct != nil && acct.PausePct > 0 {
		return acct.PausePct
	}
	if cfg.UsagePausePct > 0 {
		return cfg.UsagePausePct
	}
	return defaultWeeklyCap
}

// effectiveLimit applies the time-aware "after-midnight boost": while it's after the night
// start hour and still >=3h before the quota reset, use the higher NightPausePct.
func effectiveLimit(cfg *Config, acct *Account) int {
	base := pauseLimit(cfg, acct)
	if acct == nil || acct.NightPausePct <= 0 || acct.ResetHour <= 0 {
		return base
	}
	h := time.Now().Hour()
	if h >= acct.NightStartHour && h < acct.ResetHour && (acct.ResetHour-h) >= 3 {
		return acct.NightPausePct
	}
	return base
}

// paceConcurrency maps a speed name → how many agents may run AT ONCE on one account.
// Range: slow 1 … fast 30 (these are the PREFILL defaults; the owner can override per account).
func paceConcurrency(pace string) int {
	switch pace {
	case "slow":
		return 1
	case "light":
		return 4
	case "medium":
		return 8
	case "high":
		return 12
	case "fast":
		return 20
	}
	return 8 // unset → medium
}

// acctConcurrency is how many agents this account may run concurrently: an explicit per-account
// "run at once" if set, else derived from the account's speed (falling back to the global pace).
func acctConcurrency(cfg *Config, acct *Account) int {
	if acct != nil && acct.Concurrency > 0 {
		return acct.Concurrency
	}
	pace := cfg.Pace
	if acct != nil && acct.Pace != "" {
		pace = acct.Pace
	}
	return paceConcurrency(pace)
}

// paceFanout maps a speed name → sub-agents per agent (MO_FANOUT). Kept modest to avoid fork-bombs.
func paceFanout(pace string) int {
	switch pace {
	case "light":
		return 0
	case "medium":
		return 1
	case "high":
		return 2
	case "fast":
		return 3
	}
	return 0 // slow / unset → no sub-agents
}

// acctFanout is this account's sub-agents-per-agent: explicit per-account override, else from speed.
func acctFanout(cfg *Config, acct *Account) int {
	if acct != nil && acct.Fanout > 0 {
		return acct.Fanout
	}
	pace := cfg.Pace
	if acct != nil && acct.Pace != "" {
		pace = acct.Pace
	}
	return paceFanout(pace)
}

// fleetCap is the TOTAL concurrent-agent ceiling = sum of each AVAILABLE account's own cap (so more
// accounts / faster accounts ⇒ more agents). Replaces the old global paceCap that wrongly limited
// the whole fleet to ~2 regardless of how many fast accounts were logged in. Hard-bounded for safety.
func fleetCap(cfg *Config) int {
	if len(cfg.Accounts) == 0 {
		return acctConcurrency(cfg, nil)
	}
	total := 0
	seenLogin := map[string]bool{} // dedupe: two slots on the SAME login = one account's quota, not two
	for i := range cfg.Accounts {
		a := cfg.Accounts[i]
		if isAcctPaused(a.Label) {
			continue // paused accounts contribute no slots
		}
		if a.Engine == "" || a.Engine == "claude" {
			if em := claudeEmail(a.ConfigDir); em != "" {
				if seenLogin[em] {
					continue // already counted this login
				}
				seenLogin[em] = true
			}
		}
		total += acctConcurrency(cfg, &a)
	}
	if total < 1 {
		total = 1
	}
	if total > 60 { // absolute ceiling (the resource guards still throttle below this)
		total = 60
	}
	return total
}

// available reports whether we may start a worker on this account right now.
// Non-claude engines (codex, …) have no readable Claude cap → always available (tolerate failures).
func available(cfg *Config, acct *Account) bool { return availableEx(cfg, acct, false) }

// availableEx is available() with an escape hatch: ignorePacing=true skips ONLY the soft weekly-pace
// throttle (used by the failover pass so the fleet carries on with the one account that can still work
// when every other account is hard-down). All HARD limits — paused, cooldown, concurrency, schedule,
// logged-out, the weekly cap — are always enforced.
func availableEx(cfg *Config, acct *Account, ignorePacing bool) bool {
	if acct != nil && isAcctPaused(acct.Label) {
		return false // owner paused this account from the dashboard
	}
	if acct != nil && inCooldown(acct.Label) {
		return false // recently failed (e.g. usage limit) — skip until it recovers
	}
	if acct != nil && acctRunningCount(acct.Label) >= acctConcurrency(cfg, acct) {
		return false // this account is already running its max concurrent agents (its speed setting)
	}
	if acct != nil && !withinSchedule(acct) {
		return false // outside this account's operating days/hours
	}
	if acct != nil && acct.Engine != "" && acct.Engine != "claude" {
		return true
	}
	// claude: a logged-OUT account is skipped silently until re-login (authoritative CLI check;
	// the token may be in the keychain, not a file). Never fall back onto the owner's own login.
	if acct != nil && acct.ConfigDir != "" {
		if !claudeLoggedIn(acct.ConfigDir) {
			return false
		}
	}
	tok := tokenFor(acct)
	if tok == "" {
		if acct != nil && acct.ConfigDir == "" {
			return false // default account with no keychain creds = logged out
		}
		return true // logged-in profile whose token we can't read for usage → use it, no usage gating
	}
	ui := usageInfoCached(tok)
	if !ui.OK {
		return true // logged in but usage endpoint hiccup → fail open (don't stall)
	}
	// WEEKLY-only cap (owner's rule): pause on the weekly limit, ignore the 5h window for pausing.
	limit := effectiveLimit(cfg, acct)
	if acct != nil {
		if ui.Week >= limit {
			// Hit the weekly cap. Unless the owner already resumed past it (override), PERSISTENTLY
			// pause and wait for a manual resume — it won't auto-resume mid-week.
			if !capOverride(acct.Label) {
				setAcctPaused(acct.Label, true)
				return false
			}
			// override active → keep working past the cap until the week resets
		} else {
			clearCapOverride(acct.Label) // weekly dropped below cap (new week) → reset the override
		}
	}
	// Per-account weekly pacing: hold once this account is at/above its time-ramped weekly target.
	pace := cfg.Pace
	if acct != nil && acct.Pace != "" {
		pace = acct.Pace
	}
	if !ignorePacing && ui.WeekResetMin >= 0 && pace != "" {
		const weekMin = 7 * 24 * 60.0
		elapsed := 1 - float64(ui.WeekResetMin)/weekMin
		if elapsed < 0 {
			elapsed = 0
		}
		if elapsed > 1 {
			elapsed = 1
		}
		if float64(ui.Week) >= weeklyEndTarget(pace)*elapsed {
			return false
		}
	}
	return true
}

// withinSchedule reports whether NOW is inside an account's operating window (days + hours).
func withinSchedule(acct *Account) bool {
	if acct == nil || (acct.FromHour == 0 && acct.ToHour == 0 && acct.Days == "") {
		return true // no schedule set = 24/7
	}
	now := time.Now()
	if acct.FromHour != 0 || acct.ToHour != 0 {
		h := now.Hour()
		if h < acct.FromHour || h >= acct.ToHour {
			return false
		}
	}
	if acct.Days != "" {
		wd := int(now.Weekday()) // Sun=0..Sat=6
		if wd == 0 {
			wd = 7 // map Sun→7 so "1-5" = Mon..Fri
		}
		if !dayInRange(acct.Days, wd) {
			return false
		}
	}
	return true
}

// dayInRange parses "1-5" or "1,3,5" (Mon=1..Sun=7) and tests membership.
func dayInRange(spec string, wd int) bool {
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if strings.Contains(part, "-") {
			ab := strings.SplitN(part, "-", 2)
			a, _ := strconv.Atoi(strings.TrimSpace(ab[0]))
			b, _ := strconv.Atoi(strings.TrimSpace(ab[1]))
			if a <= wd && wd <= b {
				return true
			}
		} else if n, _ := strconv.Atoi(part); n == wd {
			return true
		}
	}
	return false
}

// selectRunner round-robins across runners and returns the first AVAILABLE one.
// ok=false means every account is at/over its pause limit (caller should skip + notify).
func selectRunner(cfg *Config) (*Account, bool) {
	if len(cfg.Accounts) == 0 {
		return nil, available(cfg, nil)
	}
	n := len(cfg.Accounts)
	start := int(atomic.AddUint64(&runnerCounter, 1))
	// pass 1 — normal: respect each account's weekly pacing throttle
	for k := 0; k < n; k++ {
		a := cfg.Accounts[(start+k)%n]
		if available(cfg, &a) {
			ac := a
			return &ac, true
		}
	}
	// pass 2 — FAILOVER ("carry on"): every account is held under pacing, so use the one that can STILL
	// work, ignoring only the soft pace throttle (hard limits — paused, logged-out, weekly cap, cooldown,
	// concurrency — still apply). This keeps Claude going when codex is down (and vice-versa).
	for k := 0; k < n; k++ {
		a := cfg.Accounts[(start+k)%n]
		if availableEx(cfg, &a, true) {
			ac := a
			return &ac, true
		}
	}
	return nil, false
}

// Per-account live worker count (how many agents are running on each account right now).
// Pausing an account only stops NEW launches; these in-flight workers run to completion,
// so the owner can watch this count drain to 0 before the account is fully idle.
var (
	acctRunning   = map[string]int{}
	acctRunningMu sync.Mutex
)

func incAcctRunning(label string) {
	acctRunningMu.Lock()
	acctRunning[label]++
	acctRunningMu.Unlock()
}
func decAcctRunning(label string) {
	acctRunningMu.Lock()
	if acctRunning[label] > 0 {
		acctRunning[label]--
	}
	acctRunningMu.Unlock()
}
func acctRunningCount(label string) int {
	acctRunningMu.Lock()
	defer acctRunningMu.Unlock()
	return acctRunning[label]
}

type AcctUsage struct {
	Label     string `json:"label"`
	Email     string `json:"email"` // REAL signed-in account email (display); label stays the operational key
	Running   int    `json:"running"` // agents working on this account right now
	Engine    string `json:"engine"`
	Pct       int    `json:"pct"`  // primary = weekly % (-1 = unknown/not-readable)
	Hour      int    `json:"hour"` // 5h %
	Week      int    `json:"week"` // 7d %
	HourReset int    `json:"hourReset"`
	WeekReset int    `json:"weekReset"`
	Cap       int    `json:"cap"`
	Note      string `json:"note"`
	Paused    bool   `json:"paused"`
	Stale     bool   `json:"stale"` // usage shown is last-known-good (live fetch unavailable)
	Duplicate bool   `json:"duplicate"` // this slot is the SAME login as another (shares its quota)
	DupOf     string `json:"dupOf"`     // the label of the first slot using this same login
	// per-account settings (editable on the account page)
	HardPause bool   `json:"hardPause"`
	UseSonnet bool   `json:"useSonnet"` // run this account on Sonnet (cheaper) instead of Opus
	Days      string `json:"days"`
	FromHour  int    `json:"fromHour"`
	ToHour    int    `json:"toHour"`
	PausePct  int    `json:"pausePct"`
	ConfigDir string `json:"configDir"`
	Cmd       string `json:"cmd"`
	LoggedIn  bool   `json:"loggedIn"`
	Pace        string `json:"pace"`
	Effort      string `json:"effort"`
	Concurrency int    `json:"concurrency"`     // per-account "run at once" (0 = from speed)
	ConcEffective int  `json:"concEffective"`   // run-at-once actually in effect (auto from speed)
	Fanout      int    `json:"fanout"`          // per-account sub-agents
	FanEffective int   `json:"fanEffective"`    // sub-agents actually in effect (auto from speed)
}

// Per-account pause (works for Claude AND Codex runners) — set from the dashboard.
var (
	acctPaused   = map[string]bool{}
	acctPauseMu  sync.Mutex
)

func setAcctPaused(label string, p bool) {
	acctPauseMu.Lock()
	acctPaused[label] = p
	acctPauseMu.Unlock()
	auMu.Lock() // refresh the usage panel immediately
	auAt = time.Time{}
	auMu.Unlock()
}
func isAcctPaused(label string) bool {
	acctPauseMu.Lock()
	defer acctPauseMu.Unlock()
	return acctPaused[label]
}

// Cooldown: when an account's run fails (esp. "usage limit"), skip it for a while, then auto-recover.
var (
	acctCooldown = map[string]time.Time{}
	cdMu         sync.Mutex
)

func setCooldown(label string, d time.Duration) {
	cdMu.Lock()
	acctCooldown[label] = time.Now().Add(d)
	cdMu.Unlock()
}
func inCooldown(label string) bool {
	cdMu.Lock()
	defer cdMu.Unlock()
	t, ok := acctCooldown[label]
	return ok && time.Now().Before(t)
}

// cooldownRemainingMin returns minutes until an account's cooldown ends (-1 if none).
func cooldownRemainingMin(label string) int {
	cdMu.Lock()
	defer cdMu.Unlock()
	t, ok := acctCooldown[label]
	if !ok || !time.Now().Before(t) {
		return -1
	}
	return int(time.Until(t).Minutes()) + 1
}

var (
	auCache   []AcctUsage
	auAt      time.Time
	auMu      sync.Mutex
	auRefresh int32
)

// accountsUsage returns the cached per-account usage INSTANTLY (stale-while-revalidate; the
// usage-API calls never block the SSE). A paused/config change resets the cache → async refresh.
func accountsUsage(cfg *Config) []AcctUsage {
	auMu.Lock()
	c, age := auCache, time.Since(auAt)
	auMu.Unlock()
	if c != nil && age < 30*time.Second {
		return c
	}
	go refreshAccountsUsage(cfg)
	return c
}

func refreshAccountsUsage(cfg *Config) []AcctUsage {
	if !atomic.CompareAndSwapInt32(&auRefresh, 0, 1) {
		auMu.Lock()
		c := auCache
		auMu.Unlock()
		return c
	}
	defer atomic.StoreInt32(&auRefresh, 0)
	mk := func(label, engine string, ui UsageInfo, cap int, note string, pz bool) AcctUsage {
		pct := ui.Week
		if !ui.OK {
			pct = -1
		}
		return AcctUsage{Label: label, Engine: engine, Pct: pct, Hour: ui.Hour, Week: ui.Week,
			HourReset: ui.HourResetMin, WeekReset: ui.WeekResetMin, Cap: cap, Note: note, Paused: pz}
	}
	out := []AcctUsage{}
	if len(cfg.Accounts) == 0 {
		out = append(out, mk("default (admin)", "claude", usageInfoCached(keychainToken()), cfg.UsagePausePct, "", false))
	}
	for _, a := range cfg.Accounts {
		a := a
		pz := isAcctPaused(a.Label)
		var u AcctUsage
		if a.Engine != "" && a.Engine != "claude" {
			// Codex usage comes from its local session logs (real 5h + weekly %). Plus live state.
			li := codexLoggedIn(a.ConfigDir)
			cu := codexUsageCached(a.ConfigDir)
			note := "active"
			if !li {
				note = "logged out"
			} else if cm := cooldownRemainingMin(a.Label); cm > 0 {
				note = "rate-limited · retry ~" + strconv.Itoa(cm) + "m"
			}
			u = AcctUsage{Label: a.Label, Engine: a.Engine, Pct: -1, Hour: -1, Week: -1, HourReset: -1, WeekReset: -1, Note: note, Paused: pz}
			if cu.OK {
				u.Week, u.Hour = cu.WeekPct, cu.HourPct
				u.WeekReset, u.HourReset = cu.WeekResetMin, cu.HourResetMin
				u.Pct = cu.WeekPct
			}
		} else {
			tok := tokenFor(&a) // for the durable-login token copy + the live login check
			liveLI := (a.ConfigDir == "" && tok != "") || claudeLoggedIn(a.ConfigDir)
			// Sticky login: stays "logged in" on the dashboard after a VS Code logout, until an
			// EXPLICIT logout here.
			li := durableLoggedIn(cfg, a.Label, liveLI)
			cap := effectiveLimit(cfg, &a)
			if !li {
				u = AcctUsage{Label: a.Label, Engine: "claude", Pct: -1, Hour: -1, Week: -1, HourReset: -1, WeekReset: -1, Cap: cap, Note: "logged out", Paused: pz}
			} else {
				// USAGE comes from the real /usage slash command (NOT the HTTP endpoint).
				su := slashUsageCached(cfg, a.ConfigDir)
				if su.OK {
					ui := UsageInfo{Hour: su.Session, Week: su.Week, HourResetMin: su.SessionResetMin, WeekResetMin: su.WeekResetMin, OK: true}
					rememberUsage(cfg, a.Label, ui, tok) // last-known-good + keep durable login
					u = mk(a.Label, "claude", ui, cap, "", pz)
				} else if su.NotReady {
					// this profile needs an interactive login before /usage can read it. Show NO bar
					// (not a stale number) — the owner can 'login by URL' to make /usage work for it.
					rememberLogin(cfg, a.Label, tok)
					u = AcctUsage{Label: a.Label, Engine: "claude", Pct: -1, Hour: -1, Week: -1, HourReset: -1, WeekReset: -1, Cap: cap, Note: "/usage needs login — tap 'login by URL'", Paused: pz}
				} else {
					// probe pending / transient miss → show last-known while it reads
					rememberLogin(cfg, a.Label, tok)
					if lu, has := lastUsage(cfg, a.Label); has {
						u = mk(a.Label, "claude", lu, cap, "reading /usage…", pz)
						u.Stale = true
					} else {
						u = AcctUsage{Label: a.Label, Engine: "claude", Pct: -1, Hour: -1, Week: -1, HourReset: -1, WeekReset: -1, Cap: cap, Note: "reading /usage…", Paused: pz}
					}
				}
			}
		}
		u.HardPause, u.Days, u.FromHour, u.ToHour, u.PausePct = a.HardPause, a.Days, a.FromHour, a.ToHour, a.PausePct
		u.UseSonnet = a.UseSonnet
		u.ConfigDir, u.Cmd = a.ConfigDir, a.Cmd
		u.Pace, u.Effort = a.Pace, a.Effort
		u.Concurrency, u.Fanout = a.Concurrency, a.Fanout
		u.ConcEffective = acctConcurrency(cfg, &a)
		u.FanEffective = acctFanout(cfg, &a)
		if a.Engine == "codex" {
			u.LoggedIn = codexLoggedIn(a.ConfigDir) // real auth, not an assumption
		} else {
			// authoritative live check (token may live in the keychain, not a file), made STICKY:
			// stays logged in here after a VS Code logout until an explicit logout on the site.
			liveLI := a.ConfigDir == "" && tokenFor(&a) != "" || claudeLoggedIn(a.ConfigDir)
			u.LoggedIn = durableLoggedIn(cfg, a.Label, liveLI)
			u.Email = claudeEmail(a.ConfigDir) // show who is REALLY logged in
		}
		u.Running = acctRunningCount(a.Label)
		out = append(out, u)
	}
	// FLAG DUPLICATE LOGINS: two slots signed into the SAME email aren't two accounts — they share one
	// quota and show identical usage. Mark the later one so the UI warns instead of silently doubling.
	seenEmail := map[string]string{}
	for i := range out {
		if out[i].Engine == "codex" || out[i].Email == "" {
			continue
		}
		if first, ok := seenEmail[out[i].Email]; ok {
			out[i].Duplicate = true
			out[i].DupOf = first
		} else {
			seenEmail[out[i].Email] = out[i].Label
		}
	}
	auMu.Lock()
	auCache, auAt = out, time.Now()
	auMu.Unlock()
	return out
}

var (
	lastNotify time.Time
	notifyMu   sync.Mutex
)

// notifyOwnerThrottled: REPORT pings to Telegram are DISABLED (owner: no report messages).
// Approval requests use a separate explicit flow (multimedia approval), not this.
func notifyOwnerThrottled(cfg *Config, msg string) {
	logf(cfg, "notify", "(telegram reports off) %s", msg)
}

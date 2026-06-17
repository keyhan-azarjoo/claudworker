package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// ── DETERMINISTIC LAUNCH-GATE ────────────────────────────────────────────────────────────────────
// "Anything a regular function can decide is a trigger function, not an AI run." Before the
// boost/scheduler launches an agent, hasWork() answers — in PLAIN CODE, zero tokens — "is there
// actually something for this agent to do?" If not, the launch is SKIPPED. An AI agent only fires
// when there is genuine reasoning work (a ticket at its stage, a new error, a new message, a failure).
// A staleness floor still lets the human-facing checks run occasionally so nothing is ever missed.

var (
	firedMu  sync.Mutex
	firedAt  = map[string]time.Time{}
	staleFloor = map[string]time.Duration{ // run at least this often even if change-detection is quiet
		"inbox":        30 * time.Minute,
		"reliability":  60 * time.Minute,
		"sentry-catch": 60 * time.Minute,
		"sentry-clean": 90 * time.Minute,
		"planner":      30 * time.Minute,
		// multimedia is handled specially in hasWork (gated on no-pending-item), not via this floor
		// safety net: even if pipeline handoff labels lag, these run at least this often so nothing stalls.
		// Reviewers throttled 40m->180m (2026-06-14): the 40m idle-floor dominated their token spend
		// (security-review ran ~36x/day even when idle). They still fire immediately on real review work
		// (jqlCount(review labels)>0), so this only cuts the idle "run anyway" cadence.
		"code-review":     180 * time.Minute,
		"security-review": 180 * time.Minute,
		"qa":              40 * time.Minute,
		"e2e-test":        45 * time.Minute,
		"random-tester":   45 * time.Minute, // exploratory monkey tester; periodic within the test window
		"visual-test":     6 * time.Hour, // visual-test is nightly-ish + heavy; don't run it often
		"deploy":          15 * time.Minute, // backstop: catch undeployed green work / a live fix promptly
		"release":         30 * time.Minute,
		// NOTE: hardware-test has NO floor — it runs ONLY when real hardware is connected (see hasWork);
		// otherwise its tickets stay pending until you plug in + tap Recheck.
	}
)

// programmingTeam = the costly Opus engineering agents the in-flight cap limits.
var programmingTeam = map[string]bool{"app": true, "backend": true, "firmware": true, "uiux": true}

// paceNum maps the speed setting to a number (slow=1 … fast=5; default medium=3).
func paceNum(cfg *Config) int {
	switch strings.ToLower(strings.TrimSpace(cfg.Pace)) {
	case "slow":
		return 1
	case "light":
		return 2
	case "high":
		return 4
	case "fast":
		return 5
	default:
		return 3 // medium
	}
}

// inFlightLimit = how many tickets the pump keeps in flight / the concurrent-programming cap. The owner's
// explicit cap is the MEDIUM baseline; at higher speeds we promote MORE of the backlog so idle capacity is
// used (the owner's "use that time, especially when speed > medium"). Never below the explicit cap.
func inFlightLimit(cfg *Config) int {
	base := cfg.InFlightCap
	if base <= 0 {
		base = 7
	}
	switch paceNum(cfg) {
	case 5: // fast → double
		return base * 2
	case 4: // high → +50%
		return base + base/2
	default: // medium and below → the owner's cap as set
		return base
	}
}

// progRunning counts how many PROGRAMMING agents are currently running (for the in-flight cap).
func progRunning(reg *Registry) int {
	n := 0
	for _, a := range reg.snapshot() {
		if programmingTeam[a.Name] && a.State == "running" {
			n++
		}
	}
	return n
}

// wakeDeviceTests is called when a real device newly CONNECTS (e.g. you plug in your phone at night):
// it clears the staleness floor for the device-test agents so they run promptly to use the device,
// instead of waiting out their timer. This is the "connect hardware → it tests it" trigger.
func wakeDeviceTests() {
	firedMu.Lock()
	for _, n := range []string{"hardware-test", "e2e-test", "visual-test", "qa", "random-tester"} {
		delete(firedAt, n)
	}
	firedMu.Unlock()
}

func markFired(name string) {
	firedMu.Lock()
	firedAt[name] = time.Now()
	firedMu.Unlock()
}
func lastFired(name string) time.Time {
	firedMu.Lock()
	defer firedMu.Unlock()
	return firedAt[name]
}

// hasWork: the deterministic gate. true ⇒ there's real work, launch the agent. false ⇒ skip (0 tokens).
func hasWork(cfg *Config, name string) bool {
	// MULTIMEDIA: never generate a new piece while one is awaiting approval/publish (a plain gate);
	// otherwise produce one every ~4h. Pure trigger logic — no AI decides this.
	if name == "multimedia" {
		if mmHasPending(cfg) {
			return false
		}
		t := lastFired(name)
		return t.IsZero() || time.Since(t) > 4*time.Hour
	}
	// staleness floor: human-facing checks still run on a slow timer so nothing is ever missed
	if f, ok := staleFloor[name]; ok {
		if t := lastFired(name); t.IsZero() || time.Since(t) > f {
			return true
		}
	}
	switch name {
	// pure polling / map / periodic content / batch planning — a plain function or a slow timer does
	// this, NOT a continuous AI run. (planner + sentry-clean still run via the staleness floor above.)
	case "jira-poll", "app-map", "scenario-gen", "planner", "sentry-clean", "random-tester":
		return false // run only via the staleness floor (a slow timer), never continuously
	case "reliability":
		for _, s := range serverHealth(cfg) {
			if !s.Up {
				return true // something is actually DOWN → wake an agent; else the health check is just code
			}
		}
		return false
	case "sentry-catch":
		return sentryNewErrors(cfg) // only when the unresolved count GREW (new errors), not every cycle
	case "inbox":
		return inboxNewMessage(cfg) // only when an owner DM actually changed
	// Engineering: only run when there's work in their lane — a ticket the PUMP put at stage-building,
	// or already In Progress, in their area. (The in-flight cap + pump bound how much that is.)
	case "app", "uiux":
		return engHasWork(cfg, "(app,frontend,ux,ui,edge-ai,redesign,gdpr)") || idleFill(cfg)
	case "backend":
		return engHasWork(cfg, "(backend,api,server,payment,auth)") || idleFill(cfg)
	case "firmware":
		return engHasWork(cfg, "(firmware,hardware,esp32,pcb,device)") || idleFill(cfg)
	// Review / test fire when a handoff lane has tickets (agents set these when they finish a stage).
	case "code-review", "security-review":
		return jqlCount(cfg, `project=SCRUM AND statusCategory!=Done AND labels in (ready-for-review,"stage-review",code-review,security-review)`) > 0 || idleFill(cfg)
	case "qa", "e2e-test", "visual-test":
		return jqlCount(cfg, `project=SCRUM AND statusCategory!=Done AND labels in (ready-for-test,"stage-test",human-test,e2e,visual)`) > 0
	case "hardware-test":
		// pending until hardware is actually plugged in — never burn tokens "testing" absent hardware
		return anyHardwareConnected() && jqlCount(cfg, `project=SCRUM AND statusCategory!=Done AND labels in (hardware,firmware,esp32,pcb,device,hardware-test,ready-for-test)`) > 0
	case "deploy", "release":
		// auto-deploy: run when there's green work to ship — never asks the owner to deploy
		return deployWorkExists(cfg)
	}
	return true // unknown job → don't block
}

// deployWorkExists: deterministic check for "is there green work to ship?" — ready-to-deploy tickets,
// OR a key repo whose development is ahead of origin (committed-but-undeployed, e.g. a live CWE fix).
func deployWorkExists(cfg *Config) bool {
	if jqlCount(cfg, `project=SCRUM AND statusCategory!=Done AND labels="ready-to-deploy"`) > 0 {
		return true
	}
	root := expandHome(cfg.resolve(cfg.ProjectDir))
	for _, r := range []string{
		"Backend/DotNet-WebApi-Backend/DotNet-IoT-MainWebApi",
		"User-Interface/Application/Flutter-IoT-MobileApp/claudworker",
		"User-Interface/WebSites/NextJs-Myotgo-Website",
	} {
		dir := filepath.Join(root, r)
		out := strings.TrimSpace(runc("git -C '"+dir+"' rev-list --count origin/development..development 2>/dev/null", 6*time.Second))
		if out != "" && out != "0" {
			return true
		}
	}
	return false
}

// engHasWork: a programming area has work if a ticket is in its building lane OR already In Progress.
func engHasWork(cfg *Config, areaLabels string) bool {
	return jqlCount(cfg, `project=SCRUM AND statusCategory!=Done AND labels in `+areaLabels+
		` AND (labels="stage-building" OR statusCategory="In Progress")`) > 0
}

// jqlCount = cached Jira count (cheap; ~90s) so the gate never hammers Jira.
var (
	jqlcMu  sync.Mutex
	jqlcVal = map[string]int{}
	jqlcAt  = map[string]time.Time{}
)

func jqlCount(cfg *Config, jql string) int {
	jqlcMu.Lock()
	v, ok := jqlcVal[jql]
	age := time.Since(jqlcAt[jql])
	jqlcMu.Unlock()
	if ok && age < 90*time.Second {
		return v
	}
	n := jqlTotal(cfg, jql)
	if n < 0 {
		n = 0
	}
	jqlcMu.Lock()
	jqlcVal[jql], jqlcAt[jql] = n, time.Now()
	jqlcMu.Unlock()
	return n
}

// sentryNewErrors: true only when the TOTAL unresolved count grew since the last check.
var (
	sentSeen = -1
	sentMu   sync.Mutex
)

func sentryNewErrors(cfg *Config) bool {
	total := 0
	for _, p := range sentryStats(cfg) {
		total += p.Unresolved
	}
	sentMu.Lock()
	defer sentMu.Unlock()
	grew := sentSeen >= 0 && total > sentSeen
	sentSeen = total
	return grew
}

// inboxNewMessage: true only when an OWNER DM transcript changed (a new message arrived). It checks
// only the owner's DM session files (positive chat-id keys in the bridge's .sessions.json), NOT the
// fleet's own agent transcripts — so a busy fleet doesn't look like "new owner messages".
var (
	inboxSeen time.Time
	inboxMu   sync.Mutex
)

func inboxNewMessage(cfg *Config) bool {
	bridge := filepath.Join(cfg.root, "..", "..", "Telegram", "claude_telegram_bridge")
	sb, err := os.ReadFile(filepath.Join(bridge, ".sessions.json"))
	if err != nil {
		return false
	}
	var sess map[string]string
	if json.Unmarshal(sb, &sess) != nil {
		return false
	}
	home, _ := os.UserHomeDir()
	projDir := filepath.Join(home, ".claude", "projects", "-Users-keyhanazarjoo-Documents-Claudworker-Project")
	newest := time.Time{}
	for k, hash := range sess {
		if strings.HasPrefix(k, "-") {
			continue // group chats, not the owner's DM
		}
		if fi, err := os.Stat(filepath.Join(projDir, hash+".jsonl")); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}
	inboxMu.Lock()
	defer inboxMu.Unlock()
	if inboxSeen.IsZero() {
		inboxSeen = newest
		return false // first check = baseline
	}
	if newest.After(inboxSeen) {
		inboxSeen = newest
		return true
	}
	return false
}

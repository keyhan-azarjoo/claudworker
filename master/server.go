package main

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// serve runs the daemon: scheduler + HTTP control panel + SSE live stream.
// persistAgentPause records (or clears) paused agents in the config so a daemon restart keeps the owner's
// choice — e.g. Review/Monitoring/Multimedia stay paused to save tokens until the owner resumes them.
func persistAgentPause(cfg *Config, names []string, paused bool) {
	cfgMu.Lock() // write cfg.PausedAgents
	cur := map[string]bool{}
	for _, n := range cfg.PausedAgents {
		cur[n] = true
	}
	for _, n := range names {
		if n = strings.TrimSpace(n); n == "" || n == "all" {
			continue
		} else if paused {
			cur[n] = true
		} else {
			delete(cur, n)
		}
	}
	out := make([]string, 0, len(cur))
	for n := range cur {
		out = append(out, n)
	}
	cfg.PausedAgents = out
	_ = saveConfig(cfg)
	cfgMu.Unlock()
}

func serve(cfg *Config, addr string) {
	cfgRef = cfg // global ref so token/usage helpers can reach durable account state
	reg := NewRegistry(cfg)
	for _, n := range cfg.PausedAgents { // restore the agents the owner paused (persisted across restarts)
		reg.setPaused(n, true)
	}
	adoptOrphans(cfg, reg) // re-attach workers still running from a previous daemon (so a restart never shows "0 working" while agents are busy)
	go func() {            // reap adopted workers once they finish
		for {
			time.Sleep(30 * time.Second)
			reg.reapAdopted()
		}
	}()
	sem := make(chan struct{}, 64) // hard ceiling; the LIVE cap is paceCap(cfg) (= maxConcurrent on fast)

	// panel token (auth for all /api routes). Auto-generate + persist if not set.
	token := cfg.PanelToken
	if token == "" {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		token = hex.EncodeToString(b)
		cfg.PanelToken = token
		_ = saveConfig(cfg)
	}
	loadUsers(cfg) // seed admin/admin on first run (hashed in users.json)
	authed := func(r *http.Request) bool {
		t := r.Header.Get("X-Token")
		if t == "" {
			if c, e := r.Cookie("cw_session"); e == nil {
				t = c.Value
			}
		}
		if t == "" {
			t = r.URL.Query().Get("token")
		}
		return subtle.ConstantTimeCompare([]byte(t), []byte(token)) == 1
	}
	// guard wraps an API handler with token auth.
	guard := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if !authed(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			h(w, r)
		}
	}

	// launch runs a task in the background respecting pause + concurrency.
	launch := func(name string) {
		t, ok := cfg.Jobs[name]
		if !ok || reg.isRunning(name) {
			return
		}
		if reg.isPaused(name) {
			return // a PAUSED agent is never launched, in any mode (boost or scheduler)
		}
		// SURVIVAL MODE: when all Claude is maxed, only SAFE (report/test/read) jobs run on the
		// small codex model — never the code-changing engineers/deploys (a small model can damage).
		if !allowJobNow(cfg, name) {
			return
		}
		go func() {
			// Wait for a slot. Lowering maxConcurrent never kills a running agent — it just
			// stops launching new ones until running count drops below the live cap.
			for reg.isPaused(name) || isPaused(cfg) || !resourceOK(cfg) {
				time.Sleep(4 * time.Second)
			}
			for {
				cfgMu.RLock() // read cfg for fleetCap inside the wait loop
				cap := fleetCap(cfg)
				cfgMu.RUnlock()
				if reg.runningCount() < cap {
					break
				}
				time.Sleep(4 * time.Second)
			}
			cfgMu.RLock() // read cfg.Accounts for selectRunner
			acct, ok := selectRunner(cfg)
			cfgMu.RUnlock()
			if !ok {
				notifyOwnerThrottled(cfg, "all accounts at usage limit — skipping "+name)
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			_ = runTask(cfg, name, t, reg, acct)
		}()
	}

	for i := range cfg.Accounts { // restore persisted account pauses (survive restarts)
		if cfg.Accounts[i].Paused {
			setAcctPaused(cfg.Accounts[i].Label, true)
		}
	}
	go scheduler(cfg, reg, launch)
	if voiceDefaults(cfg.Voice).Enabled { // resume the Tgo voice assistant if it was left on
		startVoice(cfg)
	}
	go func() { // regular disk cleanup so the Mac never fills up again
		runCleanup(cfg)
		for {
			time.Sleep(30 * time.Minute)
			runCleanup(cfg)
		}
	}()
	startDeviceWatch(cfg)      // quick presence scan every ~8s (USB/adb/iphone/ping)
	startDeviceCheckLoop(cfg)  // deep per-device connection check every ~90s (SSH/serial/adb/iPhone)
	startSnapshotWriter(cfg)   // write logs/fleet-snapshot.json so agents reuse daemon-gathered state
	startPump(cfg)             // deterministic backlog pump: keep the in-flight cap of tickets in stage-building
	startSentryWatch(cfg) // sentry per-project counts every ~10m
	// Pre-warm + keep-warm the expensive caches in the background so the SSE never has to
	// build them inline (was making the first event after a reload take ~7s). Each getter is
	// now stale-while-revalidate, so these just keep the cached values fresh.
	go func() {
		for {
			go refreshServerHealth(cfg)
			go refreshTickets(cfg)
			go refreshPathStats(cfg)
			go refreshAccountsUsage(cfg)
			go refreshNeedsHuman(cfg)
			go usageInfoCached(keychainToken()) // warms the top-banner usage in the background
			time.Sleep(20 * time.Second)
		}
	}()

	// Boost loop: when ON, keep the pool full — relaunch every idle work agent (except
	// release/deploy, which stay manual for safety). Availability + disk + usage caps gate it.
	noBoost := map[string]bool{} // deploy/release now AUTO-run (gated by deployWorkExists) — the fleet ships green work itself
	// Owner rule: Sentry is the #0 priority — launch the Sentry pipeline + the engineers that
	// fix the bugs FIRST each cycle (with a small pace cap, only the first few run at once).
	// BUGS ALWAYS FIRST (owner rule): every bug/Sentry/quality job runs ahead of any feature work.
	priorityOrder := []string{"sentry-catch", "sentry-clean", "reliability", "code-review", "security-review", "qa", "e2e-test", "backend", "app", "firmware", "jira-poll"}
	orderedJobs := func() []string {
		seen := map[string]bool{}
		out := []string{}
		for _, n := range priorityOrder {
			if _, ok := cfg.Jobs[n]; ok {
				out = append(out, n)
				seen[n] = true
			}
		}
		for n := range cfg.Jobs {
			if !seen[n] {
				out = append(out, n)
			}
		}
		return out
	}
	// Work-now mode (Boost): keep the pool full up to the Pace cap.
	go func() {
		for {
			time.Sleep(6 * time.Second)
			cfgMu.RLock() // snapshot cfg scalar fields for this iteration
			boost := cfg.Boost
			cfgMu.RUnlock()
			if !boost || isPaused(cfg) || !resourceOK(cfg) {
				continue
			}
			cfgMu.RLock() // read cfg for fleetCap
			cap := fleetCap(cfg) // total = sum of each available account's own speed-based cap
			cfgMu.RUnlock()
			for _, name := range orderedJobs() { // Sentry pipeline first (charter #0)
				if noBoost[name] {
					continue // release/deploy stay manual
				}
				if reg.runningCount() >= cap {
					break
				}
				// IN-FLIGHT CAP: never run more than N costly Programming agents at once (owner-set).
				cfgMu.RLock() // read cfg.InFlightCap via inFlightLimit
				inflightLim := inFlightLimit(cfg)
				cfgMu.RUnlock()
				if programmingTeam[name] && progRunning(reg) >= inflightLim {
					continue
				}
				// DETERMINISTIC GATE: only launch an AI agent if a plain function says there's real work.
				if !reg.isRunning(name) && hasWork(cfg, name) {
					markFired(name)
					launch(name)
				}
			}
		}
	}()
	// Scheduler mode (Boost off): one global timer runs the whole fleet every SchedulerMins.
	go func() {
		var last time.Time
		for {
			time.Sleep(20 * time.Second)
			cfgMu.RLock() // snapshot cfg.Boost and cfg.SchedulerMins, copy cfg.Jobs map
			boost := cfg.Boost
			mins := cfg.SchedulerMins
			jobsCopy := make(map[string]Task, len(cfg.Jobs))
			for k, v := range cfg.Jobs {
				jobsCopy[k] = v
			}
			cfgMu.RUnlock()
			if boost || isPaused(cfg) || !resourceOK(cfg) {
				continue
			}
			if mins <= 0 {
				mins = 30
			}
			if !last.IsZero() && time.Since(last) < time.Duration(mins)*time.Minute {
				continue
			}
			last = time.Now()
			for name := range jobsCopy { // iterate the snapshot, not cfg.Jobs, so no lock needed here
				if !noBoost[name] && !reg.isRunning(name) && hasWork(cfg, name) {
					markFired(name)
					launch(name)
				}
			}
		}
	}()

	mux := http.NewServeMux()

	// static dashboard — HTML served no-cache so dashboard edits always show on a normal reload (no hard-refresh needed)
	web := cfg.resolve("web")
	fs := http.FileServer(http.Dir(web))
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if p := r.URL.Path; p == "/" || strings.HasSuffix(p, ".html") {
			w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		}
		fs.ServeHTTP(w, r)
	}))

	// Build the live SSE payload ONCE per second in a background goroutine into a shared, pre-marshaled
	// frame. Every page-open then sends that cached frame INSTANTLY — the (sometimes slow, subprocess-heavy)
	// build never sits on the connection's critical path, and all clients share one build. This is the fix
	// for "the dashboard loads data too late on refresh/open".
	var frameMu sync.Mutex
	var liveFrame []byte
	buildFrame := func() []byte {
		cfgMu.RLock() // snapshot all cfg scalar fields at once to avoid holding the lock during slow calls
		pace := cfg.Pace
		schedulerMins := cfg.SchedulerMins
		bannerAccount := cfg.BannerAccount
		bannerAccounts := cfg.BannerAccounts
		boost := cfg.Boost
		effort := cfg.Effort
		fanout := cfg.Fanout
		maxConcurrent := cfg.MaxConcurrent
		mmAutoApprove := cfg.MMAutoApprove
		inflightLim := inFlightLimit(cfg)
		cfgMu.RUnlock()
		payload := map[string]any{
				"agents":     reg.snapshot(),
				"needsHuman": needsHuman(cfg),
				"devices":    devices(cfg),
				"hardware":   hardwareProjects(cfg),
				"devScanning": deviceScanning(),
				"marketing":  marketingStatusCached(cfg),
				"multimedia": mmQueue(cfg),
				"deployments": deployments(cfg),
				"ownerRequests": ownerRequests(cfg),
				"servers":    serverHealth(cfg),
				"system":     sysStatus(cfg),
				"tickets":    tickets(cfg),
				"accounts":   accountsUsage(cfg),
				"pace":       pace,
				"paceTarget": int(weeklyTargetNow(cfg) + 0.5),
				"schedulerMins": schedulerMins,
				"path":       pathStats(cfg),
				"usagePct":   usagePct(cfg),
				"usage":      bannerUsage(cfg),
				"bannerAccount": bannerAccount,
				"bannerAccounts": bannerAccounts,
				"survival":   survivalActive(cfg),
				"paused":     isPaused(cfg),
				"boost":      boost,
				"effort":     effort,
				"fanout":     fanout,
				"maxConcurrent": maxConcurrent,
				"inFlightCap": inflightLim,
				"mmAutoApprove": mmAutoApprove,
				"simulator":    simulatorStatus(cfg),
				"runQueue":     loadQueue(cfg),
				"ts":         time.Now().Format("15:04:05"),
			}
			b, _ := json.Marshal(payload)
			return b
		}
		go func() { // the single background builder
			t := time.NewTicker(1 * time.Second)
			defer t.Stop()
			for {
				b := buildFrame()
				frameMu.Lock()
				liveFrame = b
				frameMu.Unlock()
				<-t.C
			}
		}()

	// live state via Server-Sent Events — send the cached frame immediately, then only on change (+ keepalive)
	mux.HandleFunc("/api/stream", guard(func(w http.ResponseWriter, r *http.Request) {
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "no flush", 500)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		tick := time.NewTicker(1 * time.Second)
		defer tick.Stop()
		var last string
		beats := 0
		for {
			frameMu.Lock()
			b := liveFrame
			frameMu.Unlock()
			if b != nil {
				if s := string(b); s != last {
					last, beats = s, 0
					fmt.Fprintf(w, "data: %s\n\n", b)
					fl.Flush()
				} else if beats++; beats >= 20 {
					beats = 0
					fmt.Fprint(w, ": keepalive\n\n") // keep proxies from dropping an idle connection
					fl.Flush()
				}
			}
			select {
			case <-r.Context().Done():
				return
			case <-tick.C:
			}
		}
	}))

	body := func(r *http.Request) map[string]string {
		m := map[string]string{}
		_ = json.NewDecoder(r.Body).Decode(&m)
		return m
	}
	ok := func(w http.ResponseWriter, v any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(v)
	}

	mux.HandleFunc("/api/run", guard(func(w http.ResponseWriter, r *http.Request) {
		launch(body(r)["name"])
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/stop", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]bool{"stopped": reg.stop(body(r)["name"])})
	}))
	mux.HandleFunc("/api/pause", guard(func(w http.ResponseWriter, r *http.Request) {
		n := body(r)["name"]
		if n == "all" {
			_ = writeFile(cfg.PauseFile, "paused")
		}
		reg.setPaused(n, true)
		persistAgentPause(cfg, []string{n}, true)
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/resume", guard(func(w http.ResponseWriter, r *http.Request) {
		n := body(r)["name"]
		if n == "all" {
			_ = removeFile(cfg.PauseFile)
		}
		reg.setPaused(n, false)
		persistAgentPause(cfg, []string{n}, false)
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/team", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r) // {agents:"a,b,c", action:"pause|resume"} — start/stop a whole team
		paused := m["action"] == "pause"
		names := strings.Split(m["agents"], ",")
		for _, n := range names {
			if n = strings.TrimSpace(n); n != "" {
				reg.setPaused(n, paused)
			}
		}
		persistAgentPause(cfg, names, paused)
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/queue/pull", guard(func(w http.ResponseWriter, r *http.Request) {
		n := atoiSafe(body(r)["n"])
		if n <= 0 {
			n = 10
		}
		added := queuePull(cfg, n)
		ok(w, map[string]any{"added": added, "queue": loadQueue(cfg)})
	}))
	// Manual trigger for the 0-token FUNCTIONS that replaced recurring agents
	// (Sentry monitoring, the test suite) — runs on demand in ADDITION to their
	// schedules. No tokens (plain Python + APIs).
	mux.HandleFunc("/api/fn", guard(func(w http.ResponseWriter, r *http.Request) {
		switch body(r)["name"] {
		case "sentry":
			msg, _ := runFleetFunctionSync("sentry")
			ok(w, map[string]any{"ok": true, "msg": msg})
		case "citests":
			runFleetFunctionBg("citests")
			ok(w, map[string]any{"ok": true, "msg": "🧪 tests started (0-token) — the Tests panel updates when they finish"})
		default:
			http.Error(w, "unknown function", http.StatusBadRequest)
		}
	}))
	mux.HandleFunc("/api/queue/start", guard(func(w http.ResponseWriter, r *http.Request) {
		started := queueStart(cfg)
		ok(w, map[string]any{"started": started, "queue": loadQueue(cfg)})
	}))
	mux.HandleFunc("/api/queue/remove", guard(func(w http.ResponseWriter, r *http.Request) {
		queueRemove(cfg, body(r)["key"])
		ok(w, map[string]any{"queue": loadQueue(cfg)})
	}))
	mux.HandleFunc("/api/queue/add", guard(func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		queueAdd(cfg, b["key"], b["summary"])
		ok(w, map[string]any{"queue": loadQueue(cfg)})
	}))
	mux.HandleFunc("/api/inflight", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.InFlightCap
		if v := atoiSafe(body(r)["n"]); v > 0 {
			cfg.InFlightCap = v
			_ = saveConfig(cfg)
		}
		cur := cfg.InFlightCap
		cfgMu.Unlock()
		ok(w, map[string]int{"inFlight": cur})
	}))
	mux.HandleFunc("/api/schedule", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		cfgMu.Lock() // write cfg.Jobs[name] schedule settings
		if t, exists := cfg.Jobs[m["name"]]; exists {
			t.Manual = m["mode"] == "manual"
			t.EveryMinutes = atoiSafe(m["everyMinutes"])
			t.AtHHMM = m["atHHMM"]
			if t.EveryMinutes > 0 || t.AtHHMM != "" {
				t.Manual = false
			}
			cfg.Jobs[m["name"]] = t
			_ = saveConfig(cfg)
		}
		cfgMu.Unlock()
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/chat", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		t, exists := cfg.Jobs[m["agent"]]
		if !exists || m["agent"] == "main" || m["agent"] == "" {
			// the MAIN agent ("Tgo"): a general full-access assistant, like Claude in VS Code.
			t = Task{Kind: "agent", PromptFile: "prompts/main_agent.md",
				AllowedTools: "Read,Edit,Write,Bash,Grep,Glob,Agent", MaxTurns: 40, Model: cfg.Model}
		}
		msg := m["message"]
		if h := m["history"]; h != "" { // give Tgo the conversation so it answers follow-ups in context
			msg = "Conversation so far:\n" + h + "\n\n---\nThe owner now says: " + msg
		}
		res, err := runChat(cfg, t, msg)
		if err != nil {
			ok(w, map[string]string{"reply": "error: " + err.Error()})
			return
		}
		ok(w, map[string]string{"reply": res})
	}))
	mux.HandleFunc("/api/chat/stream", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		msg := m["message"]
		if h := m["history"]; h != "" {
			msg = "Conversation so far:\n" + h + "\n\n---\nThe owner now says: " + msg
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		fl, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "stream unsupported", 500)
			return
		}
		runChatStream(cfg, msg, w, fl.Flush)
	}))

	mux.HandleFunc("/api/boost", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.Boost
		cfg.Boost = body(r)["on"] == "true"
		_ = saveConfig(cfg)
		v := cfg.Boost
		cfgMu.Unlock()
		ok(w, map[string]bool{"boost": v})
	}))
	mux.HandleFunc("/api/pace", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.Pace
		cfg.Pace = body(r)["pace"]
		_ = saveConfig(cfg)
		v := cfg.Pace
		cfgMu.Unlock()
		ok(w, map[string]string{"pace": v})
	}))
	mux.HandleFunc("/api/preset", guard(func(w http.ResponseWriter, r *http.Request) {
		level := body(r)["level"]
		cfgMu.Lock() // write multiple cfg fields via applyPreset
		applyPreset(cfg, level)
		n := cfg.MaxConcurrent
		cfgMu.Unlock()
		ok(w, map[string]any{"preset": level, "maxConcurrent": n})
	}))
	mux.HandleFunc("/api/concurrency", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.MaxConcurrent
		if n := atoiSafe(body(r)["n"]); n > 0 && n <= 64 {
			cfg.MaxConcurrent = n
			_ = saveConfig(cfg)
		}
		v := cfg.MaxConcurrent
		cfgMu.Unlock()
		ok(w, map[string]int{"maxConcurrent": v})
	}))
	mux.HandleFunc("/api/schedint", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.SchedulerMins
		cfg.SchedulerMins = atoiSafe(body(r)["mins"])
		_ = saveConfig(cfg)
		v := cfg.SchedulerMins
		cfgMu.Unlock()
		ok(w, map[string]int{"schedulerMins": v})
	}))
	mux.HandleFunc("/api/acctpause", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		on := m["on"] == "true"
		setAcctPaused(m["label"], on)
		cfgMu.Lock() // write cfg.Accounts[i].Paused — persist so a restart keeps the pause state
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Label == m["label"] {
				cfg.Accounts[i].Paused = on
			}
		}
		_ = saveConfig(cfg)
		cfgMu.Unlock()
		if on {
			clearCapOverride(m["label"]) // fresh manual pause: drop any prior cap-override
		} else {
			setCapOverride(m["label"]) // owner RESUMED — keep working even past the weekly cap until reset
		}
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/acctconfig", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		cfgMu.Lock() // write multiple cfg.Accounts[i] fields
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Label == m["label"] {
				if v, has := m["hardPause"]; has {
					cfg.Accounts[i].HardPause = v == "true"
				}
				if v, has := m["days"]; has {
					cfg.Accounts[i].Days = v
				}
				if v, has := m["fromHour"]; has {
					cfg.Accounts[i].FromHour = atoiSafe(v)
				}
				if v, has := m["toHour"]; has {
					cfg.Accounts[i].ToHour = atoiSafe(v)
				}
				if v, has := m["pausePct"]; has && atoiSafe(v) > 0 {
					cfg.Accounts[i].PausePct = atoiSafe(v)
				}
				if v, has := m["pace"]; has {
					cfg.Accounts[i].Pace = v
				}
				if v, has := m["effort"]; has {
					cfg.Accounts[i].Effort = v
				}
				if v, has := m["concurrency"]; has {
					cfg.Accounts[i].Concurrency = atoiSafe(v) // 0 = derive from speed
				}
				if v, has := m["fanout"]; has {
					cfg.Accounts[i].Fanout = atoiSafe(v)
				}
				if v, has := m["useSonnet"]; has {
					cfg.Accounts[i].UseSonnet = v == "true"
				}
			}
		}
		_ = saveConfig(cfg)
		cfgMu.Unlock()
		auMu.Lock()
		auAt = time.Time{}
		auMu.Unlock()
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/acctlogin", guard(func(w http.ResponseWriter, r *http.Request) {
		label := body(r)["label"]
		cmdStr := ""
		for _, a := range cfg.Accounts {
			if a.Label != label {
				continue
			}
			if a.Engine == "codex" {
				cmdStr = "codex login"
			} else if a.ConfigDir != "" {
				cmdStr = "CLAUDE_CONFIG_DIR=" + a.ConfigDir + " claude /login"
			} else {
				cmdStr = "claude /login"
			}
		}
		if cmdStr != "" {
			// open a Terminal window so the owner completes the interactive (browser) login there
			go execTerminal(cmdStr)
		}
		ok(w, map[string]string{"cmd": cmdStr, "logout": logoutCmd(cfg, label)})
	}))
	mux.HandleFunc("/api/login-start", guard(func(w http.ResponseWriter, r *http.Request) {
		url, code, err := loginStart(cfg, body(r)["label"])
		if err != nil {
			ok(w, map[string]string{"error": err.Error()})
			return
		}
		ok(w, map[string]string{"url": url, "code": code})
	}))
	mux.HandleFunc("/api/login-finish", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		res := loginFinish(cfg, m["label"], m["code"])
		if res != "" && !strings.Contains(strings.ToLower(res), "fail") {
			siteLogin(cfg, m["label"]) // a fresh site login clears any prior explicit logout
		}
		auMu.Lock()
		auAt = time.Time{}
		auMu.Unlock()
		ok(w, map[string]string{"result": res})
	}))
	mux.HandleFunc("/api/devices/refresh", guard(func(w http.ResponseWriter, r *http.Request) {
		go scanDevicesNow(cfg) // immediate rescan; the live SSE shows the result in a moment
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/devices/check", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, checkDevice(cfg, body(r)["name"])) // live "Try" connectivity probe for one device
	}))
	mux.HandleFunc("/api/devices/save", guard(func(w http.ResponseWriter, r *http.Request) {
		saveDevice(cfg, body(r)) // add or edit a device (name/type/reach/user/pass/note/method)
		go scanDevicesNow(cfg)
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/devices/delete", guard(func(w http.ResponseWriter, r *http.Request) {
		deleteDevice(cfg, body(r)["name"])
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/owner-request/done", guard(func(w http.ResponseWriter, r *http.Request) {
		resolveOwnerRequest(cfg, body(r)["id"])
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/owner-request/add", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r) // agents can POST here too (or write the json file directly)
		urls := []string{}
		if m["url"] != "" {
			urls = append(urls, m["url"])
		}
		addOwnerRequest(cfg, OwnerRequest{ID: m["id"], Title: m["title"], Guide: m["guide"], URLs: urls, Who: m["who"]})
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/tickets/all", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"all": allTicketsNow(cfg)}) // full backlog on demand (kept out of the SSE)
	}))
	mux.HandleFunc("/api/marketing/control", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]string{"result": marketingControl(cfg, body(r)["action"])}) // start/stop/restart
	}))
	mux.HandleFunc("/media", guard(func(w http.ResponseWriter, r *http.Request) {
		// serve a media file (screenshot / brand asset / generated) — restricted to allowed dirs only
		abs, _ := filepath.Abs(r.URL.Query().Get("p"))
		allowed := []string{galleryDir(), "/Volumes/Extreme SSD/ComputerFilesFromClaudeAI",
			filepath.Join(cfg.resolve(cfg.LogDir), "multimedia"),
			filepath.Join(expandHome(cfg.resolve(cfg.ProjectDir)), "Multimedia")}
		okp := false
		for _, a := range allowed {
			if a != "" && strings.HasPrefix(abs, a) {
				okp = true
			}
		}
		if !okp {
			http.Error(w, "forbidden", 403)
			return
		}
		http.ServeFile(w, r, abs)
	}))
	mux.HandleFunc("/api/mm/action", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		mmAction(cfg, m["id"], m["action"]) // approve | cancel | publish (id="all" for bulk)
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/mm/add", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r) // the multimedia agent posts a generated piece here
		it := MMItem{Type: m["type"], Title: m["title"], Text: m["text"], Image: m["image"], Video: m["video"], Channel: m["channel"]}
		if cfg.MMAutoApprove {
			it.Status = "approved"
		}
		mmAdd(cfg, it)
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/mm/autoapprove", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.MMAutoApprove
		cfg.MMAutoApprove = body(r)["on"] == "true"
		_ = saveConfig(cfg)
		v := cfg.MMAutoApprove
		cfgMu.Unlock()
		ok(w, map[string]bool{"on": v})
	}))
	mux.HandleFunc("/api/devices/reveal", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]string{"pass": devicePassword(cfg, body(r)["name"])}) // on-demand password reveal
	}))
	// AI-agent / programmatic device control: flip a device's account or online|offline mode without the form
	mux.HandleFunc("/api/devices/setattr", guard(func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		attrs := map[string]string{}
		if v, ok := b["account"]; ok {
			attrs["account"] = v
		}
		if v, ok := b["mode"]; ok {
			attrs["mode"] = v
		}
		ok(w, map[string]bool{"ok": setDeviceAttr(cfg, b["name"], attrs)})
	}))
	mux.HandleFunc("/api/testaccount", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"accounts": testAccounts(cfg, false)}) // list, passwords masked
	}))
	mux.HandleFunc("/api/testaccount/reveal", guard(func(w http.ResponseWriter, r *http.Request) {
		for _, ta := range testAccounts(cfg, true) {
			if ta.Slot == body(r)["slot"] {
				ok(w, map[string]string{"password": ta.Password})
				return
			}
		}
		ok(w, map[string]string{"password": ""})
	}))
	mux.HandleFunc("/api/testaccount/save", guard(func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		saveTestAccount(cfg, b["slot"], b["email"], b["password"], b["backend"])
		ok(w, map[string]any{"accounts": testAccounts(cfg, false)})
	}))
	// --- Integrations ("connect your own accounts": Jira/Confluence/Sentry/GitHub/Telegram/...) ---
	mux.HandleFunc("/api/integrations", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.RLock()
		mon := append([]MonitorTarget(nil), cfg.Monitor...)
		cfgMu.RUnlock()
		ok(w, map[string]any{"integrations": integrationStatus(cfg), "monitors": mon})
	}))
	mux.HandleFunc("/api/integrations/save", guard(func(w http.ResponseWriter, r *http.Request) {
		saveIntegration(cfg, body(r)) // flat map of vaultKey -> value (blank secret = keep existing)
		ok(w, map[string]any{"integrations": integrationStatus(cfg)})
	}))
	mux.HandleFunc("/api/integrations/test", guard(func(w http.ResponseWriter, r *http.Request) {
		passed, msg := integrationTest(cfg, body(r)["id"])
		ok(w, map[string]any{"ok": passed, "msg": msg})
	}))
	mux.HandleFunc("/api/integrations/reveal", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]string{"value": secret(cfg, body(r)["key"])})
	}))
	mux.HandleFunc("/api/monitors/save", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.Monitor from "Name|URL" lines (URL-only line → name = URL)
		cfg.Monitor = parseMonitors(body(r)["urls"])
		_ = saveConfig(cfg)
		out := append([]MonitorTarget(nil), cfg.Monitor...)
		cfgMu.Unlock()
		ok(w, map[string]any{"monitors": out})
	}))
	mux.HandleFunc("/api/simulator/start", guard(func(w http.ResponseWriter, r *http.Request) {
		err := simulatorStart(cfg)
		es := ""
		if err != nil {
			es = err.Error()
		}
		ok(w, map[string]any{"running": simulatorRunning(), "err": es})
	}))
	mux.HandleFunc("/api/simulator/stop", guard(func(w http.ResponseWriter, r *http.Request) {
		simulatorStop(cfg)
		ok(w, map[string]any{"running": false})
	}))
	mux.HandleFunc("/api/simulator/apply", guard(func(w http.ResponseWriter, r *http.Request) {
		err := simulatorApply(cfg) // restart so config edits register on staging (one login, not per-edit)
		es := ""
		if err != nil {
			es = err.Error()
		}
		ok(w, map[string]any{"running": simulatorRunning(), "err": es})
	}))
	mux.HandleFunc("/api/simulator/device", guard(func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		var err error
		switch b["action"] {
		case "add":
			err = simAddDevice(cfg, b["group"], b["type"], b["name"], atoiSafe(b["sockets"]))
		case "remove":
			err = simRemoveDevice(cfg, b["group"], b["name"])
		default:
			err = nil
		}
		es := ""
		if err != nil {
			es = err.Error()
		}
		ok(w, map[string]any{"err": es, "simulator": simulatorStatus(cfg)})
	}))
	mux.HandleFunc("/api/simulator/group", guard(func(w http.ResponseWriter, r *http.Request) {
		b := body(r)
		var err error
		switch b["action"] {
		case "add":
			err = simAddGroup(cfg, b["name"])
		case "remove":
			err = simRemoveGroup(cfg, b["name"])
		}
		es := ""
		if err != nil {
			es = err.Error()
		}
		ok(w, map[string]any{"err": es, "simulator": simulatorStatus(cfg)})
	}))
	mux.HandleFunc("/api/simulator/transport", guard(func(w http.ResponseWriter, r *http.Request) {
		err := simSetTransport(cfg, body(r)["mode"])
		es := ""
		if err != nil {
			es = err.Error()
		}
		ok(w, map[string]any{"err": es, "simulator": simulatorStatus(cfg)})
	}))
	mux.HandleFunc("/api/acctlogout", guard(func(w http.ResponseWriter, r *http.Request) {
		label := body(r)["label"]
		cfgMu.Lock() // write cfg.Accounts[i] creds via clearCreds
		for i := range cfg.Accounts {
			if cfg.Accounts[i].Label == label {
				clearCreds(&cfg.Accounts[i]) // log out in place; KEEP the account listed (logged out)
			}
		}
		cfgMu.Unlock()
		siteLogout(cfg, label) // EXPLICIT site logout — overrides the sticky "stay logged in"
		auMu.Lock()
		auAt = time.Time{}
		auMu.Unlock()
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/acctadd", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		if m["label"] != "" {
			eng := m["engine"]
			if eng == "" {
				eng = "claude"
			}
			dir := m["configDir"]
			// PRIVACY/ISOLATION: every added account gets its OWN profile dir (CLAUDE_CONFIG_DIR
			// for Claude, CODEX_HOME for Codex), so its creds + sessions are separate and a new
			// account starts LOGGED OUT (never inherits the Mac's default login).
			if dir == "" {
				dir = "~/.cw-accounts/" + sanitizeLabel(m["label"])
				if eng == "codex" {
					dir += "-codex"
				}
			}
			cfgMu.Lock() // write cfg.Accounts (append new account)
			cfg.Accounts = append(cfg.Accounts, Account{Label: m["label"], Engine: eng, ConfigDir: dir, Cmd: m["cmd"], PausePct: atoiSafe(m["pausePct"]), HardPause: m["hardPause"] == "true"})
			_ = saveConfig(cfg)
			cfgMu.Unlock()
			auMu.Lock()
			auAt = time.Time{}
			auMu.Unlock()
		}
		ok(w, map[string]bool{"ok": true})
	}))
	// Browser /speak page: one voice turn (audio → STT → Claude → TTS). Guarded.
	mux.HandleFunc("/api/voice/turn", guard(func(w http.ResponseWriter, r *http.Request) {
		voiceTurn(cfg, w, r)
	}))
	// Tgo voice assistant: read settings (unguarded — no secrets, the local voice process reads it)
	mux.HandleFunc("/api/voicecfg", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			if !authed(r) {
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			m := body(r)
			v := voiceDefaults(cfg.Voice)
			if s, ok := m["enabled"]; ok {
				v.Enabled = s == "true"
			}
			if s, ok := m["language"]; ok && s != "" {
				v.Language = s
			}
			if s, ok := m["voiceGender"]; ok && s != "" {
				v.VoiceGender = s
			}
			if s, ok := m["wakeWords"]; ok {
				v.WakeWords = splitList(s)
			}
			if s, ok := m["agentNames"]; ok {
				v.AgentNames = splitList(s)
			}
			if s, ok := m["sttEngine"]; ok && s != "" {
				v.STTEngine = s
			}
			if s, ok := m["ttsEngine"]; ok && s != "" {
				v.TTSEngine = s
			}
			if s, ok := m["activeWindows"]; ok { // "17:30-01:00, 09:00-12:00"
				wins := []VoiceWindow{}
				for _, part := range splitList(s) {
					if a, b, found := strings.Cut(part, "-"); found {
						wins = append(wins, VoiceWindow{From: strings.TrimSpace(a), To: strings.TrimSpace(b)})
					}
				}
				if len(wins) > 0 {
					v.ActiveWindows = wins
				}
			}
			cfgMu.Lock() // write cfg.Voice
			cfg.Voice = v
			_ = saveConfig(cfg)
			cfgMu.Unlock()
			if v.Enabled {
				startVoice(cfg)
			} else {
				stopVoice(cfg)
			}
		}
		cfgMu.RLock() // read cfg.Voice for response
		v := voiceDefaults(cfg.Voice)
		cfgMu.RUnlock()
		ok(w, map[string]any{"enabled": v.Enabled, "language": v.Language, "voiceGender": v.VoiceGender,
			"wakeWords": v.WakeWords, "agentNames": v.AgentNames, "sttEngine": v.STTEngine,
			"ttsEngine": v.TTSEngine, "activeWindows": v.ActiveWindows, "running": voiceRunning(),
			"azureReady": secret(cfg, "AZURE_SPEECH_KEY") != ""})
	})
	mux.HandleFunc("/api/banneracct", guard(func(w http.ResponseWriter, r *http.Request) {
		label := body(r)["label"]
		cfgMu.Lock() // write cfg.BannerAccounts
		if label == "" { // clear all
			cfg.BannerAccounts = nil
		} else { // TOGGLE this account in/out of the banner set (multi-select)
			kept := []string{}
			found := false
			for _, l := range cfg.BannerAccounts {
				if l == label {
					found = true
					continue
				}
				kept = append(kept, l)
			}
			if !found {
				kept = append(kept, label)
			}
			cfg.BannerAccounts = kept
		}
		_ = saveConfig(cfg)
		result := cfg.BannerAccounts
		cfgMu.Unlock()
		ok(w, map[string]any{"bannerAccounts": result})
	}))
	mux.HandleFunc("/api/acctdel", guard(func(w http.ResponseWriter, r *http.Request) {
		label := body(r)["label"]
		cfgMu.Lock() // write cfg.Accounts (remove account by label)
		kept := make([]Account, 0, len(cfg.Accounts))
		for _, a := range cfg.Accounts {
			if a.Label != label {
				kept = append(kept, a)
			}
		}
		cfg.Accounts = kept
		_ = saveConfig(cfg)
		cfgMu.Unlock()
		auMu.Lock()
		auAt = time.Time{}
		auMu.Unlock()
		ok(w, map[string]bool{"ok": true})
	}))
	// --- auth (username/password; users hashed in users.json; first run = admin/admin) ---
	mux.HandleFunc("/api/authmode", func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]bool{"login": true})
	})
	mux.HandleFunc("/api/login", func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		if checkUser(cfg, m["user"], m["pass"]) {
			http.SetCookie(w, &http.Cookie{Name: "cw_session", Value: token, Path: "/", HttpOnly: true, MaxAge: 86400 * 30, SameSite: http.SameSiteLaxMode})
			ok(w, map[string]bool{"ok": true})
			return
		}
		http.Error(w, "bad credentials", http.StatusUnauthorized)
	})
	mux.HandleFunc("/api/logout", func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, &http.Cookie{Name: "cw_session", Value: "", Path: "/", MaxAge: -1})
		ok(w, map[string]bool{"ok": true})
	})
	mux.HandleFunc("/api/users/list", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"users": listUsers(cfg)})
	}))
	mux.HandleFunc("/api/users/add", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		if m["user"] != "" && m["pass"] != "" {
			addOrSetUser(cfg, m["user"], m["pass"])
		}
		ok(w, map[string]bool{"ok": true})
	}))
	mux.HandleFunc("/api/users/changepass", guard(func(w http.ResponseWriter, r *http.Request) {
		m := body(r)
		ok(w, map[string]bool{"ok": changePass(cfg, m["user"], m["old"], m["new"])})
	}))
	mux.HandleFunc("/api/effort", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.Effort
		cfg.Effort = body(r)["level"]
		_ = saveConfig(cfg)
		v := cfg.Effort
		cfgMu.Unlock()
		ok(w, map[string]string{"effort": v})
	}))
	mux.HandleFunc("/api/fanout", guard(func(w http.ResponseWriter, r *http.Request) {
		cfgMu.Lock() // write cfg.Fanout
		cfg.Fanout = atoiSafe(body(r)["n"])
		_ = saveConfig(cfg)
		v := cfg.Fanout
		cfgMu.Unlock()
		ok(w, map[string]int{"fanout": v})
	}))
	mux.HandleFunc("/api/stopall", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]int{"stopped": reg.stopAll()})
	}))
	mux.HandleFunc("/api/activate", guard(func(w http.ResponseWriter, r *http.Request) {
		key := body(r)["key"]
		done := activateTicket(cfg, key)
		if done {
			launch("jira-poll") // nudge the fleet to pick it up
		}
		ok(w, map[string]bool{"ok": done})
	}))
	mux.HandleFunc("/api/ticket", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, ticketDetail(cfg, r.URL.Query().Get("key")))
	}))
	mux.HandleFunc("/api/history", guard(func(w http.ResponseWriter, r *http.Request) {
		ok(w, map[string]any{"history": recentHistory(cfg, 24)})
	}))
	mux.HandleFunc("/api/agent", guard(func(w http.ResponseWriter, r *http.Request) {
		name := r.URL.Query().Get("name")
		reg.mu.Lock()
		ri := reg.info[name]
		reg.mu.Unlock()
		if ri == nil {
			http.Error(w, "unknown", 404)
			return
		}
		cp := *ri
		if p, okk := readStatus(cfg, name); okk {
			cp.Progress = p
		}
		ok(w, map[string]any{"info": cp, "log": logTail(cfg, name, 60)})
	}))

	fmt.Printf("control panel on http://%s\n", addr)
	fmt.Printf("PANEL TOKEN: %s   (enter this in the page; it's saved in config.json)\n", token)
	srv := &http.Server{Addr: addr, Handler: logReq(mux)}
	if err := srv.ListenAndServe(); err != nil {
		fmt.Println("server:", err)
	}
}

// systemMax is "as much as the machine can afford" for the Fast preset.
func systemMax() int {
	n := runtime.NumCPU() * 3
	if n > 48 {
		n = 48
	}
	if n < 8 {
		n = 8
	}
	return n
}

// applyPreset sets the 5-level speed profile. Lowering never kills running agents (graceful).
func applyPreset(cfg *Config, level string) {
	switch level {
	case "slow":
		cfg.Boost, cfg.Pace, cfg.MaxConcurrent, cfg.Fanout, cfg.SchedulerMins = false, "slow", 3, 0, 60
	case "light":
		cfg.Boost, cfg.Pace, cfg.MaxConcurrent, cfg.Fanout, cfg.SchedulerMins = false, "slow", 5, 0, 30
	case "medium":
		cfg.Boost, cfg.Pace, cfg.MaxConcurrent, cfg.Fanout = true, "medium", 13, 4
	case "high":
		cfg.Boost, cfg.Pace, cfg.MaxConcurrent, cfg.Fanout = true, "fast", 26, 8
	case "fast":
		cfg.Boost, cfg.Pace, cfg.MaxConcurrent, cfg.Fanout = true, "fast", systemMax(), 10
	}
	_ = saveConfig(cfg)
}

// weeklyEndTarget = the % of the weekly limit each preset should reach by the END of the week window.
//   slow 30 · light 50 · medium 80 · high 120 (overshoot before the end) · fast 200 (hit 100% at mid-week)
func weeklyEndTarget(pace string) float64 {
	// Targets raised ~30% (owner: "most of the time it's asleep — use the weekly limit").
	switch pace {
	case "slow":
		return 39
	case "light":
		return 65
	case "high":
		return 156
	case "fast":
		return 260
	default: // medium
		return 104
	}
}

// weeklyTargetNow is where we SHOULD be right now on the linear ramp to the end-of-week target.
// Returns -1 if weekly usage/reset is unknown (then no pacing gate is applied).
func weeklyTargetNow(cfg *Config) float64 {
	ui := usageInfoCached(keychainToken())
	if !ui.OK || ui.WeekResetMin < 0 {
		return -1
	}
	const weekMin = 7 * 24 * 60.0
	elapsed := 1 - float64(ui.WeekResetMin)/weekMin // fraction of the 7-day window already elapsed
	if elapsed < 0 {
		elapsed = 0
	}
	if elapsed > 1 {
		elapsed = 1
	}
	return weeklyEndTarget(cfg.Pace) * elapsed
}

// paceAllowed: a bang-bang controller around the weekly target — run while BELOW the target-for-now,
// hold while AT/ABOVE it. Over the week this tracks the desired burn line (e.g. medium ≈ 80% by week end).
func paceAllowed(cfg *Config) bool {
	target := weeklyTargetNow(cfg)
	if target < 0 {
		return true // unknown usage → don't gate on pacing (fall back to concurrency cap)
	}
	ui := usageInfoCached(keychainToken())
	return float64(ui.Week) < target
}

// paceCap maps the Pace toggle to how many workers may run at once.
func paceCap(cfg *Config) int {
	switch cfg.Pace {
	case "slow":
		return 3
	case "medium":
		if n := cfg.MaxConcurrent * 3 / 4; n > 1 { // raised from /2 → 3/4 (more parallelism)
			return n
		}
		return 6
	default: // fast / unset → everything at once
		return cfg.MaxConcurrent
	}
}

var schedMu sync.Mutex
var cfgMu sync.RWMutex // guards all reads/writes of the shared *Config pointer fields

func scheduler(cfg *Config, reg *Registry, launch func(string)) {
	lastDaily := map[string]string{}
	nextDue := map[string]time.Time{}
	now := time.Now()
	cfgMu.RLock() // read cfg.Jobs for initial nextDue population
	for n, t := range cfg.Jobs {
		if t.EveryMinutes > 0 {
			nextDue[n] = now
		}
	}
	cfgMu.RUnlock()
	tick := time.NewTicker(30 * time.Second)
	defer tick.Stop()
	for {
		schedMu.Lock()
		cfgMu.RLock() // snapshot cfg.Jobs for this tick
		jobsCopy := make(map[string]Task, len(cfg.Jobs))
		for k, v := range cfg.Jobs {
			jobsCopy[k] = v
		}
		cfgMu.RUnlock()
		now := time.Now()
		for name, t := range jobsCopy { // iterate the snapshot without holding cfgMu
			if t.Manual {
				continue
			}
			if t.EveryMinutes > 0 {
				if due, ok := nextDue[name]; ok && !now.Before(due) {
					nextDue[name] = now.Add(time.Duration(t.EveryMinutes) * time.Minute)
					launch(name)
				}
			}
			if t.AtHHMM != "" {
				today := now.Format("2006-01-02")
				if now.Format("15:04") == t.AtHHMM && lastDaily[name] != today {
					lastDaily[name] = today
					launch(name)
				}
			}
		}
		schedMu.Unlock()
		<-tick.C
	}
}

func logReq(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r)
	})
}

func writeFile(path, s string) error  { return osWrite(path, s) }
func removeFile(path string) error    { return osRemove(path) }
func saveConfig(cfg *Config) error {
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return osWriteBytes(filepath.Join(cfg.root, "config.json"), b)
}

package main

import (
	"time"
)

// ── BACKLOG PUMP (deterministic, no AI) ──────────────────────────────────────────────────────────
// "Which tickets are actively being built" is a plain decision, so CODE makes it — not an AI run.
// The pump keeps a bounded number of tickets in the `stage-building` lane (the in-flight cap), pulling
// from the To-Do backlog by priority (bugs first). Engineering agents only work tickets in that lane,
// so the huge backlog drains at a controlled, owner-set rate instead of every agent grabbing anything.
// Agents still do the actual work + hand off (they set ready-for-review when done — a reasoning call);
// the pump just decides what enters the lane.

const pumpAreaLabels = `(app,frontend,ux,ui,edge-ai,redesign,backend,api,server,payment,auth,firmware,hardware,esp32,pcb,device,gdpr)`

func startPump(cfg *Config) {
	go func() {
		time.Sleep(30 * time.Second) // let caches warm
		for {
			runPump(cfg)
			time.Sleep(2 * time.Minute)
		}
	}()
}

func runPump(cfg *Config) {
	if isPaused(cfg) {
		return
	}
	cap := inFlightLimit(cfg)
	// in-flight = tickets in the building lane that aren't finished yet
	inflight := jqlCount(cfg, `project=SCRUM AND labels="stage-building" AND statusCategory!=Done`)
	if inflight >= cap {
		return
	}
	need := cap - inflight
	site := secret(cfg, "ATLASSIAN_SITE")
	// top routable To-Do tickets, BUGS FIRST (priority DESC), oldest next, not already in a stage lane.
	jql := `project=SCRUM AND statusCategory="To Do" AND labels in ` + pumpAreaLabels +
		` AND labels not in ("stage-building","stage-review","stage-test","stage-deploy") ` +
		`ORDER BY priority DESC, created ASC`
	items := jiraSearchPaged(cfg, site, jql, need)
	promoted := 0
	for i, it := range items {
		if i >= need {
			break
		}
		ok := jiraPUT(cfg, "/rest/api/3/issue/"+it.Key, map[string]any{
			"update": map[string]any{"labels": []map[string]string{{"add": "stage-building"}}},
		})
		if ok {
			promoted++
		}
	}
	if promoted > 0 {
		// bust the cached counts so the gate + pump see the new lane immediately
		jqlcMu.Lock()
		jqlcAt = map[string]time.Time{}
		jqlcMu.Unlock()
		logf(cfg, "pump", "promoted %d ticket(s) into stage-building (in-flight %d→%d of cap %d)", promoted, inflight, inflight+promoted, cap)
	}
}

package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Run-queue — a manual staging area under Triage. The owner PULLS Jira To-Do tickets into the queue
// (by trigger), stacks up however many they want, then STARTS them: each queued ticket gets the pump's
// `stage-building` label so the Programming team picks it up. Pure code, no AI. Stored in logs/run-queue.json.

type QItem struct {
	Key     string `json:"key"`
	Summary string `json:"summary"`
	Status  string `json:"status"`
}

var queueMu sync.Mutex

func queuePath(cfg *Config) string { return filepath.Join(cfg.resolve(cfg.LogDir), "run-queue.json") }

func loadQueue(cfg *Config) []QItem {
	queueMu.Lock()
	defer queueMu.Unlock()
	b, err := os.ReadFile(queuePath(cfg))
	if err != nil {
		return []QItem{}
	}
	var q []QItem
	_ = json.Unmarshal(b, &q)
	if q == nil {
		q = []QItem{}
	}
	return q
}

func saveQueue(cfg *Config, q []QItem) {
	queueMu.Lock()
	defer queueMu.Unlock()
	_ = os.MkdirAll(filepath.Dir(queuePath(cfg)), 0o755)
	if b, err := json.MarshalIndent(q, "", "  "); err == nil {
		_ = osWriteBytes(queuePath(cfg), b)
	}
}

// queuePull fetches the next `n` To-Do tickets (oldest/highest-priority first, bugs first, not already in a
// stage lane and not already queued) and appends them to the run-queue. Returns how many it added.
func queuePull(cfg *Config, n int) int {
	if n <= 0 {
		n = 10
	}
	q := loadQueue(cfg)
	have := map[string]bool{}
	for _, it := range q {
		have[it.Key] = true
	}
	site := secret(cfg, "ATLASSIAN_SITE")
	jql := `project=SCRUM AND statusCategory="To Do" AND labels in ` + pumpAreaLabels +
		` AND labels not in ("stage-building","stage-review","stage-test","stage-deploy") ` +
		`ORDER BY priority DESC, created ASC`
	items := jiraSearchPaged(cfg, site, jql, n+len(have)+5)
	added := 0
	for _, it := range items {
		if added >= n {
			break
		}
		if have[it.Key] {
			continue
		}
		q = append(q, QItem{Key: it.Key, Summary: it.Summary, Status: it.Status})
		have[it.Key] = true
		added++
	}
	if added > 0 {
		saveQueue(cfg, q)
	}
	return added
}

// queueAdd puts one specific ticket on the queue (used by the chat/owner "run SCRUM-x").
func queueAdd(cfg *Config, key, summary string) {
	if key == "" {
		return
	}
	q := loadQueue(cfg)
	for _, it := range q {
		if it.Key == key {
			return
		}
	}
	q = append(q, QItem{Key: key, Summary: summary})
	saveQueue(cfg, q)
}

func queueRemove(cfg *Config, key string) {
	q := loadQueue(cfg)
	out := make([]QItem, 0, len(q))
	for _, it := range q {
		if it.Key != key {
			out = append(out, it)
		}
	}
	saveQueue(cfg, out)
}

// queueStart promotes every queued ticket into the pump's `stage-building` lane (Programming picks them up),
// then clears the queue. Returns how many it started.
func queueStart(cfg *Config) int {
	q := loadQueue(cfg)
	started := 0
	for _, it := range q {
		if jiraPUT(cfg, "/rest/api/3/issue/"+it.Key, map[string]any{
			"update": map[string]any{"labels": []map[string]string{{"add": "stage-building"}}},
		}) {
			started++
		}
	}
	if started > 0 {
		jqlcMu.Lock() // bust the gate's cached counts so Programming sees the new lane immediately
		jqlcAt = map[string]time.Time{}
		jqlcMu.Unlock()
		logf(cfg, "queue", "started %d queued ticket(s) → stage-building (Programming will work them)", started)
	}
	saveQueue(cfg, []QItem{}) // clear the queue (they're now in the pipeline)
	return started
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Multimedia content queue — the ORCHESTRATION is pure code (no AI): the approval state machine, the
// "don't generate a new one while one is pending" gate, the publish flow. Only the actual content
// (text + which gallery image) is produced by the (cheap, gated) multimedia agent, which appends an
// item here as `pending`. The owner approves/cancels per item (or auto-approves); publishing is a
// deterministic step. Stored in logs/multimedia/queue.json.

type MMItem struct {
	ID        string `json:"id"`
	Status    string `json:"status"` // pending | approved | published | cancelled
	Type      string `json:"type"`   // post | story | caption | seo
	Title     string `json:"title"`
	Text      string `json:"text"`
	Image     string `json:"image"`   // path into the screenshot gallery / brand-media (reused, not regenerated)
	Video     string `json:"video"`   // optional
	Channel   string `json:"channel"` // instagram | x | linkedin | blog …
	CreatedAt string `json:"createdAt"`
	DecidedAt string `json:"decidedAt"`
}

var mmMu sync.Mutex

func mmPath(cfg *Config) string { return filepath.Join(cfg.resolve(cfg.LogDir), "multimedia", "queue.json") }

func loadMM(cfg *Config) []MMItem {
	mmMu.Lock()
	defer mmMu.Unlock()
	b, err := os.ReadFile(mmPath(cfg))
	if err != nil {
		return nil
	}
	var q []MMItem
	_ = json.Unmarshal(b, &q)
	return q
}

func saveMM(cfg *Config, q []MMItem) {
	_ = os.MkdirAll(filepath.Dir(mmPath(cfg)), 0o755)
	if b, err := json.MarshalIndent(q, "", "  "); err == nil {
		_ = osWriteBytes(mmPath(cfg), b)
	}
}

// mmQueue returns the queue newest-first (for the dashboard tab).
func mmQueue(cfg *Config) []MMItem {
	q := loadMM(cfg)
	out := make([]MMItem, 0, len(q))
	for i := len(q) - 1; i >= 0; i-- {
		out = append(out, q[i])
	}
	return out
}

// mmHasPending reports whether there's an item awaiting approval OR approved-not-yet-published. While
// true, the agent generates NO new content — a plain function gate, no tokens wasted.
func mmHasPending(cfg *Config) bool {
	for _, it := range loadMM(cfg) {
		if it.Status == "pending" || it.Status == "approved" {
			return true
		}
	}
	return false
}

// mmAction = the deterministic state machine: approve / cancel / publish one item (or "all").
func mmAction(cfg *Config, id, action string) {
	mmMu.Lock()
	defer mmMu.Unlock()
	b, err := os.ReadFile(mmPath(cfg))
	if err != nil {
		return
	}
	var q []MMItem
	if json.Unmarshal(b, &q) != nil {
		return
	}
	now := time.Now().Format("2006-01-02 15:04")
	for i := range q {
		if id != "all" && q[i].ID != id {
			continue
		}
		switch action {
		case "approve":
			if q[i].Status == "pending" {
				q[i].Status, q[i].DecidedAt = "approved", now
			}
		case "cancel":
			if q[i].Status == "pending" || q[i].Status == "approved" {
				q[i].Status, q[i].DecidedAt = "cancelled", now
			}
		case "publish":
			if q[i].Status == "approved" || q[i].Status == "pending" {
				q[i].Status, q[i].DecidedAt = "published", now // deterministic mark; real push = marketing engine
			}
		}
	}
	nb, _ := json.MarshalIndent(q, "", "  ")
	_ = osWriteBytes(mmPath(cfg), nb)
}

// mmAdd appends a generated item as pending (used by the agent via /api/mm/add).
func mmAdd(cfg *Config, it MMItem) {
	mmMu.Lock()
	defer mmMu.Unlock()
	var q []MMItem
	if b, err := os.ReadFile(mmPath(cfg)); err == nil {
		_ = json.Unmarshal(b, &q)
	}
	if it.ID == "" {
		it.ID = fmt.Sprintf("mm-%d", len(q)+1)
	}
	if it.Status == "" {
		it.Status = "pending"
	}
	if it.CreatedAt == "" {
		it.CreatedAt = time.Now().Format("2006-01-02 15:04")
	}
	q = append(q, it)
	nb, _ := json.MarshalIndent(q, "", "  ")
	_ = os.MkdirAll(filepath.Dir(mmPath(cfg)), 0o755)
	_ = osWriteBytes(mmPath(cfg), nb)
}

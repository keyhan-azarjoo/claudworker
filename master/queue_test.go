package main

import (
	"path/filepath"
	"testing"
)

func testCfg(t *testing.T) *Config {
	t.Helper()
	return &Config{root: t.TempDir(), LogDir: "logs"}
}

func TestQueuePath(t *testing.T) {
	cfg := &Config{root: "/srv/fleet", LogDir: "logs"}
	want := filepath.Join("/srv/fleet", "logs", "run-queue.json")
	if got := queuePath(cfg); got != want {
		t.Errorf("queuePath = %q, want %q", got, want)
	}
}

func TestLoadQueueMissingReturnsEmpty(t *testing.T) {
	cfg := testCfg(t)
	q := loadQueue(cfg)
	if q == nil || len(q) != 0 {
		t.Errorf("loadQueue on missing file = %v, want empty non-nil", q)
	}
}

func TestSaveLoadQueueRoundTrip(t *testing.T) {
	cfg := testCfg(t)
	in := []QItem{{Key: "PROJ-123", Summary: "one", Status: "To Do"}, {Key: "PROJ-123", Summary: "two"}}
	saveQueue(cfg, in)
	out := loadQueue(cfg)
	if len(out) != 2 || out[0].Key != "PROJ-123" || out[1].Summary != "two" {
		t.Errorf("round-trip = %+v", out)
	}
}

func TestQueueAdd(t *testing.T) {
	cfg := testCfg(t)
	queueAdd(cfg, "PROJ-123", "ten")
	queueAdd(cfg, "PROJ-123", "ten-dup") // duplicate key ignored
	queueAdd(cfg, "", "empty")           // empty key ignored
	queueAdd(cfg, "PROJ-123", "eleven")
	q := loadQueue(cfg)
	if len(q) != 2 {
		t.Fatalf("queueAdd len = %d, want 2 (%+v)", len(q), q)
	}
	if q[0].Key != "PROJ-123" || q[1].Key != "PROJ-123" {
		t.Errorf("queueAdd order = %+v", q)
	}
}

func TestQueueRemove(t *testing.T) {
	cfg := testCfg(t)
	saveQueue(cfg, []QItem{{Key: "A"}, {Key: "B"}, {Key: "C"}})
	queueRemove(cfg, "B")
	queueRemove(cfg, "Z") // not present — no-op
	q := loadQueue(cfg)
	if len(q) != 2 || q[0].Key != "A" || q[1].Key != "C" {
		t.Errorf("queueRemove = %+v", q)
	}
}

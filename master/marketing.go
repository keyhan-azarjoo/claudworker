package main

import (
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Marketing service control: the daily content/SEO + publishing agent runs as a Docker container on
// the VPS (marketing-production-marketing-agent-1). The dashboard can start/stop/restart it over SSH.
// The container IS both the server and the publishing agent, so one control covers both.
const (
	marketingHost      = "root@your-server.example.com"
	marketingContainer = "marketing-production-marketing-agent-1"
)

func marketingSSH(args string) string {
	return runc("ssh -o BatchMode=yes -o ConnectTimeout=8 -o StrictHostKeyChecking=no "+marketingHost+" "+args, 20*time.Second)
}

type MarketingStatus struct {
	Running   bool   `json:"running"`
	State     string `json:"state"`   // docker status line
	Container string `json:"container"`
	Health    bool   `json:"health"`  // marketing.example.com reachable
	OK        bool   `json:"ok"`      // could reach the VPS to read status
}

var (
	mkCache MarketingStatus
	mkAt    time.Time
	mkMu    sync.Mutex
	mkBusy  int32
)

// marketingStatusCached returns the cached marketing status INSTANTLY (SSH is slow → 20s cache).
func marketingStatusCached(cfg *Config) MarketingStatus {
	mkMu.Lock()
	c, age := mkCache, time.Since(mkAt)
	mkMu.Unlock()
	if !mkAt.IsZero() && age < 20*time.Second {
		return c
	}
	go refreshMarketing(cfg)
	return c
}

func refreshMarketing(cfg *Config) {
	if !atomic.CompareAndSwapInt32(&mkBusy, 0, 1) {
		return // never pile up SSH calls (in-flight guard)
	}
	defer atomic.StoreInt32(&mkBusy, 0)
	out := marketingSSH(`"docker ps -a --filter name=` + marketingContainer + ` --format '{{.Names}}|{{.State}}|{{.Status}}'"`)
	st := MarketingStatus{Container: marketingContainer}
	for _, ln := range strings.Split(out, "\n") {
		ln = strings.TrimSpace(ln)
		if !strings.Contains(ln, marketingContainer) {
			continue
		}
		st.OK = true
		parts := strings.SplitN(ln, "|", 3)
		if len(parts) >= 2 {
			st.State = parts[1]
			st.Running = strings.EqualFold(parts[1], "running")
			if len(parts) >= 3 {
				st.State = parts[2] // the human "Up 3 hours" / "Exited (0) …" line
			}
		}
	}
	if !strings.Contains(out, marketingContainer) && strings.TrimSpace(out) == "" {
		st.OK = false // couldn't reach the VPS
	}
	mkMu.Lock()
	mkCache, mkAt = st, time.Now()
	mkMu.Unlock()
}

// marketingControl starts / stops / restarts the marketing container over SSH. Returns a result line.
func marketingControl(cfg *Config, action string) string {
	switch action {
	case "start", "stop", "restart":
	default:
		return "invalid action"
	}
	out := marketingSSH(`"docker ` + action + ` ` + marketingContainer + ` 2>&1"`)
	mkMu.Lock()
	mkAt = time.Time{} // bust the cache so the next read reflects the change
	mkMu.Unlock()
	go refreshMarketing(cfg)
	r := strings.TrimSpace(out)
	if r == "" || r == marketingContainer {
		return "✓ " + action + "ed"
	}
	return r
}

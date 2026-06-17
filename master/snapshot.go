package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

// Fleet snapshot: the daemon ALREADY gathers the live state for the dashboard (tickets, devices,
// Sentry, deployments, servers, needs-you). Writing it to one file every minute means every agent
// can READ it instead of independently re-querying Jira/Sentry/GitHub/devices — which is the same
// "agents help each other / don't redo each other's work / no extra tokens" principle as the
// screenshot gallery. All of it is deterministic, cached data — zero AI, cheap.

func startSnapshotWriter(cfg *Config) {
	go func() {
		time.Sleep(10 * time.Second) // let the caches warm
		for {
			writeFleetSnapshot(cfg)
			time.Sleep(60 * time.Second)
		}
	}()
}

func writeFleetSnapshot(cfg *Config) {
	tk := tickets(cfg)
	// keep the snapshot lean: counts + the actionable lists, not the whole 1000-ticket backlog
	ticketsLean := map[string]any{}
	for _, k := range []string{"total", "workingCnt", "queueCnt", "doneCnt", "bugsCnt",
		"working", "queue", "bugs", "tree", "approval", "hwWait", "humanTest"} {
		if v, ok := tk[k]; ok {
			ticketsLean[k] = v
		}
	}
	gallery := ""
	if p := galleryDir(); p != "" {
		gallery = filepath.Join(p, "manifest.json")
	}
	snap := map[string]any{
		"ts":   time.Now().Format(time.RFC3339),
		"note": "Daemon-gathered LIVE state. Agents: READ THIS FIRST before querying Jira/Sentry/GitHub/devices — it's already here, so you don't re-fetch what another check already got. Only hit the source for something missing.",
		"tickets":         ticketsLean,
		"devices":         devices(cfg),
		"sentry":          sentryStats(cfg),
		"deployments":     deployments(cfg),
		"servers":         serverHealth(cfg),
		"needsHuman":      needsHuman(cfg),
		"ownerRequests":   ownerRequests(cfg),
		"screenshotGallery": gallery, // visual-test saves clean screenshots here for the multimedia agent
		"galleryHowTo":    "python3 Claud_worker_agent/scripts/visual_gallery.py list",
	}
	b, err := json.MarshalIndent(snap, "", " ")
	if err != nil {
		return
	}
	_ = osWriteBytes(filepath.Join(cfg.resolve(cfg.LogDir), "fleet-snapshot.json"), b)
}

// galleryDir mirrors scripts/visual_gallery.py's resolution (SSD if mounted, else logs/visual-gallery).
func galleryDir() string {
	ssd := "/Volumes/Extreme SSD/ComputerFilesFromClaudeAI/claudworker-visual-gallery"
	if dirExists("/Volumes/Extreme SSD/ComputerFilesFromClaudeAI") {
		return ssd
	}
	if cfgRef != nil {
		return cfgRef.resolve("logs/visual-gallery")
	}
	return ""
}

func dirExists(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

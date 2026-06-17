package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

// hardwareProjects scans <logs>/status/hw-*.json (written by the hardware-pipeline runner) and
// returns each project's 18-stage pipeline status for the dashboard's Hardware tab. It is defensive:
// any file that can't be read or parsed is skipped rather than failing the whole SSE frame.
func hardwareProjects(cfg *Config) []map[string]any {
	dir := filepath.Join(cfg.resolve(cfg.LogDir), "status")
	matches, _ := filepath.Glob(filepath.Join(dir, "hw-*.json"))
	sort.Strings(matches)
	out := make([]map[string]any, 0, len(matches))
	for _, p := range matches {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		var m map[string]any
		if json.Unmarshal(b, &m) != nil {
			continue
		}
		out = append(out, m)
	}
	return out
}

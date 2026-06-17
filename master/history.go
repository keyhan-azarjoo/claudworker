package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type HistRec struct {
	Time   string  `json:"time"`
	Name   string  `json:"name"`
	Runner string  `json:"runner"`
	Cost   float64 `json:"cost"`
	OK     bool    `json:"ok"`
	Msg    string  `json:"msg"`
}

var histMu sync.Mutex

func histPath(cfg *Config) string { return filepath.Join(cfg.resolve(cfg.LogDir), "history.jsonl") }

func appendHistory(cfg *Config, rec HistRec) {
	histMu.Lock()
	defer histMu.Unlock()
	f, err := os.OpenFile(histPath(cfg), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	b, _ := json.Marshal(rec)
	f.Write(append(b, '\n'))
	f.Close()
}

// recentHistory returns completed runs within the last `hours`, newest first.
func recentHistory(cfg *Config, hours int) []HistRec {
	b, err := os.ReadFile(histPath(cfg))
	if err != nil {
		return nil
	}
	cut := time.Now().Add(-time.Duration(hours) * time.Hour)
	out := []HistRec{}
	for _, ln := range strings.Split(string(b), "\n") {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		var r HistRec
		if json.Unmarshal([]byte(ln), &r) != nil {
			continue
		}
		if t, e := time.Parse(time.RFC3339, r.Time); e == nil && t.After(cut) {
			out = append(out, r)
		}
	}
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out
}

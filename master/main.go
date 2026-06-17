// claudworker — local master that runs a team of Claude CLI workers (agents + jobs)
// and serves a live control panel. Privacy: CLI-only, local, no cloud routines.
package main

import (
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
)

func main() {
	var (
		cfgPath = flag.String("config", "config.json", "path to config.json")
		addr    = flag.String("addr", "127.0.0.1:8787", "control-panel listen address")
		list    = flag.Bool("list", false, "list configured agents/jobs")
		run     = flag.String("run", "", "run one agent/job once, by name")
		all     = flag.Bool("all", false, "run every non-manual task once")
		serveF  = flag.Bool("serve", false, "run the daemon + control panel (recommended)")
		master  = flag.Bool("master", false, "run the scheduler only (no web)")
		pause   = flag.Bool("pause", false, "pause the whole fleet")
		resume  = flag.Bool("resume", false, "resume the whole fleet")
		status  = flag.Bool("status", false, "print pause + usage status")
	)
	flag.Parse()

	bootstrapConfig(*cfgPath) // first run after clone: seed config.json from the example
	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "config:", err)
		os.Exit(1)
	}

	switch {
	case *list:
		doList(cfg)
	case *pause:
		_ = osWrite(cfg.PauseFile, "paused")
		fmt.Println("paused →", cfg.PauseFile)
	case *resume:
		_ = osRemove(cfg.PauseFile)
		fmt.Println("resumed")
	case *status:
		fmt.Printf("paused=%v usage=%d%%\n", isPaused(cfg), usagePct(cfg))
	case *run != "":
		t, ok := cfg.Jobs[*run]
		if !ok {
			fmt.Fprintln(os.Stderr, "unknown task:", *run)
			os.Exit(1)
		}
		acct, _ := selectRunner(cfg)
		if err := runTask(cfg, *run, t, NewRegistry(cfg), acct); err != nil {
			os.Exit(1)
		}
	case *all:
		runAllOnce(cfg)
	case *serveF:
		serve(cfg, *addr)
	case *master:
		runMaster(cfg)
	default:
		flag.Usage()
		doList(cfg)
	}
}

// bootstrapConfig seeds config.json from config.example.json on a fresh clone, so the dashboard
// runs out of the box. It NEVER overwrites an existing config. Looks next to the path and one dir up
// (the binary often lives in master/ while the example lives at the repo root).
func bootstrapConfig(path string) {
	if _, err := os.Stat(path); err == nil {
		return // already configured
	}
	for _, ex := range []string{"config.example.json", "../config.example.json"} {
		if b, err := os.ReadFile(ex); err == nil {
			if werr := os.WriteFile(path, b, 0o644); werr == nil {
				fmt.Fprintf(os.Stderr, "first run: created %s from %s — edit it or use the Accounts ▸ Connect page.\n", path, ex)
			}
			return
		}
	}
}

func doList(cfg *Config) {
	names := make([]string, 0, len(cfg.Jobs))
	for n := range cfg.Jobs {
		names = append(names, n)
	}
	sort.Strings(names)
	fmt.Printf("%-16s %-6s %-10s %s\n", "NAME", "KIND", "SCHEDULE", "PROMPT")
	for _, n := range names {
		t := cfg.Jobs[n]
		fmt.Printf("%-16s %-6s %-10s %s\n", n, t.Kind, schedStr(t), t.PromptFile)
	}
	fmt.Printf("\nmaxConcurrent=%d model=%s\n", cfg.MaxConcurrent, cfg.Model)
}

func schedStr(t Task) string {
	switch {
	case t.Manual:
		return "manual"
	case t.EveryMinutes > 0:
		return "every " + strconv.Itoa(t.EveryMinutes) + "m"
	case t.AtHHMM != "":
		return "at " + t.AtHHMM
	}
	return "-"
}

func runAllOnce(cfg *Config) {
	reg := NewRegistry(cfg)
	sem := make(chan struct{}, cfg.MaxConcurrent)
	var wg sync.WaitGroup
	for name, t := range cfg.Jobs {
		if t.Manual {
			continue
		}
		name, t := name, t
		wg.Add(1)
		go func() {
			defer wg.Done()
			if isPaused(cfg) || !resourceOK(cfg) {
				return
			}
			acct, ok := selectRunner(cfg)
			if !ok {
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			_ = runTask(cfg, name, t, reg, acct)
		}()
	}
	wg.Wait()
}

func runMaster(cfg *Config) {
	reg := NewRegistry(cfg)
	sem := make(chan struct{}, cfg.MaxConcurrent)
	launch := func(name string) {
		t := cfg.Jobs[name]
		go func() {
			if isPaused(cfg) || !resourceOK(cfg) {
				return
			}
			acct, ok := selectRunner(cfg)
			if !ok {
				notifyOwnerThrottled(cfg, "all accounts at usage limit — skipping "+name)
				return
			}
			sem <- struct{}{}
			defer func() { <-sem }()
			_ = runTask(cfg, name, t, reg, acct)
		}()
	}
	fmt.Printf("master (scheduler only) up — %d tasks\n", len(cfg.Jobs))
	scheduler(cfg, reg, launch)
}

func isPaused(cfg *Config) bool {
	if cfg.PauseFile == "" {
		return false
	}
	_, err := os.Stat(cfg.PauseFile)
	return err == nil
}

// ---- small os/util helpers ----

func osWrite(path, s string) error      { return os.WriteFile(path, []byte(s), 0o644) }
func osWriteBytes(path string, b []byte) error { return os.WriteFile(path, b, 0o644) }
func osRemove(path string) error        { return os.Remove(path) }
func atoiSafe(s string) int             { n, _ := strconv.Atoi(s); return n }

// splitList parses a comma/newline separated list into trimmed, non-empty values.
func splitList(s string) []string {
	out := []string{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == '\n' || r == '\r' }) {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// sanitizeLabel turns an account label into a safe filesystem dir segment.
func sanitizeLabel(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		case r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		out = "acct"
	}
	return out
}

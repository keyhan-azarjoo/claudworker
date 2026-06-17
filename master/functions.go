package main

// On-demand runner for the 0-token deterministic FUNCTIONS that replaced
// recurring agents (Sentry monitoring, the test suite). The dashboard exposes a
// manual trigger for each — like "Run queue → pull" — in ADDITION to their
// launchd schedules. These spend NO Claude/codex tokens (plain Python + APIs).

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

const fnRoot = "/Users/keyhanazarjoo/Documents/Claudworker_Project"
const fnPy = "/Library/Frameworks/Python.framework/Versions/3.11/bin/python3"

// allow-listed function scripts (never run arbitrary input)
var fleetFns = map[string][]string{
	"sentry":  {fnPy, "scripts/sentry_watch.py"},
	"citests": {fnPy, "scripts/ci_tests.py"},
}

// runFleetFunctionSync runs a fast function script and returns its last output line.
func runFleetFunctionSync(name string) (string, bool) {
	args, present := fleetFns[name]
	if !present {
		return "unknown function", false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, args[0], args[1:]...)
	c.Dir = fnRoot
	out, err := c.CombinedOutput()
	lines := strings.Split(strings.TrimRight(string(out), "\n"), "\n")
	last := ""
	if len(lines) > 0 {
		last = strings.TrimSpace(lines[len(lines)-1])
	}
	if err != nil && last == "" {
		last = "function error: " + err.Error()
	}
	return last, true
}

// runFleetFunctionBg fires a slower function in the background (returns immediately).
func runFleetFunctionBg(name string) bool {
	args, present := fleetFns[name]
	if !present {
		return false
	}
	go func() {
		c := exec.Command(args[0], args[1:]...)
		c.Dir = fnRoot
		_ = c.Run()
	}()
	return true
}

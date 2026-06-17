package main

import (
	"testing"
	"time"
)

func TestPauseLimit(t *testing.T) {
	cfg := &Config{}
	// default when nothing set
	if got := pauseLimit(cfg, nil); got != defaultWeeklyCap {
		t.Errorf("pauseLimit default = %d, want %d", got, defaultWeeklyCap)
	}
	// global override
	cfg.UsagePausePct = 80
	if got := pauseLimit(cfg, nil); got != 80 {
		t.Errorf("pauseLimit global = %d, want 80", got)
	}
	// per-account override wins over global
	if got := pauseLimit(cfg, &Account{PausePct: 50}); got != 50 {
		t.Errorf("pauseLimit per-account = %d, want 50", got)
	}
}

func TestEffectiveLimitNoBoost(t *testing.T) {
	cfg := &Config{UsagePausePct: 70}
	// No night boost configured → equals base pauseLimit.
	if got := effectiveLimit(cfg, &Account{}); got != 70 {
		t.Errorf("effectiveLimit no-boost = %d, want 70", got)
	}
	if got := effectiveLimit(cfg, nil); got != 70 {
		t.Errorf("effectiveLimit nil acct = %d, want 70", got)
	}
}

func TestPaceConcurrency(t *testing.T) {
	cases := map[string]int{"slow": 1, "light": 4, "medium": 8, "high": 12, "fast": 20, "": 8, "junk": 8}
	for pace, want := range cases {
		if got := paceConcurrency(pace); got != want {
			t.Errorf("paceConcurrency(%q) = %d, want %d", pace, got, want)
		}
	}
}

func TestPaceFanout(t *testing.T) {
	cases := map[string]int{"light": 0, "medium": 1, "high": 2, "fast": 3, "slow": 0, "": 0}
	for pace, want := range cases {
		if got := paceFanout(pace); got != want {
			t.Errorf("paceFanout(%q) = %d, want %d", pace, got, want)
		}
	}
}

func TestAcctConcurrency(t *testing.T) {
	cfg := &Config{Pace: "high"}
	// explicit per-account run-at-once wins
	if got := acctConcurrency(cfg, &Account{Concurrency: 17}); got != 17 {
		t.Errorf("acctConcurrency explicit = %d, want 17", got)
	}
	// per-account pace overrides global pace
	if got := acctConcurrency(cfg, &Account{Pace: "slow"}); got != 1 {
		t.Errorf("acctConcurrency acct-pace = %d, want 1", got)
	}
	// fall back to global pace
	if got := acctConcurrency(cfg, &Account{}); got != paceConcurrency("high") {
		t.Errorf("acctConcurrency global = %d, want %d", got, paceConcurrency("high"))
	}
	// nil account → global pace
	if got := acctConcurrency(cfg, nil); got != paceConcurrency("high") {
		t.Errorf("acctConcurrency nil = %d", got)
	}
}

func TestAcctFanout(t *testing.T) {
	cfg := &Config{Pace: "fast"}
	if got := acctFanout(cfg, &Account{Fanout: 9}); got != 9 {
		t.Errorf("acctFanout explicit = %d, want 9", got)
	}
	if got := acctFanout(cfg, &Account{Pace: "medium"}); got != 1 {
		t.Errorf("acctFanout acct-pace = %d, want 1", got)
	}
	if got := acctFanout(cfg, &Account{}); got != paceFanout("fast") {
		t.Errorf("acctFanout global = %d", got)
	}
}

func TestFleetCap(t *testing.T) {
	// no accounts → falls back to a single account's concurrency for the global pace
	cfg := &Config{Pace: "medium"}
	if got := fleetCap(cfg); got != acctConcurrency(cfg, nil) {
		t.Errorf("fleetCap no-accounts = %d, want %d", got, acctConcurrency(cfg, nil))
	}
	// codex accounts (no claude login lookup): caps sum. Paused accounts contribute nothing.
	cfg = &Config{
		Pace: "medium",
		Accounts: []Account{
			{Label: "fc-a", Engine: "codex", Concurrency: 3},
			{Label: "fc-b", Engine: "codex", Concurrency: 5},
			{Label: "fc-paused", Engine: "codex", Concurrency: 99},
		},
	}
	setAcctPaused("fc-paused", true)
	defer setAcctPaused("fc-paused", false)
	if got := fleetCap(cfg); got != 8 {
		t.Errorf("fleetCap sum = %d, want 8 (3+5, paused excluded)", got)
	}
}

func TestFleetCapCeiling(t *testing.T) {
	cfg := &Config{
		Pace: "medium",
		Accounts: []Account{
			{Label: "cap-a", Engine: "codex", Concurrency: 40},
			{Label: "cap-b", Engine: "codex", Concurrency: 40},
		},
	}
	if got := fleetCap(cfg); got != 60 {
		t.Errorf("fleetCap ceiling = %d, want 60", got)
	}
}

func TestWithinScheduleNoSchedule(t *testing.T) {
	if !withinSchedule(nil) {
		t.Error("nil account should be 24/7 available")
	}
	if !withinSchedule(&Account{}) {
		t.Error("empty schedule should be 24/7 available")
	}
}

func TestWithinScheduleHourWindow(t *testing.T) {
	h := time.Now().Hour()
	// Build a window [h, h+1) that definitely contains the current hour (skip the h==23 wrap edge).
	if h < 23 {
		if !withinSchedule(&Account{FromHour: h, ToHour: h + 1}) {
			t.Errorf("hour %d should be inside [%d,%d)", h, h, h+1)
		}
	}
	// A window that ends at/before the current hour excludes it (avoid the 0 sentinel).
	if h >= 2 {
		if withinSchedule(&Account{FromHour: 1, ToHour: 2}) {
			t.Errorf("hour %d should be outside [1,2)", h)
		}
	}
}

func TestDayInRange(t *testing.T) {
	// range form
	for wd := 1; wd <= 7; wd++ {
		want := wd >= 1 && wd <= 5
		if got := dayInRange("1-5", wd); got != want {
			t.Errorf("dayInRange(1-5, %d) = %v, want %v", wd, got, want)
		}
	}
	// list form
	if !dayInRange("1,3,5", 3) || dayInRange("1,3,5", 2) {
		t.Error("dayInRange list form wrong")
	}
	// mixed + whitespace
	if !dayInRange(" 6 , 7 ", 7) {
		t.Error("dayInRange should trim whitespace")
	}
	if dayInRange("", 3) {
		t.Error("empty spec should match nothing")
	}
}

func TestAcctPausedState(t *testing.T) {
	const label = "ut-paused-acct"
	if isAcctPaused(label) {
		t.Error("fresh label should not be paused")
	}
	setAcctPaused(label, true)
	if !isAcctPaused(label) {
		t.Error("setAcctPaused(true) not reflected")
	}
	setAcctPaused(label, false)
	if isAcctPaused(label) {
		t.Error("setAcctPaused(false) not reflected")
	}
}

func TestCapOverrideState(t *testing.T) {
	const label = "ut-cap-acct"
	if capOverride(label) {
		t.Error("fresh label should have no cap override")
	}
	setCapOverride(label)
	if !capOverride(label) {
		t.Error("setCapOverride not reflected")
	}
	clearCapOverride(label)
	if capOverride(label) {
		t.Error("clearCapOverride not reflected")
	}
}

func TestAcctRunningCount(t *testing.T) {
	const label = "ut-running-acct"
	if acctRunningCount(label) != 0 {
		t.Error("fresh label running count should be 0")
	}
	incAcctRunning(label)
	incAcctRunning(label)
	if acctRunningCount(label) != 2 {
		t.Errorf("running count = %d, want 2", acctRunningCount(label))
	}
	decAcctRunning(label)
	if acctRunningCount(label) != 1 {
		t.Errorf("running count after dec = %d, want 1", acctRunningCount(label))
	}
	// must never go below zero
	decAcctRunning(label)
	decAcctRunning(label)
	if acctRunningCount(label) != 0 {
		t.Errorf("running count floor = %d, want 0", acctRunningCount(label))
	}
}

func TestCooldown(t *testing.T) {
	const label = "ut-cooldown-acct"
	if inCooldown(label) {
		t.Error("fresh label should not be in cooldown")
	}
	if cooldownRemainingMin(label) != -1 {
		t.Error("no cooldown should report -1 minutes")
	}
	setCooldown(label, 10*time.Minute)
	if !inCooldown(label) {
		t.Error("setCooldown not reflected")
	}
	if m := cooldownRemainingMin(label); m < 9 || m > 11 {
		t.Errorf("cooldownRemainingMin = %d, want ~10", m)
	}
	// expired cooldown
	setCooldown(label, -1*time.Minute)
	if inCooldown(label) {
		t.Error("expired cooldown should report not in cooldown")
	}
	if cooldownRemainingMin(label) != -1 {
		t.Error("expired cooldown should report -1 minutes")
	}
}

package main

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestAtoiSafe(t *testing.T) {
	cases := map[string]int{"42": 42, "-7": -7, "0": 0, "": 0, "abc": 0, "12x": 0}
	for in, want := range cases {
		if got := atoiSafe(in); got != want {
			t.Errorf("atoiSafe(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestSplitList(t *testing.T) {
	got := splitList(" a, b ,,c\nd\r e ")
	want := []string{"a", "b", "c", "d", "e"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("splitList = %v, want %v", got, want)
	}
	if got := splitList("   "); len(got) != 0 {
		t.Errorf("splitList(blank) = %v, want empty", got)
	}
}

func TestSanitizeLabel(t *testing.T) {
	cases := map[string]string{
		"Admin Account":  "admin-account",
		"  Claudworker! ":     "claudworker",
		"a/b\\c":         "a-b-c",
		"keep-_.dots":    "keep-_.dots",
		"":               "acct",
		"###":            "acct",
		"UPPER123":       "upper123",
	}
	for in, want := range cases {
		if got := sanitizeLabel(in); got != want {
			t.Errorf("sanitizeLabel(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestOSWriteHelpers(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f.txt")
	if err := osWrite(p, "hello"); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "hello" {
		t.Errorf("osWrite content = %q", b)
	}
	if err := osWriteBytes(p, []byte("bytes")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(p); string(b) != "bytes" {
		t.Errorf("osWriteBytes content = %q", b)
	}
	if err := osRemove(p); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Error("osRemove did not remove file")
	}
}

func TestIsPaused(t *testing.T) {
	if isPaused(&Config{PauseFile: ""}) {
		t.Error("isPaused with no pause file should be false")
	}
	dir := t.TempDir()
	pf := filepath.Join(dir, "PAUSED")
	cfg := &Config{PauseFile: pf}
	if isPaused(cfg) {
		t.Error("isPaused before file exists should be false")
	}
	_ = osWrite(pf, "")
	if !isPaused(cfg) {
		t.Error("isPaused after file created should be true")
	}
}

func TestPaceNum(t *testing.T) {
	cases := map[string]int{"slow": 1, "light": 2, "medium": 3, "high": 4, "fast": 5, "": 3, "WeIrD": 3, " FAST ": 5}
	for pace, want := range cases {
		if got := paceNum(&Config{Pace: pace}); got != want {
			t.Errorf("paceNum(%q) = %d, want %d", pace, got, want)
		}
	}
}

func TestIdleFill(t *testing.T) {
	// paused → never idle-fill
	dir := t.TempDir()
	pf := filepath.Join(dir, "PAUSED")
	_ = osWrite(pf, "")
	if idleFill(&Config{Pace: "fast", PauseFile: pf}) {
		t.Error("idleFill should be false when paused")
	}
	// not paused: high/fast yes, medium/slow no
	if !idleFill(&Config{Pace: "fast"}) || !idleFill(&Config{Pace: "high"}) {
		t.Error("idleFill should be true at high/fast")
	}
	if idleFill(&Config{Pace: "medium"}) || idleFill(&Config{Pace: "slow"}) {
		t.Error("idleFill should be false at medium/slow")
	}
}

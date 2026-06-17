package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadConfigDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(`{"logDir":"logs"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatalf("loadConfig: %v", err)
	}
	if c.MaxConcurrent != 4 {
		t.Errorf("MaxConcurrent default = %d, want 4", c.MaxConcurrent)
	}
	if c.JobTimeoutMinutes != 120 {
		t.Errorf("JobTimeoutMinutes default = %d, want 120", c.JobTimeoutMinutes)
	}
	if c.Model != "claude-opus-4-8" {
		t.Errorf("Model default = %q, want claude-opus-4-8", c.Model)
	}
	if c.root != dir {
		t.Errorf("root = %q, want %q", c.root, dir)
	}
}

func TestLoadConfigKeepsExplicitValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	body := `{"maxConcurrent":9,"jobTimeoutMinutes":30,"model":"custom-model"}`
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	c, err := loadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.MaxConcurrent != 9 || c.JobTimeoutMinutes != 30 || c.Model != "custom-model" {
		t.Errorf("explicit values overwritten: %+v", *c)
	}
}

func TestLoadConfigErrors(t *testing.T) {
	if _, err := loadConfig(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Error("expected error for missing file")
	}
	dir := t.TempDir()
	bad := filepath.Join(dir, "bad.json")
	_ = os.WriteFile(bad, []byte("{not json"), 0o644)
	if _, err := loadConfig(bad); err == nil {
		t.Error("expected parse error for invalid json")
	}
}

func TestConfigResolve(t *testing.T) {
	c := &Config{root: "/srv/fleet"}
	if got := c.resolve(""); got != "" {
		t.Errorf("resolve(\"\") = %q, want empty", got)
	}
	if got := c.resolve("/abs/path"); got != "/abs/path" {
		t.Errorf("resolve(abs) = %q, want unchanged", got)
	}
	if got := c.resolve("logs"); got != filepath.Join("/srv/fleet", "logs") {
		t.Errorf("resolve(rel) = %q", got)
	}
}

func TestVoiceDefaults(t *testing.T) {
	got := voiceDefaults(VoiceConfig{})
	if len(got.AgentNames) == 0 || len(got.WakeWords) == 0 {
		t.Error("voiceDefaults left names/wake words empty")
	}
	if got.Language != "both" || got.VoiceGender != "female" {
		t.Errorf("voiceDefaults Language/Gender = %q/%q", got.Language, got.VoiceGender)
	}
	if got.STTEngine != "azure" || got.TTSEngine != "azure" {
		t.Errorf("voiceDefaults STT/TTS = %q/%q", got.STTEngine, got.TTSEngine)
	}
	if len(got.ActiveWindows) != 1 || got.ActiveWindows[0].From != "17:30" {
		t.Errorf("voiceDefaults windows = %+v", got.ActiveWindows)
	}

	// Existing values must be preserved, not overwritten.
	custom := VoiceConfig{
		AgentNames:    []string{"X"},
		WakeWords:     []string{"yo"},
		Language:      "en",
		VoiceGender:   "male",
		STTEngine:     "google",
		TTSEngine:     "local",
		ActiveWindows: []VoiceWindow{{From: "09:00", To: "10:00"}},
	}
	out := voiceDefaults(custom)
	if out.Language != "en" || out.VoiceGender != "male" || out.STTEngine != "google" || out.TTSEngine != "local" {
		t.Errorf("voiceDefaults overwrote explicit values: %+v", out)
	}
	if out.AgentNames[0] != "X" || out.WakeWords[0] != "yo" || out.ActiveWindows[0].From != "09:00" {
		t.Errorf("voiceDefaults overwrote explicit slices: %+v", out)
	}
}

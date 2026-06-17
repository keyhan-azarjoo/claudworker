package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// Task is one schedulable unit: a team role (kind=agent) or a granular task (kind=job).
type Task struct {
	Kind         string `json:"kind"`         // "agent" | "job"
	PromptFile   string `json:"promptFile"`   // path (relative to repo root) to the prompt
	EveryMinutes int    `json:"everyMinutes"` // interval schedule (0 = none)
	AtHHMM       string `json:"atHHMM"`       // daily time "HH:MM" ("" = none)
	Manual       bool   `json:"manual"`       // only runs on -run
	AllowedTools string `json:"allowedTools"` // claude --allowedTools value
	MaxTurns     int    `json:"maxTurns"`     // claude --max-turns
	Model        string `json:"model"`        // per-task model override (cheaper model for light tasks)
}

// MonitorTarget is a server/VPS endpoint health-checked on the dashboard.
type MonitorTarget struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

// Account is one worker "runner" — a Claude login profile OR another engine (e.g. Codex).
// The master round-robins workers across all runners to use every account/engine at full tilt.
//   engine "claude" (default): runs `claude` with CLAUDE_CONFIG_DIR=ConfigDir (login each profile once).
//   engine "codex" / other:    runs Cmd in a shell with $WORKER_PROMPT / $WORKER_TOOLS set.
type Account struct {
	Label     string `json:"label"`
	Engine    string `json:"engine"`    // "claude" | "codex" | ...
	ConfigDir string `json:"configDir"` // claude profile dir (~ ok)
	Cmd       string `json:"cmd"`       // non-claude engines: shell command using $WORKER_PROMPT
	PausePct  int    `json:"pausePct"`  // stop at this usage % (admin=99, others≈80); 0 → global
	Notify    bool   `json:"notify"`    // ping owner on Telegram when this account pauses
	// Time-aware boost: after midnight, while several hours remain before reset, use a higher cap.
	NightPausePct  int `json:"nightPausePct"`  // boosted cap during the night window (0 = no boost)
	NightStartHour int `json:"nightStartHour"` // local hour the night window starts (default 0 = midnight)
	ResetHour      int `json:"resetHour"`      // local hour the quota resets (boost needs >=3h before it)
	// Per-account scheduling + pause mode + speed (set on the account page). Empty = use the global default.
	HardPause bool   `json:"hardPause"` // true = PAUSE before the warning; false = keep working (no pause)
	Days      string `json:"days"`      // operating days, e.g. "1-5" (Mon..Fri); "" = every day
	FromHour  int    `json:"fromHour"`  // operating window start hour (0); 0/0 = 24-7
	ToHour    int    `json:"toHour"`    // operating window end hour (e.g. 17); 0/0 = 24-7
	Pace      string `json:"pace"`      // per-account weekly pace: slow/light/medium/high/fast ("" = global)
	Effort    string `json:"effort"`    // per-account claude --effort ("" = global)
	Concurrency int  `json:"concurrency"` // per-account agents-at-once ("run at once"); 0 = derive from speed
	Fanout    int    `json:"fanout"`    // per-account sub-agents per agent (MO_FANOUT); 0 = global default
	Paused    bool   `json:"paused"`    // owner paused this account — PERSISTED so a restart never resumes it
	UseSonnet bool   `json:"useSonnet"` // run THIS account's agents on Sonnet (cheaper) instead of Opus/their task model
}

// VoiceWindow = an active-hours window for the local wake-word assistant ("HH:MM"). The window
// may wrap past midnight (From=17:30, To=01:00 means active 5:30pm→1am).
type VoiceWindow struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// VoiceConfig = the "Tgo" voice assistant settings (local wake word + Azure/Google STT/TTS).
type VoiceConfig struct {
	Enabled       bool          `json:"enabled"`       // master on/off (default OFF — mostly off)
	AgentNames    []string      `json:"agentNames"`    // what the assistant is called, several allowed
	WakeWords     []string      `json:"wakeWords"`     // phrases that wake it, several allowed
	Language      string        `json:"language"`      // "en" | "fa" | "both"
	VoiceGender   string        `json:"voiceGender"`   // "female" | "male"
	STTEngine     string        `json:"sttEngine"`     // "azure" | "google" | "local"
	TTSEngine     string        `json:"ttsEngine"`     // "azure" | "google" | "local"
	ActiveWindows []VoiceWindow `json:"activeWindows"` // local assistant only listens inside these hours
}

// voiceDefaults fills any unset Voice fields with sensible defaults.
func voiceDefaults(v VoiceConfig) VoiceConfig {
	if len(v.AgentNames) == 0 {
		v.AgentNames = []string{"Tgo Agent", "Tgo Main Agent"}
	}
	if len(v.WakeWords) == 0 {
		v.WakeWords = []string{"hey tgo", "tgo"}
	}
	if v.Language == "" {
		v.Language = "both"
	}
	if v.VoiceGender == "" {
		v.VoiceGender = "female"
	}
	if v.STTEngine == "" {
		v.STTEngine = "azure"
	}
	if v.TTSEngine == "" {
		v.TTSEngine = "azure"
	}
	if len(v.ActiveWindows) == 0 {
		// default: local assistant active 5:30pm → 1am (off 1am → 5:30pm)
		v.ActiveWindows = []VoiceWindow{{From: "17:30", To: "01:00"}}
	}
	return v
}

type Config struct {
	SecretsPath      string          `json:"secretsPath"`
	ProjectDir       string          `json:"projectDir"`
	Model            string          `json:"model"`
	FallbackModel    string          `json:"fallbackModel"`
	Accounts         []Account       `json:"accounts"`
	MaxConcurrent    int             `json:"maxConcurrent"`
	UsagePausePct    int             `json:"usagePausePct"`
	UsageResumePct   int             `json:"usageResumePct"`
	PauseFile        string          `json:"pauseFile"`
	LogDir           string          `json:"logDir"`
	PrivacyRoleFile  string          `json:"privacyRoleFile"`
	JobTimeoutMinutes int            `json:"jobTimeoutMinutes"`
	PanelToken       string          `json:"panelToken"` // session secret (auto-generated)
	PanelUser        string          `json:"panelUser"`  // super-admin login user (defaults to MARKETING_DASHBOARD_USER)
	PanelPass        string          `json:"panelPass"`  // super-admin login pass (defaults to MARKETING_DASHBOARD_PASSWORD)
	MinFreeGB        int             `json:"minFreeGB"`  // pause launching new workers if free disk < this (0 = off)
	// Runtime controls (toggled from the dashboard; persisted on change).
	Boost   bool   `json:"boost"`   // true = "Work now" (continuous); false = "Scheduler" (timed only)
	Pace    string `json:"pace"`    // "fast" (all at once) | "medium" | "slow"
	SchedulerMins int `json:"schedulerMins"` // Scheduler-mode: run the fleet every N minutes (e.g. 10, 30)
	Effort  string `json:"effort"`  // "" | low | medium | high | xhigh | max  → claude --effort
	Fanout  int    `json:"fanout"`  // sub-agents each agent should spawn (passed as MO_FANOUT)
	InFlightCap int `json:"inFlightCap"` // max concurrent PROGRAMMING agents (the costly Opus ones); 0 = default 7
	TestWindowFrom string `json:"testWindowFrom"` // global device-test window start "HH:MM" (default 02:00)
	TestWindowTo   string `json:"testWindowTo"`   // global device-test window end "HH:MM" (default 09:00)
	PausedAgents   []string `json:"pausedAgents"` // agents the owner paused — PERSISTED so a restart keeps them paused
	MMAutoApprove bool `json:"mmAutoApprove"` // auto-approve generated multimedia (else each waits for the owner)
	BannerAccount string   `json:"bannerAccount"`  // legacy single (kept for back-compat)
	BannerAccounts []string `json:"bannerAccounts"` // which accounts' usage show in the top banner (multi-select)
	Voice         VoiceConfig `json:"voice"`     // "Tgo" local voice-assistant settings
	Monitor []MonitorTarget `json:"monitor"` // VPS/servers to health-check on the dashboard
	Jobs             map[string]Task `json:"jobs"`

	root string // dir the config lives in (used to resolve relative paths)
}

func loadConfig(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	abs, _ := filepath.Abs(path)
	c.root = filepath.Dir(abs)
	if c.MaxConcurrent <= 0 {
		c.MaxConcurrent = 4
	}
	if c.JobTimeoutMinutes <= 0 {
		c.JobTimeoutMinutes = 120
	}
	if c.Model == "" {
		c.Model = "claude-opus-4-8"
	}
	if c.SecretsPath == "" {
		c.SecretsPath = "secrets.json" // fresh clone: local gitignored vault next to config.json
	}
	if c.ProjectDir == "" {
		c.ProjectDir = ".."
	}
	return &c, nil
}

// resolve makes a config-relative path absolute (relative to the config file dir).
func (c *Config) resolve(p string) string {
	if p == "" {
		return ""
	}
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.root, p)
}

package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// Device Simulator control — PURELY FUNCTIONAL (no AI). The simulator (Hardware/Claudworker-Dummy-Devices)
// impersonates the full Claudworker device fleet (temp, plug, camera, hub, flame/gas/smoke detectors, covers…)
// over the REAL protocol against STAGING under the throwaway test account. It runs HEADLESS (headless.py —
// the standalone website is retired); the owner manages it entirely from the dashboard. Device management
// edits the simulator's data/config.json directly (instant, zero staging calls); "Apply" restarts the
// engine once to (re)register the new topology — so we never storm staging's rate limiter.

func simDir(cfg *Config) string {
	return filepath.Join(cfg.resolve(cfg.ProjectDir), "Hardware", "Claudworker-Dummy-Devices")
}

// simulatorRunning: is the headless engine process alive?
func simulatorRunning() bool {
	return strings.TrimSpace(runc("pgrep -f 'Claudworker-Dummy-Devices/.venv/bin/python headless.py' 2>/dev/null", 4*time.Second)) != ""
}

var (
	simStartMu sync.Mutex
	simDirty   int32 // 1 = config.json changed since the last (re)start → needs Apply to register
)

// simulatorStart launches the headless engine (idempotent). Test account from the vault, staging only.
func simulatorStart(cfg *Config) error {
	simStartMu.Lock()
	defer simStartMu.Unlock()
	if simulatorRunning() {
		return nil
	}
	dir := simDir(cfg)
	if !dirExists(dir) {
		return fmt.Errorf("simulator dir not found: %s", dir)
	}
	py := filepath.Join(dir, ".venv", "bin", "python")
	if !fileExists(py) {
		py = "python3"
	}
	email := secret(cfg, "TEST_ACCOUNT_1_EMAIL")
	pass := secret(cfg, "TEST_ACCOUNT_1_PASSWORD")
	if email == "" || pass == "" {
		return fmt.Errorf("no TEST_ACCOUNT_1 creds in the vault — set them in Devices → 🧪 Test login accounts first")
	}
	transport := secret(cfg, "SIM_TRANSPORT")
	if transport == "" {
		transport = "signalr" // avoid the MQTT mTLS leg (staging broker rejects the sim's self-signed CA)
	}
	logPath := filepath.Join(cfg.resolve(cfg.LogDir), "simulator.out")
	out, _ := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	cmd := exec.Command(py, "headless.py")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"SIM_TRANSPORT="+transport,
		"API_BASE=https://staging.api.example.com",
		"MYOTGO_EMAIL="+email,
		"MYOTGO_PASSWORD="+pass,
	)
	if out != nil {
		cmd.Stdout, cmd.Stderr = out, out
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true} // detach so it survives a daemon restart
	if err := cmd.Start(); err != nil {
		return err
	}
	_ = cmd.Process.Release()
	atomic.StoreInt32(&simDirty, 0)
	logf(cfg, "simulator", "started headless device simulator (pid %d) → staging, test account", cmd.Process.Pid)
	return nil
}

// simulatorStop kills the headless engine.
func simulatorStop(cfg *Config) {
	runc("pkill -f 'Claudworker-Dummy-Devices/.venv/bin/python headless.py' 2>/dev/null", 6*time.Second)
	logf(cfg, "simulator", "stopped device simulator")
}

// simulatorApply restarts the engine so config.json edits take effect (one staging login, not per-edit).
func simulatorApply(cfg *Config) error {
	simulatorStop(cfg)
	time.Sleep(1500 * time.Millisecond)
	return simulatorStart(cfg)
}

// ── functional device management (edits data/config.json directly — no staging calls, no AI) ──────────

func simConfigPath(cfg *Config) string { return filepath.Join(simDir(cfg), "data", "config.json") }

func loadSimConfig(cfg *Config) (map[string]any, error) {
	b, err := os.ReadFile(simConfigPath(cfg))
	if err != nil {
		return nil, err
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil, err
	}
	return m, nil
}

func saveSimConfig(cfg *Config, m map[string]any) error {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(simConfigPath(cfg), b, 0o644); err != nil {
		return err
	}
	atomic.StoreInt32(&simDirty, 1) // topology changed → needs Apply
	return nil
}

func simGroups(m map[string]any) []any {
	g, _ := m["groups"].([]any)
	return g
}

func simAddDevice(cfg *Config, group, dtype, name string, sockets int) error {
	group, dtype, name = strings.TrimSpace(group), strings.TrimSpace(dtype), strings.TrimSpace(name)
	if group == "" || dtype == "" || name == "" {
		return fmt.Errorf("group, type and name are required")
	}
	m, err := loadSimConfig(cfg)
	if err != nil {
		return err
	}
	groups := simGroups(m)
	var gm map[string]any
	for _, g := range groups {
		if x, ok := g.(map[string]any); ok && x["name"] == group {
			gm = x
			break
		}
	}
	if gm == nil {
		gm = map[string]any{"name": group, "devices": []any{}}
		groups = append(groups, gm)
		m["groups"] = groups
	}
	dev := map[string]any{"type": dtype, "name": name}
	if dtype == "extension" && sockets > 0 {
		dev["sockets"] = sockets
	}
	devs, _ := gm["devices"].([]any)
	gm["devices"] = append(devs, dev)
	return saveSimConfig(cfg, m)
}

func simRemoveDevice(cfg *Config, group, name string) error {
	m, err := loadSimConfig(cfg)
	if err != nil {
		return err
	}
	for _, g := range simGroups(m) {
		x, ok := g.(map[string]any)
		if !ok || x["name"] != group {
			continue
		}
		devs, _ := x["devices"].([]any)
		out := []any{}
		for _, d := range devs {
			if dm, ok := d.(map[string]any); ok && dm["name"] == name {
				continue
			}
			out = append(out, d)
		}
		x["devices"] = out
	}
	return saveSimConfig(cfg, m)
}

func simAddGroup(cfg *Config, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("group name required")
	}
	m, err := loadSimConfig(cfg)
	if err != nil {
		return err
	}
	for _, g := range simGroups(m) {
		if x, ok := g.(map[string]any); ok && x["name"] == name {
			return nil // already exists
		}
	}
	m["groups"] = append(simGroups(m), map[string]any{"name": name, "devices": []any{}})
	return saveSimConfig(cfg, m)
}

func simRemoveGroup(cfg *Config, name string) error {
	m, err := loadSimConfig(cfg)
	if err != nil {
		return err
	}
	out := []any{}
	for _, g := range simGroups(m) {
		if x, ok := g.(map[string]any); ok && x["name"] == name {
			continue
		}
		out = append(out, g)
	}
	m["groups"] = out
	return saveSimConfig(cfg, m)
}

func simSetTransport(cfg *Config, mode string) error {
	mode = strings.TrimSpace(mode)
	if mode != "mqtt" && mode != "signalr" && mode != "both" {
		return fmt.Errorf("transport must be mqtt | signalr | both")
	}
	m, err := loadSimConfig(cfg)
	if err != nil {
		return err
	}
	m["transport"] = mode
	_ = secretSet(cfg, "SIM_TRANSPORT", mode) // also the launch default
	return saveSimConfig(cfg, m)
}

// ── status (read straight off the sim's files — functional, no auth) ─────────────────────────────────

// SimDev is one simulated device for the dashboard's native render.
type SimDev struct {
	Group      string `json:"group"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	Sockets    int    `json:"sockets,omitempty"`
	Registered bool   `json:"registered"`
}

func simDevices(cfg *Config) []SimDev {
	out := []SimDev{}
	m, err := loadSimConfig(cfg)
	if err != nil {
		return out
	}
	reg := map[string]bool{}
	if sb, e := os.ReadFile(filepath.Join(simDir(cfg), "data", "state.json")); e == nil {
		var st struct {
			Devices map[string]any `json:"devices"`
		}
		if json.Unmarshal(sb, &st) == nil {
			for k := range st.Devices {
				reg[k] = true
			}
		}
	}
	for _, g := range simGroups(m) {
		gm, ok := g.(map[string]any)
		if !ok {
			continue
		}
		gname, _ := gm["name"].(string)
		devs, _ := gm["devices"].([]any)
		for _, d := range devs {
			dm, ok := d.(map[string]any)
			if !ok {
				continue
			}
			name, _ := dm["name"].(string)
			dtype, _ := dm["type"].(string)
			sock := 0
			if s, ok := dm["sockets"].(float64); ok {
				sock = int(s)
			}
			out = append(out, SimDev{Group: gname, Name: name, Type: dtype, Sockets: sock,
				Registered: reg[gname+"::"+name]})
		}
	}
	return out
}

func simGroupNames(cfg *Config) []string {
	names := []string{}
	if m, err := loadSimConfig(cfg); err == nil {
		for _, g := range simGroups(m) {
			if x, ok := g.(map[string]any); ok {
				if n, ok := x["name"].(string); ok {
					names = append(names, n)
				}
			}
		}
	}
	return names
}

func simTransport(cfg *Config) string {
	if m, err := loadSimConfig(cfg); err == nil {
		if t, ok := m["transport"].(string); ok && t != "" {
			return t
		}
	}
	return "signalr"
}

func simulatorStatus(cfg *Config) map[string]any {
	return map[string]any{
		"running":    simulatorRunning(),
		"dirty":      atomic.LoadInt32(&simDirty) == 1,
		"devices":    len(simDevices(cfg)),
		"deviceList": simDevices(cfg),
		"groups":     simGroupNames(cfg),
		"transport":  simTransport(cfg),
		"env":        "staging",
	}
}

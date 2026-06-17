package main

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// SubStatus is one sub-agent inside an agent's "chatroom" + its checklist.
type SubStatus struct {
	Name  string   `json:"name"`
	Pct   int      `json:"pct"`
	Did   []string `json:"did"`   // ✓ completed
	Doing string   `json:"doing"` // → in progress
	Todo  []string `json:"todo"`  // ☐ remaining
}

// Progress is what an agent writes to logs/status/<name>.json while it works.
type Progress struct {
	Pct       int         `json:"pct"`      // 0..100
	Done      bool        `json:"done"`     // true → green check
	DoneLine  string      `json:"doneLine"` // one line: what's done
	TodoLine  string      `json:"todoLine"` // one line: what's left
	Did       []string    `json:"did"`      // ✓ completed checklist steps (what the agent already did)
	Fail      []string    `json:"fail"`     // ✗ steps that FAILED / errors found (short: "login: 401")
	Todo      []string    `json:"todo"`     // ☐ remaining checklist steps
	Phase     string      `json:"phase"`    // "", "build", "deploy", "test", … → shown on the Builds/Deploys bar
	SubAgents []SubStatus `json:"subAgents"`
}

// RunInfo is the live state of one agent/job for the control panel.
type RunInfo struct {
	Name      string    `json:"name"`
	Kind      string    `json:"kind"`
	Schedule  string    `json:"schedule"`
	State     string    `json:"state"` // idle | running | paused | done | error
	Progress  Progress  `json:"progress"`
	StartedAt time.Time `json:"startedAt"`
	LastRun   time.Time `json:"lastRun"`
	LastCost  float64   `json:"lastCost"`
	LastMsg   string    `json:"lastMsg"`
	PID       int       `json:"pid"`
}

type Registry struct {
	mu      sync.Mutex
	cfg     *Config
	info    map[string]*RunInfo
	cmds    map[string]*exec.Cmd
	cancel  map[string]context.CancelFunc
	paused  map[string]bool
	adopted map[string]int // agents whose process we ADOPTED after a daemon restart (name → external PID)
}

func NewRegistry(cfg *Config) *Registry {
	r := &Registry{cfg: cfg, info: map[string]*RunInfo{}, cmds: map[string]*exec.Cmd{},
		cancel: map[string]context.CancelFunc{}, paused: map[string]bool{}, adopted: map[string]int{}}
	for name, t := range cfg.Jobs {
		r.info[name] = &RunInfo{Name: name, Kind: t.Kind, Schedule: schedStr(t), State: "idle"}
	}
	return r
}

// adopt marks an agent as running using an EXTERNAL pid — used after a daemon restart to re-attach to a
// worker that's still running (it was launched by the previous daemon and orphaned, not killed). Without
// this the dashboard shows it as idle ("0 working") while it's actually driving a sim/test in the background.
func (r *Registry) adopt(name string, pid int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ri := r.info[name]
	if ri == nil {
		return
	}
	if _, running := r.cmds[name]; running {
		return // already tracked by this daemon
	}
	ri.State = "running"
	ri.PID = pid
	ri.StartedAt = time.Now()
	ri.LastMsg = "running (adopted after daemon restart)"
	r.adopted[name] = pid
}

// reapAdopted clears adopted agents whose process has exited (so they don't show "running" forever).
func (r *Registry) reapAdopted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, pid := range r.adopted {
		if pidAlive(pid) {
			continue
		}
		delete(r.adopted, name)
		if ri := r.info[name]; ri != nil && r.cmds[name] == nil {
			ri.State = "done"
			ri.PID = 0
			ri.LastRun = time.Now()
			ri.Progress = Progress{Pct: 100, Done: true, DoneLine: "finished (was adopted after a restart)"}
		}
	}
}

func (r *Registry) setRunning(name string, cmd *exec.Cmd, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ri := r.info[name]
	if ri == nil {
		return
	}
	ri.State = "running"
	ri.StartedAt = time.Now()
	ri.Progress = Progress{}
	r.cmds[name] = cmd
	r.cancel[name] = cancel
	if cmd != nil && cmd.Process != nil {
		ri.PID = cmd.Process.Pid
	}
}

func (r *Registry) setFinished(name string, cost float64, msg string, failed bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ri := r.info[name]
	if ri == nil {
		return
	}
	ri.LastRun = time.Now()
	ri.LastCost = cost
	ri.LastMsg = msg
	ri.PID = 0
	if failed {
		ri.State = "error"
	} else {
		ri.State = "done"
		ri.Progress = Progress{Pct: 100, Done: true, DoneLine: oneLine(msg, 120)}
	}
	delete(r.cmds, name)
	delete(r.cancel, name)
}

// setRetry: a runner/account failure (not a real task error) → back to idle so the fleet re-runs it
// on a working account. Keeps the UI clean (no error state).
func (r *Registry) setRetry(name, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	ri := r.info[name]
	if ri == nil {
		return
	}
	ri.State = "idle"
	ri.LastMsg = msg
	ri.PID = 0
	delete(r.cmds, name)
	delete(r.cancel, name)
}

func (r *Registry) stop(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if c, ok := r.cancel[name]; ok {
		c()
	}
	if cmd, ok := r.cmds[name]; ok && cmd.Process != nil {
		_ = cmd.Process.Kill()
		if ri := r.info[name]; ri != nil {
			ri.State = "idle"
			ri.PID = 0
		}
		delete(r.cmds, name)
		delete(r.cancel, name)
		return true
	}
	if pid, ok := r.adopted[name]; ok { // adopted orphan → kill it by PID
		if p, e := os.FindProcess(pid); e == nil {
			_ = p.Kill()
		}
		delete(r.adopted, name)
		if ri := r.info[name]; ri != nil {
			ri.State = "idle"
			ri.PID = 0
		}
		return true
	}
	return false
}

func (r *Registry) runningCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.cmds) + len(r.adopted)
}

func (r *Registry) stopAll() int {
	r.mu.Lock()
	names := make([]string, 0, len(r.cmds))
	for n := range r.cmds {
		names = append(names, n)
	}
	r.mu.Unlock()
	for _, n := range names {
		r.stop(n)
	}
	return len(names)
}

func (r *Registry) setPaused(name string, p bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if name == "all" {
		for n := range r.info {
			r.paused[n] = p
		}
	} else {
		r.paused[name] = p
	}
	for n := range r.info {
		if r.paused[n] && r.info[n].State == "idle" {
			r.info[n].State = "paused"
		} else if !r.paused[n] && r.info[n].State == "paused" {
			r.info[n].State = "idle"
		}
	}
}

func (r *Registry) isPaused(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.paused[name]
}

func (r *Registry) isRunning(name string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.cmds[name]; ok {
		return true
	}
	_, ok := r.adopted[name] // an adopted (orphaned-but-alive) worker counts as running
	return ok
}

// pidAlive reports whether a process is still running (signal 0 = existence check).
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return p.Signal(syscall.Signal(0)) == nil
}

// adoptOrphans (called once at startup): find fleet workers still running from a previous daemon and
// re-attach them, so a daemon restart never makes a busy agent look idle. It matches each task by the
// first line of its prompt file (passed verbatim as the `claude -p` arg, so it's visible in `ps`).
func adoptOrphans(cfg *Config, reg *Registry) {
	out := runc("ps -axww -o pid=,command=", 8*time.Second)
	if out == "" {
		return
	}
	lines := strings.Split(out, "\n")
	for name, t := range cfg.Jobs {
		sig := promptSig(cfg, t)
		if sig == "" {
			continue
		}
		for _, ln := range lines {
			if !strings.Contains(ln, "claude") || !strings.Contains(ln, sig) {
				continue
			}
			if pid := leadingInt(ln); pid > 0 && pidAlive(pid) {
				reg.adopt(name, pid)
				logf(cfg, "fleet", "adopted orphaned agent %s (pid %d) still running after restart", name, pid)
			}
			break
		}
	}
}

// promptSig = the first non-empty line of a task's prompt file (e.g. "# Job: visual-test (nightly)"),
// which appears verbatim in the worker's `claude -p` command — a reliable signature to match in `ps`.
func promptSig(cfg *Config, t Task) string {
	b, err := os.ReadFile(cfg.resolve(t.PromptFile))
	if err != nil {
		return ""
	}
	for _, ln := range strings.Split(string(b), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			if len(ln) > 60 {
				ln = ln[:60]
			}
			return ln
		}
	}
	return ""
}

func leadingInt(s string) int {
	s = strings.TrimSpace(s)
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	if i == 0 {
		return 0
	}
	n, _ := strconv.Atoi(s[:i])
	return n
}

// snapshot merges live state with each running agent's status file.
func (r *Registry) snapshot() []RunInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]RunInfo, 0, len(r.info))
	for name, ri := range r.info {
		cp := *ri
		if cp.State == "running" {
			if p, ok := readStatus(r.cfg, name); ok {
				cp.Progress = p
			}
		}
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// logTail returns the last n lines of an agent's log.
func logTail(cfg *Config, name string, n int) []string {
	b, err := os.ReadFile(filepath.Join(cfg.resolve(cfg.LogDir), name+".log"))
	if err != nil {
		return nil
	}
	lines := []string{}
	for _, ln := range splitLines(string(b)) {
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func splitLines(s string) []string {
	out := []string{}
	cur := ""
	for _, c := range s {
		if c == '\n' {
			out = append(out, cur)
			cur = ""
		} else {
			cur += string(c)
		}
	}
	out = append(out, cur)
	return out
}

func readStatus(cfg *Config, name string) (Progress, bool) {
	p := Progress{}
	b, err := os.ReadFile(filepath.Join(cfg.resolve(cfg.LogDir), "status", name+".json"))
	if err != nil {
		return p, false
	}
	if json.Unmarshal(b, &p) != nil {
		return p, false
	}
	return p, true
}

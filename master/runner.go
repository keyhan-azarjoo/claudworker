package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var resetTimeRe = regexp.MustCompile(`(?i)try again at\s+(\d{1,2}):(\d{2})\s*([AP]M)?`)

// untilResetTime parses a "try again at 3:56 PM" hint (codex/claude rate-limit messages) and
// returns the duration until that wall-clock time (next occurrence). 0 if not found / >24h.
func untilResetTime(out string) time.Duration {
	m := resetTimeRe.FindStringSubmatch(out)
	if m == nil {
		return 0
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	switch strings.ToUpper(m[3]) {
	case "PM":
		if h < 12 {
			h += 12
		}
	case "AM":
		if h == 12 {
			h = 0
		}
	}
	now := time.Now()
	target := time.Date(now.Year(), now.Month(), now.Day(), h, mi, 0, 0, now.Location())
	if !target.After(now) {
		target = target.Add(24 * time.Hour)
	}
	d := time.Until(target)
	if d <= 0 || d > 24*time.Hour {
		return 0
	}
	return d
}

type claudeResult struct {
	Result       string  `json:"result"`
	SessionID    string  `json:"session_id"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	IsError      bool    `json:"is_error"`
}

func loadSecretsEnv(path string) []string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	var env []string
	for k, v := range m {
		if s, ok := v.(string); ok {
			env = append(env, k+"="+s)
		}
	}
	return env
}

func readFile(path string) string {
	b, _ := os.ReadFile(path)
	return string(b)
}

// buildArgs assembles the claude CLI args for a prompt + tool set.
func buildArgs(cfg *Config, model, effort, prompt, tools string, maxTurns int) []string {
	role := readFile(cfg.resolve(cfg.PrivacyRoleFile))
	if maxTurns <= 0 {
		maxTurns = 40
	}
	if tools == "" {
		tools = "Read,Edit,Write,Bash"
	}
	if model == "" {
		model = cfg.Model
	}
	args := []string{
		"-p", prompt,
		"--output-format", "json",
		"--model", model,
		"--permission-mode", "acceptEdits",
		"--allowedTools", tools,
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--strict-mcp-config",
	}
	if cfg.FallbackModel != "" {
		args = append(args, "--fallback-model", cfg.FallbackModel)
	}
	if effort == "" {
		effort = cfg.Effort
	}
	if effort != "" {
		args = append(args, "--effort", effort)
	}
	if strings.TrimSpace(role) != "" {
		args = append(args, "--append-system-prompt", role)
	}
	return args
}

func expandHome(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// newWorkerCmd builds the exec.Cmd for a prompt on the given runner (nil = default claude login).
// Returns the cmd and the runner label (for logs).
func newWorkerCmd(cfg *Config, ctx context.Context, r *Account, model, prompt, tools string, maxTurns int, buf *bytes.Buffer) (*exec.Cmd, string) {
	env := append(os.Environ(), loadSecretsEnv(cfg.resolve(cfg.SecretsPath))...)
	if fo := acctFanout(cfg, r); fo > 0 { // sub-agents auto-derived from this account's speed
		env = append(env, fmt.Sprintf("MO_FANOUT=%d", fo))
	}
	var cmd *exec.Cmd
	label := "default-claude"

	if r != nil && r.Engine != "" && r.Engine != "claude" {
		// non-claude engine (e.g. codex): run its shell command with the prompt in env
		label = r.Label
		role := readFile(cfg.resolve(cfg.PrivacyRoleFile))
		cmd = exec.CommandContext(ctx, "bash", "-lc", r.Cmd)
		env = append(env, "WORKER_PROMPT="+role+"\n\n"+prompt, "WORKER_TOOLS="+tools)
		if r.ConfigDir != "" { // isolate this codex account's auth (CODEX_HOME)
			env = append(env, "CODEX_HOME="+expandHome(r.ConfigDir))
		}
		if survivalActive(cfg) { // small/cheap model + light reasoning to keep the lights on
			env = append(env, "SURVIVAL=1")
		}
	} else {
		// claude engine (optionally a specific account profile)
		eff := cfg.Effort
		if r != nil && r.Effort != "" {
			eff = r.Effort
		}
		cmd = exec.CommandContext(ctx, "claude", buildArgs(cfg, model, eff, prompt, tools, maxTurns)...)
		if r != nil {
			label = r.Label
			if r.ConfigDir != "" {
				env = append(env, "CLAUDE_CONFIG_DIR="+expandHome(r.ConfigDir))
			}
		}
	}
	cmd.Dir = cfg.resolve(cfg.ProjectDir)
	cmd.Env = env
	cmd.Stdout = buf
	cmd.Stderr = os.Stderr
	return cmd, label
}

// runTask executes one job/agent via the Claude CLI. If reg != nil it registers
// live state (running/finished, PID, cost) for the control panel and is stoppable.
func runTask(cfg *Config, name string, t Task, reg *Registry, acct *Account) error {
	prompt := readFile(cfg.resolve(t.PromptFile))
	if strings.TrimSpace(prompt) == "" {
		return fmt.Errorf("%s: empty/missing prompt %s", name, t.PromptFile)
	}

	ctx, cancel := context.WithTimeout(context.Background(),
		time.Duration(cfg.JobTimeoutMinutes)*time.Minute)
	defer cancel()

	model := t.Model
	if acct != nil && acct.UseSonnet { // this account is set to run on Sonnet (cheaper) regardless of the task's model
		model = "claude-sonnet-4-6"
	}
	var buf bytes.Buffer
	cmd, runnerLabel := newWorkerCmd(cfg, ctx, acct, model, prompt, t.AllowedTools, t.MaxTurns, &buf)
	cmd.Env = append(cmd.Env, "CW_AGENT="+name) // marker so a restarted daemon can re-adopt this worker

	if err := cmd.Start(); err != nil {
		logf(cfg, name, "START FAILED: %v", err)
		if reg != nil {
			reg.setRetry(name, "could not start — will retry")
		}
		return err
	}
	if reg != nil {
		reg.setRunning(name, cmd, cancel)
	}
	// Track this as a live worker on its account. Decremented when it finishes — so a paused
	// account (no NEW launches) still shows its in-flight agents draining to 0.
	acctLabel := runnerLabel
	if acct != nil {
		acctLabel = acct.Label
	}
	incAcctRunning(acctLabel)
	defer decAcctRunning(acctLabel)
	start := time.Now()
	logf(cfg, name, "START kind=%s runner=%s", t.Kind, runnerLabel)

	err := cmd.Wait()
	dur := time.Since(start).Round(time.Second)
	if err != nil {
		// Cool the runner down so the fleet stops routing to a dead account.
		if acct != nil {
			out := buf.String()
			low := strings.ToLower(out)
			cd := 5 * time.Minute
			if strings.Contains(low, "usage limit") || strings.Contains(low, "rate limit") || strings.Contains(low, "quota") {
				cd = 60 * time.Minute
				// codex/claude often say "try again at 3:56 PM" — cool down to that EXACT time so
				// the dashboard shows an accurate reset (real codex usage signal).
				if d := untilResetTime(out); d > 0 {
					cd = d
				}
			}
			setCooldown(acct.Label, cd)
		}
		// Not a real task error — the account/runner was unavailable. Retry on a working one;
		// keep the dashboard clean (no error state).
		logf(cfg, name, "retry: %s unavailable after %s (%v)", runnerLabel, dur, err)
		if reg != nil {
			reg.setRetry(name, "waiting for a free account ("+runnerLabel+" busy)")
		}
		return err
	}

	var r claudeResult
	msg := ""
	if json.Unmarshal(buf.Bytes(), &r) == nil {
		msg = oneLine(r.Result, 600)
		logf(cfg, name, "DONE %s cost=$%.4f session=%s", dur, r.TotalCostUSD, r.SessionID)
		logf(cfg, name, "RESULT: %s", msg)
	} else {
		msg = fmt.Sprintf("(unparsed output, %d bytes)", buf.Len())
		logf(cfg, name, "DONE %s %s", dur, msg)
	}
	if reg != nil {
		reg.setFinished(name, r.TotalCostUSD, msg, r.IsError)
	}
	appendHistory(cfg, HistRec{Time: time.Now().Format(time.RFC3339), Name: name, Runner: runnerLabel, Cost: r.TotalCostUSD, OK: !r.IsError, Msg: msg})
	return nil
}

// runChat runs a one-off instruction the owner typed to an agent in the control panel.
// chatRunner picks a CLAUDE account for the control-panel chat (codex can't run a claude session).
// Prefers an available, non-paused claude account; falls back to the Mac's default claude login.
func chatRunner(cfg *Config) *Account {
	for i := range cfg.Accounts {
		a := cfg.Accounts[i]
		if (a.Engine == "" || a.Engine == "claude") && !isAcctPaused(a.Label) && available(cfg, &a) {
			ac := a
			return &ac
		}
	}
	return nil // default keychain claude login
}

func runChat(cfg *Config, agentTask Task, message string) (string, error) {
	role := readFile(cfg.resolve(agentTask.PromptFile))
	prompt := "The owner sent you this instruction from the control panel. Do it now (or answer):\n\n" + message
	if role != "" {
		prompt = "Your role:\n" + role + "\n\n---\n" + prompt
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	var buf bytes.Buffer
	acct := chatRunner(cfg) // a CLAUDE account (the chat is a Claude session, not codex)
	cmd, _ := newWorkerCmd(cfg, ctx, acct, agentTask.Model, prompt, agentTask.AllowedTools, 30, &buf)
	if err := cmd.Run(); err != nil {
		// surface the tail of claude's output so the owner sees the real reason
		out := strings.TrimSpace(buf.String())
		if len(out) > 300 {
			out = out[len(out)-300:]
		}
		if out != "" {
			return "", fmt.Errorf("%v — %s", err, out)
		}
		return "", err
	}
	var r claudeResult
	if json.Unmarshal(buf.Bytes(), &r) == nil {
		return r.Result, nil
	}
	return buf.String(), nil
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}

func logf(cfg *Config, name, format string, a ...any) {
	line := fmt.Sprintf("%s [%s] %s", time.Now().Format("2006-01-02 15:04:05"), name, fmt.Sprintf(format, a...))
	fmt.Println(line)
	dir := cfg.resolve(cfg.LogDir)
	_ = os.MkdirAll(dir, 0o755)
	if f, err := os.OpenFile(filepath.Join(dir, name+".log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
		fmt.Fprintln(f, line)
		f.Close()
	}
}

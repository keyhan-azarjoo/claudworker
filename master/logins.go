package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Interactive account login driven from the dashboard:
//   start → spawn `claude auth login` / `codex login --device-auth`, capture the auth URL (+ device code)
//   finish → paste the code back into the process's stdin (Claude) so it completes — all on this Mac, no token shared.
type loginSess struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	mu       sync.Mutex
	buf      strings.Builder
	url      string
	code     string // codex device code (shown to user)
	finished bool
}

var (
	lsessMu sync.Mutex
	lsess   = map[string]*loginSess{}
	urlRe   = regexp.MustCompile(`https://[^\s]+`)
	codeRe  = regexp.MustCompile(`(?i)code[:\s]+([A-Z0-9][A-Z0-9-]{3,})`)
)

func loginStart(cfg *Config, label string) (string, string, error) {
	var acct *Account
	for i := range cfg.Accounts {
		if cfg.Accounts[i].Label == label {
			acct = &cfg.Accounts[i]
		}
	}
	if acct == nil {
		return "", "", fmt.Errorf("unknown account")
	}
	var cmd *exec.Cmd
	extra := []string{}
	if acct.Engine == "codex" {
		// Bare `codex login` reliably prints the OAuth URL (device-auth is often 429-rate-limited).
		// It runs a localhost callback server, so authorizing completes the login automatically.
		cmd = exec.Command("codex", "login")
		dir := acct.ConfigDir
		if dir == "" {
			dir = "~/.codex"
		}
		_ = os.MkdirAll(expandHome(dir), 0o700) // CODEX_HOME must exist or codex errors
		extra = append(extra, "CODEX_HOME="+expandHome(dir))
	} else {
		cmd = exec.Command("claude", "auth", "login", "--claudeai")
		if acct.ConfigDir != "" {
			_ = os.MkdirAll(expandHome(acct.ConfigDir), 0o700)
			extra = append(extra, "CLAUDE_CONFIG_DIR="+expandHome(acct.ConfigDir))
		}
	}
	cmd.Env = append(os.Environ(), extra...)
	stdin, _ := cmd.StdinPipe()
	r, w := io.Pipe()
	cmd.Stdout = w
	cmd.Stderr = w
	if err := cmd.Start(); err != nil {
		return "", "", err
	}
	s := &loginSess{cmd: cmd, stdin: stdin}
	go func() {
		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 1024*64), 1024*64)
		for sc.Scan() {
			ln := sc.Text()
			s.mu.Lock()
			s.buf.WriteString(ln + "\n")
			if s.url == "" {
				if m := urlRe.FindString(ln); strings.Contains(m, "oauth") || strings.Contains(m, "authorize") {
					s.url = m
				}
			}
			if s.code == "" {
				if m := codeRe.FindStringSubmatch(ln); m != nil {
					s.code = m[1]
				}
			}
			s.mu.Unlock()
		}
	}()
	go func() { cmd.Wait(); s.mu.Lock(); s.finished = true; s.mu.Unlock(); w.Close() }()
	go func() { // reaper: an ABANDONED interactive login (user never pastes the code) must not hang
		// forever — hung `claude auth login` procs pile up and eat memory/swap, which then blocks the
		// whole fleet via the resource gate. Kill it after 15 min if it hasn't completed.
		time.Sleep(15 * time.Minute)
		s.mu.Lock()
		fin := s.finished
		s.mu.Unlock()
		if !fin && s.cmd != nil && s.cmd.Process != nil {
			s.cmd.Process.Kill()
		}
	}()
	lsessMu.Lock()
	if old := lsess[label]; old != nil && old.cmd != nil && old.cmd.Process != nil {
		old.cmd.Process.Kill()
	}
	lsess[label] = s
	lsessMu.Unlock()
	for i := 0; i < 40; i++ { // wait up to ~8s for the URL
		s.mu.Lock()
		u, c := s.url, s.code
		s.mu.Unlock()
		if u != "" {
			return u, c, nil
		}
		time.Sleep(200 * time.Millisecond)
	}
	// No URL — surface the REAL reason (e.g. codex "429 Too Many Requests") instead of a generic msg.
	s.mu.Lock()
	out := strings.TrimSpace(s.buf.String())
	s.mu.Unlock()
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	if out == "" {
		out = "no output from the login CLI"
	}
	return "", "", fmt.Errorf("%s", out)
}

// loginFinish pastes the code (Claude) and reports the tail of output / login status.
func loginFinish(cfg *Config, label, code string) string {
	lsessMu.Lock()
	s := lsess[label]
	lsessMu.Unlock()
	if s == nil {
		return "no active login — click Login again"
	}
	if code != "" && s.stdin != nil {
		io.WriteString(s.stdin, strings.TrimSpace(code)+"\n")
	}
	for i := 0; i < 30; i++ { // wait up to ~9s for completion
		s.mu.Lock()
		f := s.finished
		s.mu.Unlock()
		if f {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	s.mu.Lock()
	out := s.buf.String()
	s.mu.Unlock()
	low := strings.ToLower(out)
	if strings.Contains(low, "logged in") || strings.Contains(low, "success") || strings.Contains(low, "authenticated") {
		return "✅ logged in"
	}
	if len(out) > 240 {
		out = out[len(out)-240:]
	}
	return strings.TrimSpace(out)
}

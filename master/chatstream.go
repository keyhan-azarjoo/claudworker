package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"time"
)

// runChatStream runs the main agent (Tgo) and STREAMS its output token-by-token to the browser as it
// thinks — exactly like Claude Code in the IDE. Uses claude's stream-json + partial messages, parses
// the text deltas, and forwards them over an SSE-style response. Multiple of these can run at once
// (the chat never locks), each on a Claude account.
func runChatStream(cfg *Config, message string, w http.ResponseWriter, flush func()) {
	role := readFile(cfg.resolve("prompts/main_agent.md"))
	prompt := message
	if role != "" {
		prompt = "Your role:\n" + role + "\n\n---\nThe owner is chatting with you from the dashboard. Do it now (or answer):\n\n" + message
	}
	acct := chatRunner(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
	defer cancel()
	args := []string{
		"-p", prompt,
		"--output-format", "stream-json", "--include-partial-messages", "--verbose",
		"--model", cfg.Model, "--permission-mode", "acceptEdits",
		"--allowedTools", "Read,Edit,Write,Bash,Grep,Glob,Agent",
		"--max-turns", "40", "--strict-mcp-config",
	}
	cmd := exec.CommandContext(ctx, "claude", args...)
	cmd.Env = append(os.Environ(), loadSecretsEnv(cfg.resolve(cfg.SecretsPath))...)
	if acct != nil && acct.ConfigDir != "" {
		cmd.Env = append(cmd.Env, "CLAUDE_CONFIG_DIR="+expandHome(acct.ConfigDir))
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		streamSend(w, flush, "(couldn't start Tgo)")
		streamDone(w, flush)
		return
	}
	if err := cmd.Start(); err != nil {
		streamSend(w, flush, "error: "+err.Error())
		streamDone(w, flush)
		return
	}
	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	sentAny := false
	var finalResult string
	for sc.Scan() {
		var ev map[string]any
		if json.Unmarshal(sc.Bytes(), &ev) != nil {
			continue
		}
		switch ev["type"] {
		case "stream_event":
			if e, ok := ev["event"].(map[string]any); ok && e["type"] == "content_block_delta" {
				if d, ok := e["delta"].(map[string]any); ok && d["type"] == "text_delta" {
					if txt, ok := d["text"].(string); ok && txt != "" {
						streamSend(w, flush, txt)
						sentAny = true
					}
				}
			}
		case "result":
			if rs, ok := ev["result"].(string); ok {
				finalResult = rs
			}
		}
	}
	_ = cmd.Wait()
	if !sentAny && finalResult != "" { // no partial deltas came through → send the final answer at once
		streamSend(w, flush, finalResult)
	}
	streamDone(w, flush)
}

// streamSend writes one text chunk as a JSON-encoded SSE data line (preserves newlines/quoting).
func streamSend(w http.ResponseWriter, flush func(), txt string) {
	b, _ := json.Marshal(txt)
	fmt.Fprintf(w, "data: %s\n\n", b)
	flush()
}

func streamDone(w http.ResponseWriter, flush func()) {
	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
}

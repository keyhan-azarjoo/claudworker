package main

import (
	"os/exec"
	"strings"
)

// execTerminal opens a new macOS Terminal window running cmdStr so the owner can complete an
// interactive (browser-based) Claude/Codex login for a specific account profile.
func execTerminal(cmdStr string) {
	esc := strings.ReplaceAll(cmdStr, `"`, `\"`)
	_ = exec.Command("osascript", "-e", `tell application "Terminal" to do script "`+esc+`"`,
		"-e", `tell application "Terminal" to activate`).Run()
}

// logoutCmd returns the logout command for an account (used by the dashboard logout button).
func logoutCmd(cfg *Config, label string) string {
	for _, a := range cfg.Accounts {
		if a.Label != label {
			continue
		}
		if a.Engine == "codex" {
			return "codex logout"
		}
		if a.ConfigDir != "" {
			return "CLAUDE_CONFIG_DIR=" + a.ConfigDir + " claude /logout"
		}
		return "claude /logout"
	}
	return ""
}

#!/usr/bin/env bash
# One-shot reminder: add the friend's 2nd account tomorrow. Sends a Telegram note, then self-disarms.
HERE="$(cd "$(dirname "$0")" && pwd)"
bash "$HERE/notify.sh" "Reminder: add your friend's 2nd account today (Claude + Codex). Log in via VS Code with CLAUDE_CONFIG_DIR=~/.claude-friend, then add it to config.json (claude pausePct 80, codex pausePct 70 + nightPausePct 95 + resetHour 8). Kept light so it never looks drained."
PL="$HOME/Library/LaunchAgents/com.claudworker.reminder-codex.plist"
launchctl unload "$PL" 2>/dev/null || true
rm -f "$PL"

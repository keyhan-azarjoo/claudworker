#!/usr/bin/env bash
# Send a Telegram message to the owner when an agent needs a human.
# Usage: scripts/notify.sh "qa: blocked — no real iPhone connected for the e2e run"
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
BRIDGE="${MYOTGO_BRIDGE:-$HERE/../../Telegram/claude_telegram_bridge}"
MSG="${*:-"(no message)"}"

python3 - "$BRIDGE" "$MSG" <<'PY'
import json, os, sys, urllib.request, urllib.parse, re
bridge, msg = sys.argv[1], sys.argv[2]
# Enforce brief + complete: collapse whitespace to one line, cap length.
msg = re.sub(r"\s+", " ", msg).strip()
if len(msg) > 280:
    msg = msg[:277].rstrip() + "…"
def load(name):
    try: return json.load(open(os.path.join(bridge, name)))
    except Exception: return {}
cfg, reports, topics = load("config.json"), load("reports.json"), load("topics.json")
token = reports.get("bot_token") or cfg.get("bot_token")
chat  = topics.get("chat_id")
if not chat:
    ac = cfg.get("allowed_chat_ids") or []
    chat = ac[0] if ac else None
if not token or not chat:
    print("notify.sh: telegram bot_token/chat_id not found in bridge config", file=sys.stderr)
    sys.exit(1)
# Route the agent's reports to their DEDICATED forum topic (created for the Claudworker agent).
fields = {"chat_id": chat, "text": "🤖 Claudworker agent: " + msg}
thread = topics.get("agent_reports") or topics.get("report")
if thread:
    fields["message_thread_id"] = thread
data = urllib.parse.urlencode(fields).encode()
url = f"https://api.telegram.org/bot{token}/sendMessage"
try:
    urllib.request.urlopen(urllib.request.Request(url, data=data), timeout=15)
    print("notify.sh: sent")
except Exception as e:
    print("notify.sh: send failed:", e, file=sys.stderr); sys.exit(1)
PY

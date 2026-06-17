#!/usr/bin/env bash
# Run ONE agent/job via the Claude CLI — no Go build needed.
# Usage: scripts/run-job.sh <task-name>        e.g. run-job.sh jira-poll  /  run-job.sh planner
#    or: scripts/run-job.sh <path-to-prompt.md>
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
NAME="${1:?usage: run-job.sh <task-name|prompt-file>}"
CFG="$ROOT/config.json"; [ -f "$CFG" ] || CFG="$ROOT/config.example.json"

exec python3 - "$CFG" "$ROOT" "$NAME" <<'PY'
import json, os, sys
cfg_path, root, name = sys.argv[1], sys.argv[2], sys.argv[3]
cfg = json.load(open(cfg_path))
model = cfg.get("model", "claude-opus-4-8")

if os.path.isfile(name):                       # a direct prompt-file path
    prompt_file, tools, maxt = name, "Read,Edit,Write,Bash", 60
else:                                          # a named task from config
    j = cfg.get("jobs", {}).get(name)
    if not j:
        sys.stderr.write(f"unknown task: {name}\n"); sys.exit(2)
    prompt_file = os.path.join(root, j["promptFile"])
    tools = j.get("allowedTools", "Read,Edit,Write,Bash")
    maxt = j.get("maxTurns", 60)

prompt = open(prompt_file).read()
role_path = os.path.join(root, cfg.get("privacyRoleFile", "prompts/privacy_role.md").lstrip("./"))
role = open(role_path).read() if os.path.isfile(role_path) else ""

# secrets → child env (safe: no shell quoting)
env = dict(os.environ)
sec_path = os.path.join(root, cfg.get("secretsPath", "../Telegram/claude_telegram_bridge/.secrets.json"))
sec_path = os.path.normpath(sec_path)
if os.path.isfile(sec_path):
    for k, v in json.load(open(sec_path)).items():
        if isinstance(v, str):
            env[k] = v

args = ["claude", "-p", prompt, "--output-format", "json", "--model", model,
        "--permission-mode", "acceptEdits", "--allowedTools", tools,
        "--max-turns", str(maxt), "--strict-mcp-config"]
if role.strip():
    args += ["--append-system-prompt", role]

os.chdir(os.path.normpath(os.path.join(root, cfg.get("projectDir", ".."))))
os.execvpe("claude", args, env)
PY

#!/usr/bin/env bash
# Install launchd agents for every SCHEDULED task (not manual). Local, private scheduling.
# Each agent runs:  claudworker -config <cfg> -run <name>
# Usage: scripts/install-launchd.sh [--uninstall]
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
ROOT="$(cd "$HERE/.." && pwd)"
CFG="$ROOT/config.json"; [ -f "$CFG" ] || CFG="$ROOT/config.example.json"
BIN="$ROOT/claudworker"
LA="$HOME/Library/LaunchAgents"
PREFIX="com.claudworker"

if [ "${1:-}" = "--uninstall" ]; then
  for p in "$LA/$PREFIX".*.plist; do
    [ -e "$p" ] || continue
    launchctl unload "$p" 2>/dev/null || true
    rm -f "$p"; echo "removed $(basename "$p")"
  done
  exit 0
fi

[ -x "$BIN" ] || { echo "Build the binary first:  (cd master && go build -o ../claudworker .)"; exit 1; }
mkdir -p "$LA"

python3 - "$CFG" "$ROOT" "$BIN" "$LA" "$PREFIX" <<'PY'
import json, os, sys
cfg_path, root, binp, la, prefix = sys.argv[1:6]
cfg = json.load(open(cfg_path))
plist_paths = []
for name, t in cfg.get("jobs", {}).items():
    if t.get("manual"):                       # manual tasks are never scheduled
        continue
    label = f"{prefix}.{name}"
    sched = ""
    if t.get("everyMinutes", 0) > 0:
        sched = f"<key>StartInterval</key><integer>{int(t['everyMinutes'])*60}</integer>"
    elif t.get("atHHMM"):
        hh, mm = t["atHHMM"].split(":")
        sched = ("<key>StartCalendarInterval</key><dict>"
                 f"<key>Hour</key><integer>{int(hh)}</integer>"
                 f"<key>Minute</key><integer>{int(mm)}</integer></dict>")
    else:
        continue
    plist = f"""<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
  <key>Label</key><string>{label}</string>
  <key>ProgramArguments</key><array>
    <string>{binp}</string><string>-config</string><string>{cfg_path}</string>
    <string>-run</string><string>{name}</string>
  </array>
  <key>WorkingDirectory</key><string>{root}</string>
  <key>EnvironmentVariables</key><dict>
    <key>PATH</key><string>/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin:/usr/sbin:/sbin</string>
  </dict>
  {sched}
  <key>StandardOutPath</key><string>{root}/logs/launchd.{name}.out</string>
  <key>StandardErrorPath</key><string>{root}/logs/launchd.{name}.err</string>
</dict></plist>
"""
    p = os.path.join(la, f"{label}.plist")
    open(p, "w").write(plist)
    plist_paths.append(p)
    print("wrote", p)
print("\nLoad them with:")
for p in plist_paths:
    print(f"  launchctl unload '{p}' 2>/dev/null; launchctl load '{p}'")
PY
echo "Done. (deploy is manual — intentionally NOT scheduled.)"

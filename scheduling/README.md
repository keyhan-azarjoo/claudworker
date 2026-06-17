# Scheduling (local, private)

Scheduling is done with **macOS launchd** — never cloud routines (those store prompts/results
server-side under the shared account). Two ways to schedule:

### A) Generate + install from config (recommended)
```bash
(cd master && go build -o ../claudworker .)   # build once
./scripts/install-launchd.sh                  # writes com.claudworker.*.plist per scheduled task
# then load each as printed, e.g.:
launchctl load ~/Library/LaunchAgents/com.claudworker.reliability.plist
./scripts/install-launchd.sh --uninstall      # remove them all
```
Each plist runs `claudworker -run <name>` at its interval/time. `manual` tasks (backend, app, firmware,
release, deploy, e2e, …) are intentionally **not** scheduled — you trigger those.

### B) One long-running daemon
```bash
./claudworker -master      # in-process scheduler; same schedules, one process
```

### Default schedule
| Task | When |
|---|---|
| `planner` (agent) | daily 00:30 |
| `e2e-test` is run by `qa` (agent) | daily 01:00 |
| `visual-test` | daily 02:00 |
| `hardware-test` | daily 03:30 |
| `uiux` (agent) | daily 05:00 |
| `reliability` (agent, sentry catch+clean) | every 120 min |
| `jira-poll` | every 120 min |
| everything else (backend/app/firmware/release/deploy/…) | manual / trigger |

Pause/resume the whole fleet any time: `./claudworker -pause` / `-resume`
(or `touch /tmp/claudworker.off` / `rm` it). The master also auto-pauses at ≥95% account usage.

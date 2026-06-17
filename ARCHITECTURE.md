# Architecture

```
                    ┌──────────────────────────────────────────────┐
   launchd (cron) ─▶│  claudworker (Go master)                      │
   or `-master`     │   • reads config.json (jobs, schedules, caps) │
                    │   • usage guard: pause ≥95%, resume <90%       │
                    │   • concurrency semaphore (maxConcurrent)      │
                    │   • loads secrets → env (never logged)         │
                    └───────────────┬──────────────────────────────-┘
                                    │ spawns N workers
        ┌───────────────┬──────────┼───────────────┬───────────────┐
        ▼               ▼          ▼                ▼               ▼
   claude -p        claude -p   claude -p        claude -p       …(cap)
   jobs/jira_poll   sentry      visual_test      e2e_test
        │
        ▼  each worker:
   --output-format json  --permission-mode acceptEdits
   --allowedTools <per-job>  --strict-mcp-config
   --append-system-prompt prompts/privacy_role.md
   → makes its OWN git worktree → does the work → updates Jira/Confluence
```

## Why this shape
- **The CLI does the work; Go only orchestrates.** There is no official Go SDK, and none is needed —
  the right pattern is exec'ing the `claude` binary and parsing its JSON. This mirrors your existing
  `go-claude-demo`, which already does streaming, session tracking, and usage-based pause/resume.
- **launchd schedules, not cloud routines.** Cloud `/schedule` runs on Anthropic servers and stores
  prompt+result under the (shared) account. launchd keeps everything local and private.
- **Jobs are prompt files**, so they're usable directly (`claude -p "$(cat jobs/x.md)"`) and the Go
  master is optional sugar (fleet management, scheduling, token guard).

## Data flow per worker
1. Master picks a due/triggered job, acquires a concurrency slot, checks the usage guard + pause file.
2. Loads `.secrets.json` into the child env (Atlassian, Sentry, device creds, etc.).
3. Execs `claude -p <prompt>` with the per-job allowed-tools and the privacy role appended.
4. The worker creates its own worktree, does the task, updates Jira/Confluence, commits on its branch.
5. Master parses the JSON result (`result`, `total_cost_usd`, `session_id`), logs it, frees the slot.

## Credentials & cost — the trade-off you must pick (config `maxConcurrent`)
You can have **two** of: _many concurrent agents_ · _free (no new spend)_ · _private on the shared account_.

| Option | Privacy | Cost | Concurrency |
|---|---|---|---|
| **Shared account, CLI-only + local** (default) | Work content private; usage visible to others | Free (flat shared sub) | **~3–8** before the shared rate limit throttles |
| **Your own Anthropic login** | Fully isolated | New flat subscription = new spend | Higher, your own limit |
| **Your own API key (ZDR-capable)** | Best (zero-retention possible) | **Metered, pay-per-token** | 10–50, but you pay for every token |

Running **10–50 concurrent Opus workers** realistically requires the **API-key** path (metered spend),
which conflicts with the "never spend money" rule — or it will hard-throttle on a shared subscription
and spike usage the other account users will notice. Default `maxConcurrent` is conservative; raise it
only once you've chosen a credential/cost posture.

## Usage guard
The master reads the Claude Code credential from the macOS keychain
(`security find-generic-password -s "Claude Code-credentials" -w`) and queries the usage endpoint to
get the current plan %. ≥`usagePausePct` → stop spawning; <`usageResumePct` → resume. If it can't read
usage, it does **not** block (fails open). Align the exact parsing with `go-claude-demo` if it drifts.
```

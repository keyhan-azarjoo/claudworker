# Claudworker — a local AI agent fleet for *your* project

A **local fleet of AI CLI workers** (Claude Code by default; Codex / Gemini / any CLI too) that test,
maintain, and advance **your** software project — managed by a small Go **master** orchestrator with a
**live web dashboard**, scheduled locally, and kept **private on your machine**.

Point it at any repo, connect your own **Jira / Confluence / Sentry / GitHub / Telegram** and backend
URLs from the dashboard, and it runs a team of agents (planner, engineers, QA, reviewers, reliability…)
on a schedule you control.

> 🆕 **Works with any project.** Nothing is hardcoded — set your project dir in `config.json` and
> connect your accounts on the dashboard (**Accounts → Connect your accounts**). See **[SETUP.md](SETUP.md)**.

## 🔒 Privacy posture (read this first)

This fleet runs an **AI CLI** locally. Important truths:

- Claude has **no local model** — your prompts/code **are sent to the model provider** to get responses.
  "Local" here means the *orchestration, files, secrets, and session transcripts* stay on your machine,
  **not** that zero bytes leave it. (For true zero-egress you'd need a local model.)
- Your **CLI session transcripts live on disk** (e.g. `~/.claude/`) — they are **not** in the provider's
  web history and are **not** visible to others who share the account.
- It is **CLI-only by design**: it never uses a provider web app or cloud schedules (those store
  prompts/results server-side). Scheduling is done **locally**.
- **Secrets never get committed.** Tokens live in a gitignored `secrets.json` vault written by the
  dashboard; this repo ships only `secrets.example.json` (empty) and `config.example.json`.

## Quick start

```bash
git clone https://github.com/keyhan-azarjoo/claudworker.git
cd claudworker
cd master && go build -o ../claudworker . && cd ..
./claudworker -serve            # dashboard on http://127.0.0.1:8787 (config.json auto-created on first run)
```

Open the dashboard → **Accounts** → **Connect your accounts** → paste your Jira / Sentry / GitHub /
Telegram tokens and your backend URLs, hit **test connection**, done. Full walkthrough in **[SETUP.md](SETUP.md)**.

## Live control panel
`./claudworker -serve` serves a local dashboard at **http://127.0.0.1:8787** (optionally exposed privately
via `cloudflare/` — Tunnel + Access, no ports opened). It shows every agent live with a **progress bar
(% + one-line done/left + green ✓)**, **Run / Pause / Resume / Stop** per agent, editable schedules, a
**"Needs you / blockers"** list, a **chat box to tell any agent what to do**, usage + activity charts, and
the **Connect your accounts** page for credentials.

## Capacity & automation (always-on)
- **Multi-account / multi-engine:** the master round-robins workers across every runner in `config.json`
  (multiple Claude profiles + Codex/Gemini/any CLI). Log in each profile **once**
  (`CLAUDE_CONFIG_DIR=~/.claude-acct2 claude /login`) and it's automatic thereafter — or add accounts from
  the dashboard.
- **Never waits on you:** if a login expires, in-flight workers may error but the daemon keeps running and
  retries next cycle. `fallbackModel` degrades instead of failing on overload.
- **Usage guard:** the master pauses near your usage cap and resumes when it drops, so it won't blow a
  shared rate limit. Per-account caps/pace/effort are editable on the dashboard.

## What's here

```
claudworker/
├── README.md            ← you are here
├── SETUP.md             ← step-by-step: run it on your own project
├── ARCHITECTURE.md      ← design, data flow, credentials & the cost/concurrency trade-off
├── config.example.json  ← auto-copied to config.json on first run (concurrency, schedules, models, monitors)
├── secrets.example.json ← documents the credential keys (real values go in gitignored secrets.json)
├── prompts/
│   └── privacy_role.md  ← appended as the system prompt to EVERY worker
├── agents/              ← the TEAM: role-agents (planner · engineers · qa · code-review · security-review · reliability …)
├── jobs/                ← the job catalog — each file is a ready-to-run prompt (jira-poll, sentry-catch, e2e, …)
├── master/              ← the Go orchestrator + web server (config.go · server.go · integrations.go · runner.go …)
├── web/                 ← the dashboard (single-page UI)
├── scheduling/          ← local cron (launchd) plists + installer
└── scripts/             ← run-one-job, installers, helpers
```

> **Adapt the prompts.** The files under `agents/` and `jobs/` are **examples** describing one product's
> conventions. Edit them to describe *your* repo, branch workflow, review gates and definition of done.

## CLI

```bash
./claudworker -serve                  # daemon + dashboard (recommended)
./claudworker -list                   # show configured agents/jobs
./claudworker -run jira-poll          # run one agent/job once
./claudworker -all                    # run every non-manual task once (respects the concurrency cap)
./claudworker -master                 # scheduler only (no web)
./claudworker -pause / -resume        # pause / resume the whole fleet
```

## Guardrails baked in
- Every worker is told (via `prompts/privacy_role.md`) to work in its **own git worktree**, follow your
  repo's rules, claim the ticket, update Jira/Confluence, and never delete on its own initiative.
- The usage guard pauses the fleet near your cap.
- Deploy-type jobs are **manual/trigger only** — never on a timer.
- **Secrets are never committed** — `secrets.json`, `config.json`, `users.json` and `state/` are gitignored.

## License
MIT — see [LICENSE](LICENSE).

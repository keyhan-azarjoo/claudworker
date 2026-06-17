# Setup — run Claudworker on your own project

This guide gets the agent fleet running against **any** repository in a few minutes. Everything stays
local; no secret ever leaves your machine or gets committed.

## 1. Prerequisites

- **Go 1.21+** (to build the master) — `brew install go` on macOS.
- **An AI CLI logged in.** The default engine is the **Claude CLI** (`claude`). Install it and run
  `claude /login` once. (Codex / Gemini / any other CLI also work — see step 5.)
- macOS or Linux. The dashboard is a local web app on `127.0.0.1`.

## 2. Clone & build

```bash
git clone https://github.com/keyhan-azarjoo/claudworker.git
cd claudworker
cd master && go build -o ../claudworker . && cd ..
```

## 3. Point it at your project

Open `config.json` (auto-created from `config.example.json` on first run) and set:

```jsonc
{
  "secretsPath": "secrets.json",   // local vault, gitignored — leave as-is
  "projectDir":  "/path/to/your/repo",  // the repo the agents work in
  "maxConcurrent": 5,              // how many workers run at once
  ...
}
```

`projectDir` is the only thing you *must* set. Concurrency, models, schedules and the prompt set can stay
at their defaults and be tuned later.

## 4. Run the dashboard & connect your accounts

```bash
./claudworker -serve
# → control panel on http://127.0.0.1:8787
# → it prints a PANEL TOKEN on first run; the page also accepts admin / admin (change it under Users)
```

In the dashboard go to **Accounts → Connect your accounts** and fill the cards for the services you use:

| Service        | What you need                                              | Where to get it |
|----------------|------------------------------------------------------------|-----------------|
| **Jira**       | site (`you.atlassian.net`), email, API token, project key  | id.atlassian.com → Security → API tokens |
| **Confluence** | a space key (reuses the Jira Atlassian login)              | same Atlassian token |
| **Sentry**     | org slug, auth token (`project:read`, `org:read`)          | Sentry → Settings → Auth Tokens |
| **GitHub**     | a personal access token (repo scope)                       | github.com → Settings → Developer settings → Tokens |
| **Telegram**   | bot token + chat id (optional status pings)                | @BotFather |
| **Backend**    | your API/server URLs to health-check                       | the textarea at the bottom |

Hit **save**, then **test connection** — a green ✓ means the credentials work. Everything is written to
the local, gitignored `secrets.json`.

## 5. Add more accounts / engines (optional)

On the **Accounts** page → **Add account**: name it and pick an engine.
- **Claude** — log in via the card (URL or Terminal); each account is an isolated CLI profile.
- **Codex / Gemini / other** — pick "other" and give a run command; the task arrives in `$WORKER_PROMPT`.

## 6. Run agents

- From the dashboard: **Run / Pause / Stop** per agent, edit schedules, or use the chat box to tell an
  agent what to do.
- From the CLI: `./claudworker -run jira-poll`, `./claudworker -all`, or `./claudworker -master` (daemon).
- To schedule locally: `scripts/install-launchd.sh` (macOS launchd).

## 7. Make the agents yours

The files under `agents/` and `jobs/` are **example prompts** describing one product's workflow. Edit them
to match **your** repo: branch strategy, review gates, test commands, definition of done. The master just
runs whatever each prompt file says, in `projectDir`, as the engine you configured.

---

**Safety defaults:** `secrets.json`, `config.json`, `users.json`, `state/` and `logs/` are all gitignored.
The fleet works in its own git worktrees and is told never to delete on its own initiative. Review the
prompts before pointing it at a production repo.

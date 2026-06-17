# Agent: Backend / Platform Engineer  ("backend")

You own the backend + platform: `Backend/DotNet-WebApi-Backend`, `infra` (coturn/emqx/pki), the
Matter Bridge, MQTT/SignalR, DB/Atlas, auth, payments backend.
You ALSO own the **agent control-panel Go master** (the `claudworker_claudworker` repo, `master/*.go`) — build
its v2 server-side (Jira **PROJ-123**: V2c per-account scheduling + hard-pause/notify checkbox + start/
stop/default + per-account speed, V2d Codex usage + weekly limits, V2f token-grouping engine that merges
overlapping tickets/jobs into one run). Work in a worktree of `claudworker_claudworker`. Reports via code, not LLM.

Each run:
1. Pick your top ticket: Jira `project=SCRUM AND statusCategory!=Done AND (labels in (backend,iot,migration,mqtt,security,gdpr,payments) OR component backend) ORDER BY priority DESC`, skipping
   `wait-for-human-test`/`held`/`ns-later`/`iran-deferred`. Prefer North-Star NS items needing backend
   (e.g. NS1 local-first transport, NS3 GDPR data export/delete, NS5 Stripe £).
2. Claim it, branch+worktree, implement with root-cause rigour. Honour the Design Gate (no core feature
   requires a third-party cloud or a subscription; per-user data isolation; UK-GDPR export/delete).
3. Test against staging. Spawn sub-agents for parallel sub-tasks (e.g. migration + tests).
4. Merge to `development`, delete your branch, update Jira + Confluence. Do NOT deploy (hand to `release`).
5. Coordinate via Jira: if the app/firmware side needs a change, file/label a ticket for that role.

Escalate (Telegram) on: prod data risk, secret rotation, anything needing the owner's go-ahead.


## Tests are part of "done" — zero-token CI (added 2026-06-14)
For ANY new or changed code, ADD or extend the matching automated tests in the SAME change:
unit + integration (widget tests for Flutter UI, `dotnet test` for backend, `pytest` for Python, `go test`
for Go). These run with NO agents/tokens via each project's own tooling. Before you hand off, run from the
repo root:  `python3 scripts/ci_tests.py <project>`  (e.g. app · hub · backend · support-ai · marketing ·
mcp · fleet) and make sure it is GREEN. The same runner runs after every build/deploy and AUTO-FILES a
Highest-priority Jira bug (label `test-failure`) the moment a check breaks — never hand off red, and never
delete/skip a test just to make it pass.

**Coverage is ratcheted (added 2026-06-14):** run `python3 scripts/ci_tests.py <project> --coverage`.
Your change must NOT lower coverage — a drop auto-files a Highest-priority Jira bug you must fix. Add
unit + integration tests for EVERY function / endpoint / button / branch you add or touch; aim to raise
coverage toward 100%. Do not delete or weaken existing tests to pass.

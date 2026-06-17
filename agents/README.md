# The Claudworker Agent Team

12 role-agents that behave like a real engineering team. Each runs as `claude -p agents/<role>.md`
(via the master), can **spawn sub-agents** to parallelise, works on its **own branch → merges → deletes
it**, and **coordinates only through Jira + Confluence** (the shared bus). Common rules live in
`../prompts/privacy_role.md` (the Charter), appended to every agent.

## Org chart
```
              ┌──────────────────────────────┐
              │  Master (claudworker, Go)     │  schedules + concurrency + usage guard
              └───────────────┬──────────────-┘
   inbox (Telegram→Jira) ─────┤  planner (lead: plans + prioritises the board)
                              │ runs role-agents (each spawns sub-agents)
  ┌─────────┬─────────┬───────┼────────┬────────┬───────────┬─────────────┬──────────┐
  ▼         ▼         ▼       ▼        ▼        ▼           ▼             ▼          ▼
backend    app    firmware   qa     uiux   code-review  security-review reliability release
engineer engineer /hardware /test designer  (gate)        (gate)        /sentry    /devops
                         engineers → ready-for-review → BOTH gates approve → merge → release
```

## Roster
| File | Role | Owns | Default cadence |
|---|---|---|---|
| `inbox_telegram.md` | Inbox / Idea Catcher | owner's Telegram thoughts → Jira tickets | every ~20m |
| `planner.md` | Planning / Product Lead | roadmap, future plan, ticket grooming + priority (North-Star) | daily |
| `backend_engineer.md` | Backend / Platform | API, infra, MQTT/SignalR, DB | on Jira (backend) |
| `app_engineer.md` | App / Frontend | Flutter app + hub UI features | on Jira (app/hub) |
| `firmware_hardware_engineer.md` | Firmware / Hardware | ESP32 firmware, PCB, 3D, UK compliance | on Jira (firmware/hardware) |
| `qa_test_engineer.md` | QA / Test | app-map, scenario-gen, e2e, visual, hardware tests | nightly |
| `uiux_designer.md` | AI UI/UX Designer | rendered-screen review, design-system adherence, UX proposals | on UI change / nightly |
| `code_reviewer.md` | Code Reviewer | reviews every diff before merge (gate: `code-approved`) | on `ready-for-review` |
| `security_reviewer.md` | Security Reviewer | security review before merge (gate: `security-approved`) | on `ready-for-review` |
| `reliability_sentry.md` | Reliability | Sentry catch + clean → Jira; health | every 2h |
| `multimedia.md` | Multimedia / Marketing | daily content/SEO/social — fact-checked, real-screenshot visuals, voiceover, **publishes** | daily |
| `release_devops.md` | Release / DevOps | push to git + server + TestFlight + downloads page (incl. both Pis) | manual/trigger |

**Merge gate:** an engineer labels the ticket `ready-for-review`; the work merges to `development` only
after BOTH `code-approved` and `security-approved`. Then `release` pushes it to git, the server,
TestFlight, both Pis, and the website downloads page.

## How they talk
- **Jira** = working memory + messaging: claim, comment progress, hand off by comment+label/assignee.
- **Confluence** = shared brain: the **Agent Team Board** page is the standup/status hub; each role keeps
  its area page current.
- **Telegram** (`scripts/notify.sh`) = escalation to the human only (blocked / needs decision / hardware).

A role-agent that needs another role's help does NOT call it directly — it files/updates a Jira ticket
and labels it for that role. The other role picks it up on its next run. The **planner** keeps the whole
board ordered so the right work surfaces first.

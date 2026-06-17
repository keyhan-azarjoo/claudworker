# Job: sentry-catch  (every 2 hours)

Turn new Sentry errors into Jira bug tasks. **This feeds the team's #0 priority (charter): Sentry
fixes ship before everything else, to BOTH staging and production.**

1. Query Sentry for **unresolved issues** across BOTH orgs — `claudworker` on sentry.io (`SENTRY_TOKEN`)
   and the fresh-quota `claudworker-0m` on sentry.io (`SENTRY_TOKEN_2_USER`); creds in `.secrets.json`. Mind
   the free-tier quota (one noisy project eats it — flag noisy projects so we silence them at the source).
2. For each issue: capture title, culprit, count, first/last seen, a representative stack frame, and the
   permalink.
3. **Dedupe against existing Jira tasks** (search `project=SCRUM AND labels=sentry` and match by the
   Sentry issue id stored in the description). Do NOT create duplicates.
4. For genuinely new issues, create a Jira **Task** at **Highest priority**: summary `[Sentry] <title>`,
   labels `sentry` + `ready-for-deploy` + the platform label (app/hub/backend/web), description containing
   the Sentry permalink + `sentry-issue-id: <id>` + the stack excerpt. Highest priority so the engineer
   picks it up before any other ticket.
5. Comment on the Jira task: root-cause hint, the area engineer to fix it, and a reminder that the fix
   must **deploy to BOTH staging and production** and then **resolve** the Sentry issue.

Output: counts — new issues, tasks created (with keys), duplicates skipped, and any **noisy projects**
to silence at the source.

Note: this job is **read + Jira-write only** (it routes; the engineers fix + deploy per the charter #0 rule).

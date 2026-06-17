# Job: sentry-clean  (every 2 hours)

Keep Sentry and its Jira mirror tidy. **Owner rule: a FIXED error must not linger in Sentry — clear it
(resolve) the moment its fix ships, so the active error list only ever shows real, open problems.**

1. Query Sentry (BOTH orgs: `claudworker` on sentry.io + fresh `claudworker-0m` on sentry.io) for issues whose
   linked fix has SHIPPED, plus any already resolved/ignored or not recurred since their fix shipped.
2. For each, find the matching Jira task (`labels=sentry` + `sentry-issue-id`). If the underlying Jira
   task is **Done/merged** and the Sentry issue hasn't recurred, **resolve the Sentry issue** and close
   the Jira task (transition to Done) with a comment linking the resolution.
3. **Dedupe**: if several Sentry issues map to the same root cause / same Jira task, merge/mark the
   duplicates in Sentry and keep one canonical Jira task.
4. Flag (do not auto-close) any noisy project that is burning the free-tier quota, so the owner can mute it.

Safety: **never delete** Sentry projects or Jira issues — only resolve/close/merge, and only when the
fix is confirmed shipped. When unsure, leave it open and report.

Output: what was resolved/closed/merged, and anything flagged for the owner.

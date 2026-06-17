# Job: jira-poll  (every 2 hours)

Advance the Claudworker backlog, following the **North-Star priority order**.

1. Fetch open issues from Jira via REST (Basic auth `$ATLASSIAN_EMAIL:$ATLASSIAN_TOKEN` against
   `https://$ATLASSIAN_SITE`). JQL: `project=SCRUM AND statusCategory!=Done ORDER BY priority DESC, created ASC`.
2. Pick the **single highest-priority actionable ticket**. SKIP any labelled `wait-for-human-test`,
   `held`, `wait-for-hardware-test`, `ns-later`, `iran-deferred`, or already `In Progress` by someone else.
   Prefer the North-Star epic **PROJ-123** stories (NS1→NS5) first, then `north-star`/`ns-hero-related`.
3. Claim it: transition to **In Progress** and add a comment that the worker is taking it.
4. Read the full ticket (description + comments). Make your **own git worktree**
   (`scripts/new-agent-worktree.sh <repo-dir> agent/SCRUM-XXXX`) and do the work **only there**.
5. Follow `CLAUDE.md` + the North-Star Design Gate. Build and run the relevant tests.
6. When done: commit on your branch; update the Jira ticket (comment what changed + suggest the next
   transition) and the relevant Confluence page. **Do not deploy.** Do not merge to `development`
   unless the task is genuinely complete and verified.
7. If the task is blocked (needs a human, hardware, or a paid resource), write a clear hand-off comment
   and leave it for review — do not guess.

Output: which ticket you took, what you changed, what you verified, what remains.

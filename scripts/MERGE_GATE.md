# Mechanical merge gate (PROJ-123 / T9·T10·T11)

The team rule is "no branch merges to `development` without **both** `code-approved` **and**
`security-approved`." Until now that was convention-only — nothing actually stopped a merge. These three
scripts make it a hard, scriptable gate so reviewer and release agents cannot bypass it.

| Script | Does |
|---|---|
| `merge-gate.sh <KEY>` | Exit 0 **only** if the Jira ticket has both approvals and no blocking label; else exit 2 with the reason. `--quiet` (code only) / `--json` for scripting. |
| `safe-merge.sh <repo> agent/<branch> <KEY> [base]` | The only sanctioned merge path. Runs the gate, refuses on failure, then merges `agent/*`→`development` in a throwaway worktree (never disturbs the shared clone). `--cleanup` also removes your own merged worktree+branch. Never pushes, never deploys. |
| `merge-queue.sh` | Gate status of the whole In-Review queue, priority-ordered: mergeable-now / one-approval-away / needs-both / blocked. `--json` too. Answers "what's actually mergeable?" |

### Canonical label lists — ONE source of truth: `scripts/gate-labels.json`

Both the bash gate (`merge-gate.sh`, `merge-queue.sh`) **and** the Python release pipeline
(`scripts/release.py`, PROJ-123/T11) read the approval + blocking lists from the single file
`scripts/gate-labels.json`. Two tools that both perform merges must never disagree on what
"mergeable" means, so the list lives in exactly one place — edit it there, nowhere else. Each
script keeps a baked-in default equal to the file's contents purely as a fail-safe (if the file
is missing the gate still fails *closed*, blocking held/hardware-test tickets).

**Approvals required:** `code-approved`, `security-approved`.
**Blocking labels** (override approvals): `merge-blocked`, `changes-requested`, `needs-human`,
`blocked`, `do-not-merge`, `do-not-deploy`, `wait-for-human-test`, `wait-for-hardware-test`, `held`.

> Note the last three (`wait-for-human-test`, `wait-for-hardware-test`, `held`): a ticket parked
> for a human/bench test or explicitly held must **never** auto-merge even with both approvals.

Env: `ATLASSIAN_SITE`, `ATLASSIAN_EMAIL`, `ATLASSIAN_TOKEN` (from `.secrets.json`).

### Typical flow
```bash
scripts/merge-queue.sh                 # reviewers: pick the sharpest unblocks
scripts/merge-gate.sh PROJ-123       # confirm a ticket is mergeable
# release agent only, when approved + coordinated (no solo deploy):
scripts/safe-merge.sh --cleanup Backend/DotNet-WebApi-Backend agent/PROJ-123 PROJ-123
```

Deploy/push stays the release agent's separate, human-coordinated step — this gate governs **merge**, not
deploy. (Branch flow, "no solo deploys", "never delete except your own merged branch" all still apply.)

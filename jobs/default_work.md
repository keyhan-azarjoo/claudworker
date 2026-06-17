# Default work — what to do when your lane has NO queued ticket

You were launched on **idle-fill** (the speed is high/fast and the owner wants idle time used). There is no
ticket in your lane right now, so pick **one** genuinely useful, self-contained improvement in YOUR area,
**file a Jira ticket for it first** (so it's tracked + flows through review/test like any other work), do it
on a branch, and hand it off. Keep it small and shippable — one improvement per run, not a sprawling refactor.

Pick the highest-value item that applies to your area (top of list first):

1. **Groom the backlog (planner-style):** find the oldest/highest-priority To-Do in your area that's
   ready, clarify/scope it, and start it — real backlog work beats invented work. (Prefer this.)
2. **Raise test coverage:** add tests for an under-tested module you own (unit/widget/golden/e2e as fits).
3. **Clear a real TODO / FIXME / tech-debt** in your area's code — pick one, fix it properly, add a test.
4. **Fix a lint / analyzer / type warning** the CI tolerates but shouldn't.
5. **Small reliability/perf win** with a measurement (e.g. cut an obvious N+1, memoize a hot path) — prove it.
6. **Docs that are wrong or missing** for code you own (README/runbook/inline) — only if it's actually stale.
7. **Dependency hygiene:** a safe, non-breaking bump with the test suite green (never spend money).

Rules:
- **Never invent busywork.** If you genuinely can't find a real improvement, say so and exit — do NOT
  manufacture churn just to look busy. An honest "nothing useful to do right now" is correct.
- One ticket, one branch, real value, tests green, hand off for review. Follow all the standing rules
  (branch→development, bugs always first if any appear, never delete, commit author = keyhan-azarjoo only).
- This is gated to high/fast speed; at medium/below you won't be launched for default work.

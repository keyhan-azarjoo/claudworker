# Job: e2e-test  (nightly)  [Jira T3 / PROJ-123 — supersedes PROJ-123; CLOSE PROJ-123 when this is solid]

Run the generated scenarios as an **autonomous human-like end-to-end tester** on simulator AND real
hardware, capturing screenshots and verifying behind-the-scenes behaviour.

1. Load scenarios from `Claud_worker_agent/artifacts/scenarios/` (run `scenario-gen` first if empty).
   Pick a batch by priority (cover the North-Star hero-kit flows first), or the scenarios touched by
   tonight's changes.
2. For each scenario, on each target surface it names (iOS sim, Android sim, real iPhone, real Android,
   hub): drive the UI step-by-step like a user, **screenshot every page** into
   `logs/e2e/<date>/<scenario-id>/<surface>/`.
3. **Verify the action behind each button**, not just the tap: check the API/SignalR/MQTT call fired,
   the state changed, the device actually responded (e.g. the socket really toggled; the phone really
   used the hub's activated LLM). Assert the `expected` from the scenario.
4. Honour preconditions (login, ensure a device is present, set online/offline/local mode, biometric).
   **Login like a real human: if any surface shows the login screen (fresh, logged-out, or an
   EXPIRED session), sign in through the on-screen form using `$TEST_ACCOUNT_1_EMAIL` /
   `$TEST_ACCOUNT_1_PASSWORD` — these throwaway test creds are already in your env and authenticate
   on staging + prod. Never the owner's account; never print the password. Follow the exact steps in
   `jobs/_login_recipe.md`. Do NOT skip a run with "session expired / no plaintext pw" — re-login
   like a user instead.** Use staging for sims, prod creds for prod per the routing rule.
5. Record pass/fail per step with the screenshot evidence. Open a Jira bug (label `e2e`) for each real
   failure, attaching screenshots + the exact step.
6. Write a run report to `logs/e2e/<date>/REPORT.md` (totals, pass/fail, links).

When this job reliably covers the map end-to-end with evidence, **close Jira PROJ-123** with a comment
pointing to this job + the latest report.

Drive real devices via the saved recipes (WDA/Appium for iPhone, adb for Android). **Never build iOS
locally.** Honour the hardware coordination protocol. Output: the run report summary.

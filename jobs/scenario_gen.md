# Job: scenario-gen  (manual)  [Jira T2 / PROJ-123]

Turn the app functionality map into **exhaustive, concrete test scenarios** the E2E tester can run.

1. Read `Claud_worker_agent/artifacts/APP_FUNCTIONALITY_MAP.md`.
2. For **every page** and **every action** in the map, emit one or more scenarios. Cover:
   - Happy path for each button/action + its behind-the-scenes effect (assert the real result, not just
     that the tap registered).
   - Edge/negative cases: offline / local mode, no device present, permission denied, bad input, retry.
   - Cross-device flows (e.g. hub→phone local-LLM sharing on Android + iOS, real + sim; provisioning;
     calls; voice loop; payments).
   - The North-Star "pull-the-cable" checks where relevant (does it still work offline?).
3. Each scenario is a small structured file under `artifacts/scenarios/<surface>/<page>__<action>.md` with:
   `id`, `surface(s)`, `preconditions`, `steps` (numbered, UI-level), `expected` (including the
   behind-the-scenes assertion), `screenshots` (which screens to capture), `priority`.
4. Keep them deterministic and self-contained so a worker can pick one up cold.

Output: the populated `artifacts/scenarios/` tree + an index `artifacts/scenarios/INDEX.md` listing every
scenario id with its surface, page, and priority. Report total scenario count by surface.

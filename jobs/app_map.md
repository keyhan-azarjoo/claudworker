# Job: app-map  (manual — the foundation of the test system)  [Jira T1 / PROJ-123]

Produce a **complete, exhaustive map of the app + hub** — every page, every action, every button, and
the behaviour behind each — with **nothing missing**. This is the source of truth the scenario generator
and the E2E tester consume.

Scope (cover ALL):
- **Mobile app**: `User-Interface/Application/Flutter-IoT-MobileApp/claudworker/lib` — every screen under
  `features/**/presentation/screens`, every route, every tab, dialog, bottom-sheet, and setting.
- **Hub app**: `User-Interface/Application/Flutter-Hub/claudworker-hub/lib` — dashboard, settings, AI host,
  provider selection, wake word, etc.
- Apple Watch + CarPlay surfaces (list, lower depth).

For EACH page, record (1-by-1, do not summarise away):
1. **Page id + route + how you reach it** (nav path from app launch).
2. **Every interactive element** on it: buttons, toggles, fields, list actions, menus, gestures.
3. **What each action does behind the scenes**: the function/service it calls, the API/SignalR/MQTT
   call or local action, the state it changes, and the expected result + side effects.
4. **Preconditions** (login, a device present, online/offline/local mode, biometric).
5. **Cross-device / cross-surface flows** explicitly, e.g.:
   - Share the **local LLM from the hub to the phone** (Android + iOS, real + simulator): connect to the
     local hub, activate each different LLM on the hub, and verify the phone uses it end-to-end.
   - Offline/LAN local mode device control; provisioning; calls/chat; voice loop; payments/wallet.

How to work:
- Fan out reads across the feature directories; trace each screen's widgets → handlers → services.
- Be exhaustive and precise (file:line references). If a flow is ambiguous, note the open question.

Output: write `Claud_worker_agent/artifacts/APP_FUNCTIONALITY_MAP.md` — a structured, navigable map
(grouped by surface → page → actions → behind-the-scenes). This artifact is committed (no secrets).
When complete and reviewed, it unblocks `scenario-gen` and `e2e-test`.

# Job: hardware-test  (nightly)

Enumerate every device this Mac can reach, deploy the latest build, and test the parts touched by
recent changes — with **screenshots from both the simulator AND real hardware**.

> **Step 0 — run the deterministic plan first (saves tokens; PROJ-123 / T5):**
> `python3 scripts/hw_test_plan.py --write` reads the already-gathered device enumeration from the
> fleet snapshot and derives a change-driven, per-device test plan into `logs/hardware/<date>/plan.md`
> + `inventory.json`. It tells you, deterministically: which devices are connected, which areas changed
> in the window, and for each — the device/sim to run it on, the exact command, and whether it's
> `runnable` / `blocked-device-offline` / `bench-only`. **Read that plan and EXECUTE it** (deploy,
> exercise, screenshot) instead of re-deriving the inventory + "what changed" by hand. Only fall back to
> the manual discovery below if the script can't run. Its logic is unit-tested under
> `python3 scripts/ci_tests.py scripts` (zero-token).

1. **Discover connected/reachable devices:**
   - Android via `adb devices` (e.g. Galaxy A6 @192.168.1.100).
   - iOS: real iPhone (drive installed/TestFlight build — **never build iOS locally**, it hangs the Mac)
     + iOS simulators (17.5/18.6, not 26).
   - ESP32 boards on USB/serial (identify by MAC) and the Wokwi sims.
   - Raspberry Pi hubs: Pi 5 (192.168.1.100) and Hub Lite / Pi Zero 2 (192.168.1.100) over SSH.
   - Record what is actually present tonight (others may be unplugged) — see the DEVICE REGISTRY memory.
2. **Deploy the latest version** to each reachable target (app build to the phones/sims, firmware to the
   boards if flashing is safe and the board isn't fw-locked, latest hub build to BOTH Pis).
3. **Test the touched parts:** from recent git history / the recent Jira tickets, identify what changed,
   and exercise those flows on the matching device. Default to staging for sims/PCB, prod creds for prod.
   **If a device shows the login screen (incl. an EXPIRED session), sign in like a real human through
   the on-screen form using `$TEST_ACCOUNT_1_EMAIL` / `$TEST_ACCOUNT_1_PASSWORD` (already in your env;
   never the owner's account; never print the password) per `jobs/_login_recipe.md`. Never skip with
   "session expired / no plaintext pw".**
4. **Screenshots are mandatory** — capture each tested screen/flow on the simulator AND on the real
   device (use WDA for iPhone screenshots, adb for Android, render for hub). Save under `logs/hardware/<date>/`.
5. Watch Mac disk; don't fill it (the sim-janitor handles cleanup, but be considerate).
6. File any failure as a Jira bug (label `hardware`, attach the screenshots) and report a summary.

Honour the hardware coordination protocol (check→queue→claim→release) before using a shared device.

Output: device inventory, what was deployed, what was tested, pass/fail with screenshot paths.

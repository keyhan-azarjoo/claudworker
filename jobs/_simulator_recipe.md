# Recipe: use the DEVICE SIMULATOR (no real hardware needed)

There is a **functional device simulator** — `Hardware/Claudworker-Dummy-Devices`, a headless Python
service (no website — managed from the dashboard). It impersonates the **whole Claudworker device fleet** (temperature, smart plug,
camera, hub, flame/gas/smoke detectors, curtain/garage/window/door-rail/blind) over the **real protocol**
(REST registration + MQTT over mutual TLS, per-device cert) against **staging**, under the **throwaway test
account**. It is NOT AI — it's deterministic code. **Use it as your device source for testing instead of
waiting for physical hardware.**

## 1. Make sure it's running (functional — one call, no reasoning)
- Check the dashboard's `simulator` status (Devices → 🧪 Device Simulator panel shows running + device count).
- If it's stopped, start it: `POST http://127.0.0.1:8787/api/simulator/start` (the dashboard launches the
  process with the test account + `AUTO_START=true`, which registers the full fleet on staging). The owner
  can also Start/Stop it from **Devices → 🧪 Device Simulator**. Give it ~10–20s to register + connect.

## 2. Use the simulated devices for the test
- Log into the **app** (or hub) with the **same test account** the simulator uses (`$TEST_ACCOUNT_1_*`,
  see `jobs/_login_recipe.md`). The simulated devices now appear in the app exactly like real ones —
  on/off a plug, read temperature, drive a cover open/closed, trip a detector alarm, view the camera, etc.
- Send commands and assert the device reacts (covers accept `open`/`close`/`stop`/`setposition`; the plug
  toggles sockets; detectors can be made to alarm). Verify the round-trip (app → backend → device → back).

## 3. "Delete all devices and use the simulated fleet" (clean test set)
The simulated fleet is the **canonical test device set**, managed entirely from the dashboard — the
simulator runs **headless** (there is NO separate website). Reset the topology via the dashboard endpoints
(functional, no staging storm — they edit config, then ONE Apply restarts + registers):
- `POST /api/simulator/device {action:add|remove, group, type, name, sockets}`
- `POST /api/simulator/group {action:add|remove, name}` · `POST /api/simulator/transport {mode}`
- then `POST /api/simulator/apply` to register the new set on staging (one call — never restart per edit).
- In the **dashboard device list**, the simulator's devices are the ones to work with; ignore stale
  real-hardware rows that aren't connected. (Real devices auto-reappear when you actually plug them in.)
- Never point the simulator at the owner's real account or at prod — it's **test account + staging only**.
- ⚠️ Staging rate-limits logins (429): batch topology edits, Apply ONCE — don't restart in a tight loop.

## Notes
- You don't need the iPhone/A6/Pi connected for most device tests — the simulator covers them. Keep the
  hardware-test job for genuine on-metal checks; everything else can run against the simulator.
- File any bug you find as a Jira ticket yourself (you have Jira access) — never park "file a bug" on the owner.

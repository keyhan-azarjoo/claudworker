# Recipe: sign in like a real human (device-test login)

> 🎛️ **Devices have an account + an online/offline mode you can change** (the owner sets which account a
> device belongs to). **online** = reachable over the internet; **offline** = local network / simulator
> only. Test each device the way its mode says. You can flip a device yourself (functional, no form):
> `POST /api/devices/setattr {name, mode:"online"|"offline", account:"<email/label>"}` — handy to put a
> device into offline/local mode for a LAN test, or assign it to the test account before driving it.


> 🧪 **No real hardware? Use the DEVICE SIMULATOR** — see `jobs/_simulator_recipe.md`. It runs the whole
> device fleet on staging under this same test account, so after you log in the simulated devices appear in
> the app like real ones. Prefer it over waiting for a physical phone/Pi for device-behaviour tests.


**Use this whenever a device/sim shows the login screen** — fresh install, logged-out, or an
**expired session** (the app dropped to "Welcome back"). Do NOT skip the run with "session
expired / no password". You always have credentials: the fleet injects them into your env.

## Credentials (already in your environment — never the owner's account)
- `TEST_ACCOUNT_1_EMAIL` / `TEST_ACCOUNT_1_PASSWORD` — primary throwaway test user (Role=User,
  subscribed). `TEST_ACCOUNT_2_*` is a spare (e.g. multi-account / second-device tests).
- These are real, live accounts that authenticate against **both staging and prod** (verified
  2026-06-13). They are NOT the owner's personal login — using them is the intended path.
- `useCookies` routing: staging for sims/PCB, prod creds for prod, per the job's routing rule.
- **Never print the password** to logs/screenshots. Read it from `$TEST_ACCOUNT_1_PASSWORD`;
  redact it in any reported command.

## The rule
A real human does not abandon a test when the session expires — they type their email and
password into the form and sign in. **So do that.** Drive the on-screen login form like a user:
tap email → type `$TEST_ACCOUNT_1_EMAIL`, tap password → type the password, tap **Sign in**,
wait for `/dashboard`. Then continue the scenario. Capture a screenshot of the login screen and
the post-login dashboard as evidence.

## ⚠️ Why login "fails with wrong password" — and the fix
`adb shell input text "$PW"` **silently mangles the password**: a space becomes a literal `%s`, and
`@ & ( ) < > | ; $ ' "` get eaten or break the shell word-split. The test account password contains
`@` (e.g. `Myotgo@Test123`), so a naive `input text` types the *wrong* string → 401 → "wrong password,
retrying" forever. **Never type the password with a bare `input text`.** Use ONE of the robust methods
below, then VERIFY the auth call returned 200 before assuming it worked.

## Android (real device / emulator) — adb
```bash
EMAIL="$TEST_ACCOUNT_1_EMAIL"; PW="$TEST_ACCOUNT_1_PASSWORD"   # exact value, never guessed/typed by hand
# 1. Confirm we're on the login screen (don't log in if already authed):
adb -s "$SERIAL" exec-out uiautomator dump /dev/tty | grep -qi "Welcome back\|Sign in" && NEED_LOGIN=1
# 2. Tap the email field, then type. Email is safe-ish but still escape it:
type_safe() { local s="$1"; s="${s// /%s}"; printf '%s' "$s"; }   # spaces → %s for adb input text
adb -s "$SERIAL" shell input tap <email_x> <email_y>
adb -s "$SERIAL" shell input text "$(type_safe "$EMAIL")"
adb -s "$SERIAL" shell input tap <pw_x> <pw_y>
# 3. PASSWORD — robust method A (PREFERRED): set the clipboard to the EXACT value, then long-press→Paste.
#    This types ANY characters verbatim (no escaping problems):
adb -s "$SERIAL" shell cmd clipboard set-text "$PW" 2>/dev/null \
  && adb -s "$SERIAL" shell input keyevent 279                       # 279 = KEYCODE_PASTE
# 3b. Method B (fallback if clipboard cmd is unavailable): type each char so specials survive:
#     adb -s "$SERIAL" shell input text "$(type_safe "$PW")"   # only if PW has no @&()<>|;$'\"
adb -s "$SERIAL" shell input tap <signin_x> <signin_y>
# 4. VERIFY (don't trust the tap): poll until the dashboard nav appears AND assert POST /Token/Create==200.
#    If 401 with these known-good creds → it's a real backend/login bug, file it (don't loop retrying).
```
Prefer resource-ids from the dump over hard-coded coordinates; coordinates differ per device
(Galaxy A6 @ 192.168.1.100 vs emulator). Owner can view/edit these creds in the dashboard
**Devices → 🧪 Test login accounts** section.

## iOS (real iPhone via WDA / simulator)
- ⏰ **Real-iPhone TIME RULE:** only test the **real iPhone between 02:00 and 08:00** local (the owner
  connects the phone overnight then sleeps, so the fleet does all real-iPhone tests then). Outside that
  window, do NOT drive the real iPhone — use the iOS **simulator** or another surface instead. (The
  hardware-test gate already enforces this; honour it in e2e/visual/qa too.) The iOS **simulator** has no
  time restriction.
- Real iPhone: drive the **installed/TestFlight** build via WDA/Appium — **never build iOS
  locally** (it hangs the Mac). Use WDA element queries to find the email/password fields by
  accessibility id / label, `setValue` each, tap **Sign in**, wait for the dashboard.
- Simulator: same via WDA, or `xcrun simctl` + the accessibility tree. Screenshot both screens.

## Hub (Pi) 
The hub signs in once and mirrors the JWT to the embedded app ("Sign in on the hub first").
If the hub shows logged-out, sign in through the hub's own login UI with the same test account.

## After login
- Assert it really worked: the `POST /Token/Create` (or `/Token/Refresh`) call returned 200 and
  the app routed to `/dashboard` — not just that a button was tapped.
- If login genuinely fails (200 but app stuck, or 401 with known-good creds), that's a **real
  bug** — file it (label `e2e`/`hardware`) with screenshots; don't silently skip.

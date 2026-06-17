# Job: deploy  (Ops team — AUTO-deploys green work, then WATCHES it)

You ship the verified work yourself. **You do NOT ask the owner to deploy** — deploying (build, push,
release, update the Pis) is your job, not theirs. Prod has no live users yet, so deploying is safe; the
owner only hears from you if a real EXTERNAL CREDENTIAL you don't have is missing (then `owner-requests`).

## When you run, there is deploy work. Do it:
1. Figure out what's undeployed: tickets labelled `ready-to-deploy` (passed review + test), AND any repo
   whose `development` is ahead of `origin` (unpushed, undeployed commits — e.g. backend/app). **Bugs +
   security fixes first** (e.g. a CWE / stack-trace leak that's live on prod — ship that immediately).
2. Deploy it — yourself, end to end:
   - Push `development` → `origin` for the repos that are ahead (websites auto-deploy from GitHub).
   - Backend → build locally + deploy direct to the VPS Docker (your-server.example.com) per the runbook.
   - App → TestFlight (staging) / stores (prod) as appropriate.
   - **"Deploy" ALWAYS includes BOTH Raspberry Pi hubs** (Pi Zero 2 "Hub Lite" + Pi 5 hub). A `git pull`
     on the hub is **NOT a deploy** — the kiosk runs a *built bundle*, so a pull alone changes nothing
     (this exact bug left the hub on day-old code: repo at HEAD 06-13 but bundle from 06-12). For EACH Pi:
     `ssh claudworker@<pi>` → `git -C ~/Flutter-Hub pull` → rebuild the Flutter Linux app
     (`export PATH=$PATH:~/flutter/bin && cd ~/Flutter-Hub/claudworker-hub && flutter build linux --release`)
     → `sudo systemctl restart claudworker-kiosk.service` → **verify the bundle exe mtime is now newer than the
     repo HEAD commit time AND the kiosk ActiveEnterTimestamp is fresh** (else the hub is still stale).
     Pi 5 = `192.168.1.100`, Hub Lite = `192.168.1.100` (slower CPU — the build takes longer / may need
     a cross-built bundle copied over). Also restart `claudworker-hub-ai.service` if the relay/AI code changed.
3. Promote through the flow when green: `development → staging → main/prod`. No owner approval needed —
   the Reviewers + Testers already approved; you verify and ship.
4. **WATCH it after deploying** (the owner explicitly wants this): for a few minutes, confirm the
   services are healthy and the fix actually took — hit the live endpoints, check Sentry for new errors,
   and verify the specific success criterion on the ticket (e.g. `/Token/Refresh` returns 401, not a
   500 + stack trace). If something's wrong, roll forward with a fix or roll back, then re-verify.
   Also run the **zero-token CI** for what you shipped: `python3 scripts/ci_tests.py <project>` (e.g. app ·
   backend · website · support-ai · matter-bridge) — it re-confirms build/lint/unit/integration are green
   and AUTO-FILES a Highest-priority bug if not (no agents/tokens). Don't leave a deploy red.
5. Transition the deployed tickets to **Done**, drop the `ready-to-deploy` label, and update Jira +
   Confluence with what shipped where + the watch result.

## Hard rules
- Do it yourself; never park "please deploy" on the owner. Only `owner-requests` for a genuinely missing
  external token/access (a GoDaddy/X/registrar key, a portal authorization, money) the system can't get.
- Never spend money, never delete, never bypass the secret-scan hook for a real secret.

Output: what shipped (incl. both Pis), the post-deploy watch result (endpoints/Sentry/criteria), tickets closed.

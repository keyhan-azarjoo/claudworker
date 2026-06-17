# Job: visual-test  (nightly)

Screenshot **every page** of the app and hub on simulator + real hardware, AI-diff against the baseline,
and file visual regressions.

1. Read `Claud_worker_agent/artifacts/APP_FUNCTIONALITY_MAP.md` (produced by the `app-map` job) for the
   full list of pages/screens. If it's missing, run `app-map` first.
2. For each page, on each available surface (iOS sim, Android sim, real iPhone, real Android, hub):
   navigate to it and capture a screenshot into `logs/visual/<date>/<surface>/<page>.png`.
   - **If a surface shows the login / "Welcome back" screen, sign in first** like a real human with the
     throwaway test account — follow `jobs/_login_recipe.md` (read `$TEST_ACCOUNT_1_EMAIL` /
     `$TEST_ACCOUNT_1_PASSWORD`, use the clipboard-paste method so password specials aren't mangled).
     Never skip a page with "session expired"; never use the owner's real login.
3. Compare against the **baseline** in `artifacts/visual-baseline/<surface>/<page>.png`:
   - If no baseline exists, write the current shot as the new baseline and mark it `NEW`.
   - Else AI-diff: describe meaningful visual differences (layout break, missing element, overflow,
     wrong icon — remember the new design-system icons only) vs noise (timestamps, animations).
4. For each **meaningful regression**, open a Jira bug (label `visual`, attach before/after) and
   summarise. Pass the UI/UX designer-review bar.
5. Do not overwrite baselines automatically except for `NEW` pages — baseline updates are deliberate.
6. **Feed the multimedia gallery (token saver — agents help each other).** Whenever a page's screenshot
   is CLEAN and current (a good baseline — no regression, correct design-system icons, fully settled),
   save it to the shared gallery so the Multimedia agent reuses it WITHOUT re-navigating + re-screenshotting
   (which would burn tokens). Per good page, run:
   `python3 Claud_worker_agent/scripts/visual_gallery.py add <path/to/clean.png> "<Page name>" "<one-line description: what's on screen, the features/devices it shows, the vibe>" <surface>`
   Write a genuinely useful description — that's what Multimedia reads to choose images. Save only the BEST
   shot per page/surface (it replaces the prior one). Gallery = external SSD + a `manifest.json` table.

Output: pages captured per surface, regressions found (with Jira keys + screenshot paths), new baselines,
and how many clean screenshots were added to the multimedia gallery.

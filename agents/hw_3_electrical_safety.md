# Electrical Safety — sub-team (Stages 4 & 6)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 4 Current-Carrying, Stage 6 Safety.
**Stage 4 — gate:** `hw_validate.current_carrying_sweep` IPC-2221 across 1/2/5/10/13A with acceptable ΔT; `CURRENT_ANALYSIS.md`.
**Stage 6 — gate:** `fuse_rating` (fuse on LIVE; PE never switched; chain rated/labelled to socket — BS1363⇒13A implied weak-link), `mov_location` (MOV present + AFTER fuse), `creepage`/`clearance` re-asserted, `touch_safety` (no exposed live; non-isolated ⇒ enclosure is the user barrier; insulated plunger), `fire_safety` (production UL94 V-0); `SAFETY_REVIEW.md`.
**Honest verdict:** simulation validates protective FUNCTION only — never replaces physical UKCA/CE hipot/ground-bond/temp-rise. NEVER a false green light on mains; NEVER weaken a rule (document residual risk).
Reuse `hw_validate.py`; report via `hw_pipeline.py`.

# PCB Design — sub-team (Stages 2-3)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 2 Schematic, Stage 3 PCB Layout.
**Stage 2 — gate:** `hw_validate.sch_pcb_sync` (schematic net/ref set == laid-out PCB — drift is FAIL); `hw_validate.erc` 0 critical (rails, decoupling, relay drive, pull-ups/downs, fuse, MOV, TVS, reverse-polarity, ESD); `SCHEMATIC_REVIEW.md`.
**Stage 3 — gate:** `hw_validate.drc` 0 critical (no unconnected/shorts); `hw_validate.creepage` ≥3mm + `hw_validate.clearance` ≥2mm (mains L↔N); `hw_validate.rf_keepout` (antenna keepout + GND plane + RF clearance); `PCB_REVIEW.md`.
**Lessons:** mains layout is expert INTERACTIVE work — auto-route is proven NO-GO on mains; keep sch↔pcb synced always; scope creepage genuinely to Live↔Neutral. Use `pcb_to_stl.py` to render the populated board (exact components) on request.
**Rules:** never advance until checks PASS; never weaken a rule. Reuse `hw_validate.py`. Report via `hw_pipeline.py`.

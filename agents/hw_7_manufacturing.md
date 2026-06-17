# Manufacturing — sub-team (Stages 16-17)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 16 Manufacturing Review, Stage 17 Release Gate.
**Stage 16 — gate:** `hw_validate.dfm_check` (PCB DFM/DFA; injection-moulding draft/gates/ejection; assembly build-time/yield); `MANUFACTURING_REVIEW.md`.
**Stage 17 — gate:** `all_stages_pass` (every stage 0..16 PASS — computed by the runner) **AND** `hw_validate.release_gate` (self-gated FINAL_RELEASE builder returns READY). Only then emit `FINAL_RELEASE/` (CAD·STL·STEP·PCB·GERBER·BOM·CPL·Firmware·Documentation·Compliance·Manufacturing) + tag.
**Rules:** the builder is SELF-GATED — it refuses unless READY; never hand-generate FINAL_RELEASE to bypass a red gate. Reuse `hw_validate.py`; report via `hw_pipeline.py`.

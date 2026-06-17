# Mechanical — sub-team (Stages 7, 8, 10)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 7 Mechanical CAD, Stage 8 3D Print Validation, Stage 10 Prototype Build.
**Stage 7 — gate:** `MECHANICAL_REVIEW.md` (wall thickness, draft, radii/fillets); `hw_validate.fit` (PCB X/Y/Z + relay/cap/button clearance, fit_checks.json all true); `hw_validate.fcl_puzzle` (per-interface overlap≈0, gaps OK — run the python-fcl puzzle-fit test, write fcl_report.json).
**Stage 8 — gate:** `hw_validate.print_check` (every STL manifold/flat-bottom/plate-safe/no floating geometry); `hw_validate.snap_fit_hidden` (parts join via HIDDEN INTERNAL cantilever snap — no external clip; assembly closes); `PRINT_VALIDATION.md` + STL/.
**Stage 10 — gate:** physical assembly attestation `physical_signoff.json` (fits/closes/buttons work); `PROTOTYPE_BUILD.md`.
**Lessons:** grow the BOARD (square axis) before the box; geometry from board_features.json; verify on the OUTPUT, not assumptions; flat printable bottom + engraved branding; FCL software ≠ physical validation.
Reuse `hw_validate.py`; report via `hw_pipeline.py`.

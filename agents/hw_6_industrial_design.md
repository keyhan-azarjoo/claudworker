# Industrial Design — sub-team (Stage 9)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 9 Industrial Design. Benchmark = Tapo P110-class retail feel.
**Gate:** `hw_validate.id_consistency(spec)` against `hardware-framework/design-language/MYOTGO_DESIGN_LANGUAGE.md` (R10-R14 corners, front-upper recessed wordmark ≥16mm, top monogram, hidden tongue-and-groove seam, INTERNAL snap-fit only, dished soft-touch button, blind sealed LED pocket, no vents on mains); `hw_validate.id_score` ≥ **8.5/10** (appearance, brand consistency, premium feel); `INDUSTRIAL_DESIGN_REVIEW.md`.
**Assets:** reuse `design-language/assets/` branding STLs (recessed/embossed/colourfill wordmark + monogram).
**Rules:** purely visible-surface work — never change validated mechanical/electrical geometry to chase looks. Reuse `hw_validate.py`; report via `hw_pipeline.py`.

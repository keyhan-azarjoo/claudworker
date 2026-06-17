# Thermal — sub-team (Stage 5)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 5 Thermal.
**Gate:** `hw_validate.thermal` estimate (relay/fuse/MOV/caps/SMPS/MCU within rating; sealed-box Rθ≈25°C/W; relay dominant; 105°C limit) + review enclosure hotspots/heat-paths/air-volume; **physical temp-rise attestation** `thermal_signoff.json` (authoritative — estimate NEVER replaces it); `THERMAL_REVIEW.md`.
**Lessons:** latching dual-coil relay saves ~11°C in a sealed box; ≥5mm clearance around hot parts. Estimates are conservative and advisory only.
Reuse `hw_validate.py`; report via `hw_pipeline.py`.

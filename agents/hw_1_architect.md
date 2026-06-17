# Hardware Architect — sub-team (Stages 0-1)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 0 Requirements, Stage 1 Architecture.
**Stage 0 — gate:** produce `REQUIREMENTS.md` (dimensions, power, regulatory region UK/EU/US, BOM budget, mfg target, environmental) + `CONSTRAINTS.md`; run the North-Star 7-point **Design Gate** (`hw_validate.design_gate`).
**Stage 1 — gate:** `SYSTEM_ARCHITECTURE.md` (MCU, power arch, relay, sensor arch, OTA arch + enclosure/PCB-stack/button/LED concept) + `BLOCK_DIAGRAM.png`; `hw_validate.flash_headroom` <80%; `hw_validate.bom_sourcing` (≥2 sources + active).
**MCU/flash:** follow `MYOTGO_HARDWARE_STRATEGY.md` — C6 N8 standard for IoT/sensors; S3 N16R8 only for AI/voice/camera.
**Rules:** never advance until the stage's checks PASS; never weaken a safety rule (document risks). Reuse `hw_validate.py`. Report via `hw_pipeline.py` to the dashboard.

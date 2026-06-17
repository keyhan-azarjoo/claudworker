# Firmware — sub-team (Stage 11)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 11 Firmware.
**Gate:** `hw_validate.firmware_checklist` (OTA, recovery, watchdog, factory-reset, NVS, power-recovery all implemented) + boot attestation `firmware_signoff.json` (boots in Wokwi CI / on real board, CI marker reached, no crash); `FIRMWARE_REVIEW.md`.
**Tooling:** Wokwi CI runner `Arduino-ElectricalExtension/.../wokwi-ci-test.sh` (--expect-text boot marker, --fail-text 'Guru Meditation'); pio at ~/.platformio. Flash budget per `MYOTGO_HARDWARE_STRATEGY.md` (dual-OTA slot, <80%).
Reuse `hw_validate.py`; report via `hw_pipeline.py`.

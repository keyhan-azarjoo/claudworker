# Functional Test — sub-team (Stage 12)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 12 Functional Testing.
**Gate:** real-hardware attestation `functional_test.json` — WiFi, BLE, Matter, Thread, Zigbee connectivity + sensor accuracy/range/stability all pass; `FUNCTIONAL_TEST_REPORT.md`.
**Tooling:** Renode multi-device sim (radio nodes) for pre-bench logic; real device on the bench for the authoritative result. Enable only the radios/sensors the device category actually has.
Reuse `hw_validate.py`; report via `hw_pipeline.py`. BLOCKED until the bench result file exists — never fake a PASS.

# Reliability — sub-team (Stage 13)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 13 Reliability Testing.
**Gate:** attestation `reliability_report.json` — continuous 24h / 72h / 168h soak + relay cycling + button cycling, **no failures**; `RELIABILITY_REPORT.md`.
**Rules:** soak/cycle counts are real elapsed tests — BLOCKED until the report exists; a single failure = FAIL→STOP (root-cause, fix, re-run). MTBF/derating notes captured for the technical file.
Reuse `hw_validate.py`; report via `hw_pipeline.py`.

# Compliance / Lab — sub-team (Stages 14-15)
Part of the MANDATORY pipeline (`hardware-framework/PIPELINE.md`). **Owns:** Stage 14 UKCA/CE Preparation, Stage 15 Laboratory Testing.
**Stage 14 — gate:** `hw_validate.compliance_package` — technical file complete (BOM, schematics, PCB, risk assessment, labels, product markings, ratings, warnings); `COMPLIANCE_PACKAGE.md`.
**Stage 15 — gate:** lab-report attestation `lab_reports/index.json` — EMC (EN 55032 / EN 55035), Radio (RED), Safety (EN 62368-1), Environmental (RoHS / REACH) reports obtained; `LAB_REPORTS/`.
**Honest:** lab reports are EXTERNAL — software cannot fake them; stays BLOCKED until real reports are filed. UK market is the North-Star (BS1363 + UKCA/CE mandatory before sale).
Reuse `hw_validate.py`; report via `hw_pipeline.py`.

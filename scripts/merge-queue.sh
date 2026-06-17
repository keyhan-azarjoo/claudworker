#!/usr/bin/env bash
# merge-queue.sh — at-a-glance gate status of the whole In-Review queue.
#
# Answers the planner's recurring question ("0/25 mergeable — what's the state?")
# by running the merge gate over every non-Done ticket that is awaiting review and
# printing, in priority order: which are MERGEABLE now, which only need one more
# approval (the sharpest unblocks), and which are blocked. (PROJ-123 / T19 aid)
#
# Usage:
#   scripts/merge-queue.sh            # human summary
#   scripts/merge-queue.sh --json     # machine-readable array
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"
: "${ATLASSIAN_SITE:?}"; : "${ATLASSIAN_EMAIL:?}"; : "${ATLASSIAN_TOKEN:?}"
JSON=0; [ "${1:-}" = "--json" ] && JSON=1
GATE_LABELS="$HERE/gate-labels.json"   # canonical lists, shared with merge-gate.sh + release.py

JQL='project=SCRUM AND statusCategory!=Done AND (labels=ready-for-review OR status="In Review") ORDER BY priority DESC, created ASC'
raw="$(curl -s -G "https://$ATLASSIAN_SITE/rest/api/3/search/jql" \
  -u "$ATLASSIAN_EMAIL:$ATLASSIAN_TOKEN" \
  --data-urlencode "jql=$JQL" \
  --data-urlencode "maxResults=100" \
  --data-urlencode "fields=key,summary,priority,labels")"

JSON="$JSON" GATE_LABELS="$GATE_LABELS" python3 - "$raw" <<'PY'
import json, os, sys
raw = sys.argv[1]
as_json = os.environ["JSON"] == "1"

# --- canonical label lists (gate-labels.json — shared with merge-gate.sh + release.py) ---
# Baked-in default == the union shipped in gate-labels.json (fail-safe if missing).
_DEFAULT_REQUIRED = ["code-approved", "security-approved"]
_DEFAULT_BLOCKING = ["merge-blocked", "changes-requested", "needs-human", "blocked",
                     "do-not-merge", "do-not-deploy", "wait-for-human-test",
                     "wait-for-hardware-test", "held"]
try:
    with open(os.environ.get("GATE_LABELS", "")) as fh:
        _cfg = json.load(fh)
    REQUIRED = list(_cfg.get("required") or _DEFAULT_REQUIRED)
    BLOCKING = list(_cfg.get("blocking") or _DEFAULT_BLOCKING)
except Exception:
    REQUIRED, BLOCKING = list(_DEFAULT_REQUIRED), list(_DEFAULT_BLOCKING)

d = json.loads(raw)
issues = d.get("issues", [])
rows = []
for i in issues:
    f = i["fields"]
    labels = set(f.get("labels") or [])
    missing = [l for l in REQUIRED if l not in labels]
    blocks = [l for l in BLOCKING if l in labels]
    state = ("mergeable" if (not missing and not blocks)
             else "blocked" if blocks
             else "needs-one" if len(missing) == 1
             else "needs-both")
    rows.append({
        "key": i["key"], "summary": f.get("summary",""),
        "priority": (f.get("priority") or {}).get("name","?"),
        "state": state, "missing": missing, "blocks": blocks,
    })

if as_json:
    print(json.dumps(rows, indent=1)); sys.exit(0)

order = {"mergeable":0, "needs-one":1, "needs-both":2, "blocked":3}
rows.sort(key=lambda r: order[r["state"]])
buckets = {}
for r in rows: buckets.setdefault(r["state"], []).append(r)

n = len(rows)
nm = len(buckets.get("mergeable", []))
print(f"In-Review queue: {n} tickets — {nm} MERGEABLE now.\n")
titles = {
    "mergeable":  "✅ MERGEABLE NOW (both gates pass) — run safe-merge:",
    "needs-one":  "🟡 ONE approval away (sharpest unblocks) — review these first:",
    "needs-both": "⬜ Needs both code + security review:",
    "blocked":    "⛔ Blocked by label (resolve the blocker first):",
}
for st in ["mergeable","needs-one","needs-both","blocked"]:
    rs = buckets.get(st)
    if not rs: continue
    print(titles[st])
    for r in rs:
        extra = ""
        if r["state"] == "needs-one":   extra = " (needs " + r["missing"][0] + ")"
        elif r["state"] == "blocked":   extra = " (" + ",".join(r["blocks"]) + ")"
        print(f"  {r['key']:11} [{r['priority']:7}] {r['summary'][:52]}{extra}")
    print()
PY

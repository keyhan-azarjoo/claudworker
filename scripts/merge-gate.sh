#!/usr/bin/env bash
# merge-gate.sh — the MECHANICAL merge gate for the Claudworker agent team.
#
# WHY: until now the "no merge without code-approved + security-approved" rule
# was convention-only — nothing actually blocked a merge. This script turns the
# rule into a hard, scriptable check so the reviewer agents (T9 code-review,
# T10 security-review) and the release agent (T11) cannot bypass it. (PROJ-123)
#
# It inspects a Jira ticket's labels and EXITS 0 only when BOTH approvals are
# present and no blocking label is set. Any other state exits non-zero with a
# human-readable reason — so `merge-gate.sh KEY && git merge …` is safe.
#
# Usage:
#   scripts/merge-gate.sh PROJ-123            # check one ticket, print verdict
#   scripts/merge-gate.sh --quiet PROJ-123    # exit code only (for scripting)
#   scripts/merge-gate.sh --json PROJ-123     # machine-readable verdict
#
# Env (from .secrets.json / the agent env):
#   ATLASSIAN_SITE   e.g. yourcompany.atlassian.net
#   ATLASSIAN_EMAIL  Basic-auth user
#   ATLASSIAN_TOKEN  API token
#
# Exit codes: 0 = mergeable, 2 = not mergeable (missing/blocking), 3 = usage/auth error.
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"

QUIET=0; JSON=0; KEY=""
for a in "$@"; do
  case "$a" in
    --quiet|-q) QUIET=1 ;;
    --json)     JSON=1 ;;
    -h|--help)  grep '^#' "$0" | sed 's/^# \{0,1\}//'; exit 0 ;;
    SCRUM-*|*-*) KEY="$a" ;;
    *) echo "merge-gate: unknown arg '$a'" >&2; exit 3 ;;
  esac
done
[ -n "$KEY" ] || { echo "usage: merge-gate.sh [--quiet|--json] SCRUM-KEY" >&2; exit 3; }

: "${ATLASSIAN_SITE:?merge-gate: ATLASSIAN_SITE not set}"
: "${ATLASSIAN_EMAIL:?merge-gate: ATLASSIAN_EMAIL not set}"
: "${ATLASSIAN_TOKEN:?merge-gate: ATLASSIAN_TOKEN not set}"

# Required approvals and the blocking labels come from ONE canonical source —
# gate-labels.json — shared with merge-queue.sh AND the Python release pipeline
# (release.py, PROJ-123), so the two merge tools can never disagree on what
# "mergeable" means. Edit the list there, never inline here.
GATE_LABELS="$HERE/gate-labels.json"

raw="$(curl -s -G "https://$ATLASSIAN_SITE/rest/api/3/issue/$KEY" \
  -u "$ATLASSIAN_EMAIL:$ATLASSIAN_TOKEN" \
  --data-urlencode "fields=labels,status,summary")" || {
    echo "merge-gate: Jira request failed for $KEY" >&2; exit 3; }

QUIET="$QUIET" JSON="$JSON" KEY="$KEY" GATE_LABELS="$GATE_LABELS" \
python3 - "$raw" <<'PY'
import json, os, sys

# --- canonical label lists (gate-labels.json — shared with release.py) ---------
# Baked-in default == the union shipped in gate-labels.json, so the gate still
# fails safe (and still blocks held/hardware-test tickets) even if the file is
# missing. The file is the source of truth; this default must mirror it.
_DEFAULT_REQUIRED = ["code-approved", "security-approved"]
_DEFAULT_BLOCKING = ["merge-blocked", "changes-requested", "needs-human", "blocked",
                     "do-not-merge", "do-not-deploy", "wait-for-human-test",
                     "wait-for-hardware-test", "held"]
def load_gate_labels(path):
    try:
        with open(path) as fh:
            cfg = json.load(fh)
        req = cfg.get("required") or _DEFAULT_REQUIRED
        blk = cfg.get("blocking") or _DEFAULT_BLOCKING
        return list(req), list(blk)
    except Exception:
        return list(_DEFAULT_REQUIRED), list(_DEFAULT_BLOCKING)

raw = sys.argv[1]
key = os.environ["KEY"]
quiet = os.environ["QUIET"] == "1"
as_json = os.environ["JSON"] == "1"
required, blocking = load_gate_labels(os.environ.get("GATE_LABELS", ""))

try:
    d = json.loads(raw)
except Exception:
    print(f"merge-gate: could not parse Jira response for {key}", file=sys.stderr)
    sys.exit(3)
if "fields" not in d:
    errs = d.get("errorMessages") or [d.get("error", "unknown error")]
    print(f"merge-gate: Jira error for {key}: {errs}", file=sys.stderr)
    sys.exit(3)

f = d["fields"]
labels = set(f.get("labels") or [])
status = (f.get("status") or {}).get("name", "?")
summary = f.get("summary", "")

missing = [l for l in required if l not in labels]
present_blocks = [l for l in blocking if l in labels]
mergeable = (not missing) and (not present_blocks)

if as_json:
    print(json.dumps({
        "key": key, "mergeable": mergeable, "status": status,
        "missing_approvals": missing, "blocking_labels": present_blocks,
        "labels": sorted(labels),
    }))
elif not quiet:
    icon = "PASS" if mergeable else "FAIL"
    print(f"[{icon}] {key} [{status}] - {summary[:60]}")
    print("   approvals: " + ", ".join(
        (("[x] " + l) if l in labels else ("[ ] " + l)) for l in required))
    if present_blocks:
        print(f"   BLOCKED BY: {', '.join(present_blocks)}")
    if mergeable:
        print("   → MERGEABLE: both gates passed, no blocking label.")
    else:
        reasons = []
        if missing: reasons.append("missing " + "+".join(missing))
        if present_blocks: reasons.append("blocked by " + "+".join(present_blocks))
        print(f"   → NOT mergeable: {'; '.join(reasons)}.")

sys.exit(0 if mergeable else 2)
PY

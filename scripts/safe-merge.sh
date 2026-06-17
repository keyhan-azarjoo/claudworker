#!/usr/bin/env bash
# safe-merge.sh — gated merge of an agent task branch into `development`.
#
# The ONLY sanctioned way for an agent (reviewer / release / engineer doing the
# merge) to land a branch. It refuses to merge unless merge-gate.sh passes —
# i.e. the linked Jira ticket carries BOTH code-approved AND security-approved
# and no blocking label. This is the mechanical enforcement of the team rule
# "no merge to development without both gates" (PROJ-123 / T9, T10, T11).
#
# Usage:
#   scripts/safe-merge.sh <repo-dir> <agent-branch> <SCRUM-KEY> [base=development]
#   scripts/safe-merge.sh --cleanup <repo-dir> <agent-branch> <SCRUM-KEY> [base]
#
#   --cleanup : after a successful merge, remove the branch's worktree and delete
#               the merged agent/* branch (authorised only for YOUR OWN merged
#               task branch — never anyone else's).
#
# It will NOT deploy and NOT push — landing on development only. Deploy/push is
# the release agent's separate, human-coordinated step (no solo deploys).
set -euo pipefail
HERE="$(cd "$(dirname "$0")" && pwd)"

CLEANUP=0
if [ "${1:-}" = "--cleanup" ]; then CLEANUP=1; shift; fi

REPO="${1:?usage: safe-merge.sh [--cleanup] <repo-dir> <agent-branch> <SCRUM-KEY> [base]}"
BRANCH="${2:?missing <agent-branch>}"
KEY="${3:?missing <SCRUM-KEY>}"
BASE="${4:-development}"

# Resolve <repo-dir>: accept absolute / CWD-relative, else project-root-relative.
# Project root = grandparent of this script (repo/scripts/.. /.. == project root)
# when installed in the shared clone.
PROJECT_ROOT="$(cd "$HERE/../.." && pwd)"
if [ ! -e "$REPO/.git" ]; then
  if [ -e "$PROJECT_ROOT/$REPO/.git" ]; then REPO="$PROJECT_ROOT/$REPO"
  elif [ -e "$PWD/$REPO/.git" ]; then REPO="$PWD/$REPO"
  fi
fi
git -C "$REPO" rev-parse --git-dir >/dev/null 2>&1 || { echo "safe-merge: '$REPO' is not a git repo" >&2; exit 1; }
REPO="$(cd "$REPO" && pwd)"

case "$BRANCH" in
  agent/*) : ;;
  *) echo "safe-merge: refusing — '$BRANCH' is not an agent/* task branch" >&2; exit 1 ;;
esac

# 1) THE GATE. Hard stop on failure.
echo "── merge gate ──"
if ! "$HERE/merge-gate.sh" "$KEY"; then
  echo "safe-merge: GATE FAILED for $KEY — not merging $BRANCH. Get both code-approved + security-approved first." >&2
  exit 2
fi

# 2) Verify the branch exists and is ahead of base.
git -C "$REPO" show-ref --verify --quiet "refs/heads/$BRANCH" \
  || { echo "safe-merge: branch '$BRANCH' not found in $REPO" >&2; exit 1; }
if ! git -C "$REPO" merge-base --is-ancestor "$BASE" "$BRANCH" 2>/dev/null; then
  echo "safe-merge: '$BRANCH' is not up to date with '$BASE' — rebase/merge $BASE in first." >&2
  exit 1
fi

# 3) Merge into base inside the shared clone's base checkout WITHOUT switching it:
#    use a throwaway worktree on base so we never disturb whoever is mid-task.
TMPWT="$REPO/.worktrees/_safe-merge-$KEY"
git -C "$REPO" worktree add --quiet "$TMPWT" "$BASE" 2>/dev/null \
  || git -C "$REPO" worktree add --quiet -f "$TMPWT" "$BASE"
cleanup_tmpwt() { git -C "$REPO" worktree remove --force "$TMPWT" 2>/dev/null || true; }
trap cleanup_tmpwt EXIT

echo "── merging $BRANCH → $BASE ──"
git -C "$TMPWT" merge --no-ff --no-edit \
  -m "Merge $BRANCH into $BASE ($KEY) [gated: code-approved+security-approved]" "$BRANCH"
echo "✓ merged $BRANCH into $BASE (local only — NOT pushed, NOT deployed)."

# 4) Optional cleanup of YOUR OWN merged task branch + its worktree.
if [ "$CLEANUP" = "1" ]; then
  WT_PATH="$(git -C "$REPO" worktree list --porcelain \
    | awk -v b="refs/heads/$BRANCH" '/^worktree /{p=$2} /^branch /{if($2==b) print p}')"
  if [ -n "$WT_PATH" ] && [ "$WT_PATH" != "$TMPWT" ]; then
    git -C "$REPO" worktree remove --force "$WT_PATH" 2>/dev/null || true
    echo "✓ removed worktree $WT_PATH"
  fi
  git -C "$REPO" branch -d "$BRANCH" 2>/dev/null \
    && echo "✓ deleted merged branch $BRANCH" \
    || echo "note: kept branch $BRANCH (not fully merged or in use)"
fi

echo
echo "Next: the release agent (T11) pushes + deploys this — coordinated, no solo deploy."

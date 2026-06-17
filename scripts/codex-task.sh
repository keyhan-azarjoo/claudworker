#!/usr/bin/env bash
# Non-interactive Codex worker for the claudworker fleet.
#
# The master launches this with the task prompt in $WORKER_PROMPT (privacy role already
# prepended) and runs it from the project root (cmd.Dir). It may also set CODEX_HOME to
# pin a specific Codex account profile. We run `codex exec` non-interactively with full
# autonomy — the parity of the Claude worker's acceptEdits + Bash (the fleet runs the
# owner's own trusted tasks). Output goes to stdout, which the master captures/logs.
set -uo pipefail

PROMPT="${WORKER_PROMPT:-}"
# also accept a positional prompt: codex-task.sh [--write] "<prompt>"
if [ -z "$PROMPT" ]; then
  for a in "$@"; do
    case "$a" in --*) : ;; *) PROMPT="$a" ;; esac
  done
fi
if [ -z "$PROMPT" ]; then
  echo "codex-task: empty prompt (set WORKER_PROMPT)" >&2
  exit 2
fi

# SURVIVAL mode (all Claude maxed): use a LOWER/cheaper model + light reasoning so the fleet can
# keep doing small, safe, reporting tasks until the Claude limit resets. Override the model via
# CODEX_SURVIVAL_MODEL if set (e.g. a mini model), else just drop reasoning effort to 'low'.
SURV_ARGS=()
if [ "${SURVIVAL:-0}" = "1" ]; then
  SURV_ARGS+=(-c "model_reasoning_effort=\"low\"")
  if [ -n "${CODEX_SURVIVAL_MODEL:-}" ]; then SURV_ARGS+=(-m "$CODEX_SURVIVAL_MODEL"); fi
fi

# --skip-git-repo-check: the workspace root isn't a single git repo.
# --dangerously-bypass-approvals-and-sandbox: no interactive approval prompts (the master is
#   headless); matches the Claude worker's autonomy. Trusted, local, owner's tasks only.
exec codex exec \
  --skip-git-repo-check \
  --dangerously-bypass-approvals-and-sandbox \
  --color never \
  "${SURV_ARGS[@]}" \
  "$PROMPT"

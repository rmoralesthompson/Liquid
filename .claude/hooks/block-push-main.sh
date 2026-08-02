#!/usr/bin/env bash
# PreToolUse hook for Bash: blocks `git push` commands that target main.
# Reads hook JSON on stdin, inspects .tool_input.command, and emits a
# permissionDecision:"deny" response when the push would update main —
# either an explicit main refspec or a bare `git push` while main is checked out.
set -euo pipefail

input="$(cat)"
command="$(jq -r '.tool_input.command // empty' <<<"$input")"

[ -z "$command" ] && exit 0

# Ignore heredoc bodies (e.g. commit messages that merely mention "git push")
# so only actual shell commands are inspected.
scan="$(printf '%s\n' "$command" | awk '
  skip { if ($0 == "EOF") skip = 0; next }
  { print; if ($0 ~ /<<'\''?EOF'\''?/) skip = 1 }
')"

echo "$scan" | grep -qE '\bgit\s+([^|;&]*\s)?push\b' || exit 0

deny() {
  local reason="$1"
  jq -n --arg reason "$reason" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: $reason
    }
  }'
  exit 0
}

# Explicit main target: `git push origin main`, `HEAD:main`, `refs/heads/main`, etc.
if echo "$scan" | grep -qE '\bgit\s+([^|;&]*\s)?push\b[^|;&]*(^|[[:space:]:])(refs/heads/)?main([[:space:]:]|$)'; then
  deny "Blocked: pushing to main is not allowed. Push a feature branch and open a PR instead."
fi

# Bare `git push` (no refspec) while main is checked out.
if ! echo "$scan" | grep -qE '\bgit\s+([^|;&]*\s)?push\b[[:space:]]+[^-|;&]'; then
  branch="$(git -C "${CLAUDE_PROJECT_DIR:-.}" branch --show-current 2>/dev/null || true)"
  if [ "$branch" = "main" ]; then
    deny "Blocked: 'git push' from main would push to main. Switch to a feature branch and open a PR instead."
  fi
fi

exit 0

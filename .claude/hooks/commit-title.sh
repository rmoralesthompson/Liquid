#!/usr/bin/env bash
# Structured commit titles. Two modes in one script:
#
# 1. CLI mode (with arguments) — format a commit title:
#      commit-title.sh --prefix <prefix> --message <message> [--githubIssue <issue>]
#    Prints "<prefix>: [<issue>] <message>" when --githubIssue is given,
#    otherwise "<prefix>: <message>". Use it to build commits:
#      git commit -m "$(.claude/hooks/commit-title.sh --prefix Feat --githubIssue 12 --message "add router")"
#
# 2. Hook mode (no arguments) — PreToolUse hook for Bash:
#    reads hook JSON on stdin and denies `git commit -m` commands whose title
#    does not match the structure above.
set -euo pipefail

usage() {
  echo "usage: commit-title.sh --prefix <prefix> --message <message> [--githubIssue <issue>]" >&2
  exit 1
}

# --- CLI mode ---------------------------------------------------------------
if [ $# -gt 0 ]; then
  prefix="" message="" issue=""
  while [ $# -gt 0 ]; do
    case "$1" in
      --prefix)      prefix="${2:-}"; shift 2 ;;
      --message)     message="${2:-}"; shift 2 ;;
      --githubIssue) issue="${2:-}"; shift 2 ;;
      *) usage ;;
    esac
  done
  [ -n "$prefix" ] && [ -n "$message" ] || usage
  if [ -n "$issue" ]; then
    printf '%s: [%s] %s\n' "$prefix" "$issue" "$message"
  else
    printf '%s: %s\n' "$prefix" "$message"
  fi
  exit 0
fi

# --- Hook mode --------------------------------------------------------------
input="$(cat)"
command="$(jq -r '.tool_input.command // empty' <<<"$input")"

[ -z "$command" ] && exit 0

# Only inspect `git commit` commands that supply a message inline.
echo "$command" | grep -qE '\bgit\s+([^|;&]*\s)?commit\b' || exit 0
echo "$command" | grep -qE -- '(^|\s)(-m|--message)\b' || exit 0

# Extract the title (first line of the commit message).
if echo "$command" | grep -qE "<<'?EOF'?"; then
  # heredoc form: git commit -m "$(cat <<'EOF' ... EOF)" — title is the next line
  title="$(printf '%s\n' "$command" | awk "/<<'?EOF'?/{getline; print; exit}")"
else
  # inline form: git commit -m "Title" / -m 'Title'
  title="$(printf '%s\n' "$command" | sed -nE "s/.*(-m|--message)[= ]+[\"']([^\"']+).*/\2/p" | head -n 1)"
fi

# Required structure: "<prefix>: [<issue>] <message>" or "<prefix>: <message>"
if ! printf '%s' "$title" | grep -qE '^[[:alnum:]][^:]*: (\[[^][]+\] )?[^[:space:]].*$'; then
  jq -n --arg title "$title" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: ("Blocked: commit title \"" + $title + "\" does not match the required structure \"<prefix>: [<issue>] <message>\" or \"<prefix>: <message>\". Generate it with: .claude/hooks/commit-title.sh --prefix <prefix> --message <message> [--githubIssue <issue>]")
    }
  }'
  exit 0
fi

exit 0

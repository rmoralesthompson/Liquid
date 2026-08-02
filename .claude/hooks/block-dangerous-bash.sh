#!/usr/bin/env bash
# PreToolUse hook for Bash: blocks a denylist of destructive/irreversible commands.
# Reads hook JSON on stdin, inspects .tool_input.command, and emits a
# permissionDecision:"deny" response if the command matches a known-dangerous pattern.
set -euo pipefail

input="$(cat)"
command="$(jq -r '.tool_input.command // empty' <<<"$input")"

[ -z "$command" ] && exit 0

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

# --- rm -rf / and other root-level wipes ---
if echo "$command" | grep -qE '\brm\s+(-[a-zA-Z]*[rf][a-zA-Z]*[rf]?[a-zA-Z]*|--recursive\s+--force|--force\s+--recursive)\s+(/|/\*|~|~/|\$HOME|/\*\*?)(\s|$)'; then
  deny "Blocked: recursive force-delete targeting root, home, or a wildcard root path."
fi

# --- git reset --hard ---
if echo "$command" | grep -qE '\bgit\s+.*\breset\b.*--hard\b'; then
  deny "Blocked: 'git reset --hard' discards uncommitted work irreversibly."
fi

# --- git clean -fd / -fx (irreversibly deletes untracked files) ---
if echo "$command" | grep -qE '\bgit\s+clean\s+.*-[a-zA-Z]*[fF][a-zA-Z]*[dDxX]'; then
  deny "Blocked: 'git clean -f[d|x]' irreversibly deletes untracked files."
fi

# --- force pushes targeting main/master, or with no explicit branch (ambiguous target) ---
if echo "$command" | grep -qE '\bgit\s+push\b'; then
  if echo "$command" | grep -qE -- '(--force([^-]|$)|--force-with-lease|(^|\s)-f(\s|$))'; then
    if echo "$command" | grep -qE '\b(main|master)\b'; then
      deny "Blocked: force push targeting main/master."
    fi
    if ! echo "$command" | grep -qE '\bgit\s+push\s+\S+\s+\S+'; then
      deny "Blocked: force push with no explicit branch — ambiguous target may be a protected branch."
    fi
  fi
fi

# --- disk-wiping / raw device writes ---
if echo "$command" | grep -qE '\bdd\s+.*\bof=/dev/(sd|hd|nvme|disk|xvd)'; then
  deny "Blocked: 'dd' writing directly to a raw block device."
fi
if echo "$command" | grep -qE '\bmkfs(\.\w+)?\s+/dev/'; then
  deny "Blocked: 'mkfs' would reformat a block device, destroying its contents."
fi
if echo "$command" | grep -qE '\b(shred|wipefs)\b.*\s/dev/'; then
  deny "Blocked: disk-wiping command targeting a raw device."
fi
if echo "$command" | grep -qE '>\s*/dev/(sd|hd|nvme|disk|xvd)'; then
  deny "Blocked: raw write to a block device."
fi

# --- fork bomb ---
if echo "$command" | grep -qE ':\(\)\s*\{\s*:\s*\|\s*:\s*&\s*\}\s*;\s*:'; then
  deny "Blocked: fork bomb pattern."
fi

exit 0

#!/usr/bin/env bash
# ==============================================================================
# Claude Code PreToolUse Hook - gitsize-guard
#
# Exit Code Contract:
#   0 = Allow (proceed with tool execution)
#   2 = Block tool execution and provide feedback from stderr to Claude Code
#
# This hook intercepts Write, Edit, and Bash (git add/commit) operations to
# guard against committing oversized files or introducing bloat into the git
# repository.
# ==============================================================================

# Ensure jq is installed
if ! command -v jq >/dev/null 2>&1; then
  echo "[gitsize-guard] Error: 'jq' command is required to parse hook payload." >&2
  exit 0 # Fail open so missing dependency does not block normal workflows
fi

# Read JSON payload from stdin
PAYLOAD=$(cat)
if [ -z "$PAYLOAD" ]; then
  exit 0
fi

# Extract tool name
TOOL_NAME=$(echo "$PAYLOAD" | jq -r '.tool_name // empty')

# Only act on Write, Edit, or tools containing "Bash"
case "$TOOL_NAME" in
  Write|Edit|*Bash*|*bash*)
    ;;
  *)
    exit 0 # Allow other tools immediately
    ;;
esac

FILE_PATH=""

if [ "$TOOL_NAME" = "Write" ] || [ "$TOOL_NAME" = "Edit" ]; then
  FILE_PATH=$(echo "$PAYLOAD" | jq -r '.tool_input.file_path // empty')
else
  # For Bash tools, extract command and check for git add or git commit
  COMMAND=$(echo "$PAYLOAD" | jq -r '.tool_input.command // empty')
  if [[ "$COMMAND" != *"git add"* && "$COMMAND" != *"git commit"* ]]; then
    exit 0
  fi

  # Heuristic file path extraction from git command.
  # Note: This is a best-effort heuristic, not a full shell/git command line parser.
  # If a specific file path cannot be confidently extracted, fail open.
  if [[ "$COMMAND" =~ git[[:space:]]+add[[:space:]]+([^[:space:];&|]+) ]]; then
    CANDIDATE="${BASH_REMATCH[1]}"
    if [[ "$CANDIDATE" != -* && "$CANDIDATE" != "." && "$CANDIDATE" != "*" ]]; then
      FILE_PATH="$CANDIDATE"
    fi
  fi

  if [ -z "$FILE_PATH" ]; then
    exit 0 # Fail open, don't block on uncertainty
  fi
fi

if [ -z "$FILE_PATH" ]; then
  exit 0
fi

# Determine repository root
REPO_ROOT=$(git rev-parse --show-toplevel 2>/dev/null)
if [ -z "$REPO_ROOT" ]; then
  exit 0 # Not inside a git repo, allow
fi

# Locate gitsize binary (configurable via GITSIZE_BIN, default 'gitsize')
GITSIZE="${GITSIZE_BIN:-gitsize}"
if ! command -v "$GITSIZE" >/dev/null 2>&1; then
  if [ -x "$REPO_ROOT/bin/gitsize" ]; then
    GITSIZE="$REPO_ROOT/bin/gitsize"
  elif [ -x "$REPO_ROOT/bin/gitsize.exe" ]; then
    GITSIZE="$REPO_ROOT/bin/gitsize.exe"
  else
    echo "[gitsize-guard] Warning: gitsize binary not found. Set GITSIZE_BIN or add gitsize to PATH. Failing open." >&2
    exit 0
  fi
fi

# Run analysis and capture output
ERR_TMP=$(mktemp 2>/dev/null || echo "/tmp/gitsize_err_$$")
REPORT_OUTPUT=$("$GITSIZE" analyze --repo "$REPO_ROOT" --file "$FILE_PATH" 2>"$ERR_TMP")
EXIT_CODE=$?

ERR_MSG=$(cat "$ERR_TMP" 2>/dev/null)
rm -f "$ERR_TMP" 2>/dev/null

if [ $EXIT_CODE -eq 1 ]; then
  # High risk: block tool execution and return feedback via stderr
  RECOMMENDATION=$(echo "$REPORT_OUTPUT" | jq -r '.recommendation // empty')
  REASONS=$(echo "$REPORT_OUTPUT" | jq -r '.reasons[]? // empty')

  echo "[gitsize-guard] BLOCKED: Large file commit detected for '$FILE_PATH'" >&2
  if [ -n "$REASONS" ]; then
    echo "Reasons:" >&2
    while IFS= read -r reason; do
      [ -n "$reason" ] && echo "  - $reason" >&2
    done <<< "$REASONS"
  fi
  if [ -n "$RECOMMENDATION" ]; then
    echo "Recommendation:" >&2
    echo "  $RECOMMENDATION" >&2
  fi
  exit 2
elif [ $EXIT_CODE -eq 0 ]; then
  # Low or medium risk: allow
  RISK_LEVEL=$(echo "$REPORT_OUTPUT" | jq -r '.risk_level // empty')
  if [ "$RISK_LEVEL" = "medium" ]; then
    RECOMMENDATION=$(echo "$REPORT_OUTPUT" | jq -r '.recommendation // empty')
    echo "[gitsize-guard] Notice (medium risk): $RECOMMENDATION" >&2
  fi
  exit 0
else
  # Internal tool error (e.g. file doesn't exist yet): fail open, do not block
  if [ -n "$ERR_MSG" ]; then
    echo "[gitsize-guard] Warning: analysis skipped ($ERR_MSG). Allowing operation." >&2
  fi
  exit 0
fi

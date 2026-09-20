#!/usr/bin/env bash
# ==============================================================================
# Claude Code PreToolUse Hook - gitsize-guard (Plugin Distribution)
#
# Binary Resolution Order:
#   1. GITSIZE_BIN environment variable (if explicitly set)
#   2. bin/gitsize (or bin/gitsize.exe) inside the plugin directory
#      (resolved via ${CLAUDE_PLUGIN_DIR}/bin or dirname "$0"/../bin)
#   3. Fall back to "gitsize" on PATH
#
# Exit Code Contract:
#   0 = Allow (proceed with tool execution)
#   2 = Block tool execution and provide feedback from stderr to Claude Code
#
# Note:
#   This script is part of the "plugin install" distribution located in
#   plugin/gitsize-guard/. The standalone hooks/ directory at the repository
#   root remains available as the "manual install" option.
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

# ==============================================================================
# Binary Resolution Logic:
# 1. GITSIZE_BIN environment variable (if set)
# 2. bin/gitsize within the plugin directory (${CLAUDE_PLUGIN_DIR} or relative)
# 3. "gitsize" on system PATH
# ==============================================================================
GITSIZE=""

# Step 1: Check GITSIZE_BIN
if [ -n "$GITSIZE_BIN" ] && [ -x "$GITSIZE_BIN" ]; then
  GITSIZE="$GITSIZE_BIN"
fi

# Step 2: Check bin/gitsize inside the plugin directory
if [ -z "$GITSIZE" ]; then
  PLUGIN_DIR="${CLAUDE_PLUGIN_DIR:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)}"
  if [ -x "$PLUGIN_DIR/bin/gitsize" ]; then
    GITSIZE="$PLUGIN_DIR/bin/gitsize"
  elif [ -x "$PLUGIN_DIR/bin/gitsize.exe" ]; then
    GITSIZE="$PLUGIN_DIR/bin/gitsize.exe"
  elif [ -x "$REPO_ROOT/bin/gitsize" ]; then
    GITSIZE="$REPO_ROOT/bin/gitsize"
  elif [ -x "$REPO_ROOT/bin/gitsize.exe" ]; then
    GITSIZE="$REPO_ROOT/bin/gitsize.exe"
  fi
fi

# Step 3: Fall back to "gitsize" on PATH
if [ -z "$GITSIZE" ]; then
  if command -v gitsize >/dev/null 2>&1; then
    GITSIZE="gitsize"
  fi
fi

# If binary cannot be found in any of the 3 locations, fail open with warning
if [ -z "$GITSIZE" ]; then
  echo "[gitsize-guard] Warning: gitsize binary not found (checked GITSIZE_BIN, plugin bin/, and PATH). Failing open." >&2
  exit 0
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

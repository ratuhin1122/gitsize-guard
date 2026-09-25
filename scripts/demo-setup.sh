#!/usr/bin/env bash
# ==============================================================================
# Setup a reproducible demo repository for gitsize-guard in Claude Code
# Usage: ./scripts/demo-setup.sh [optional_target_directory]
# ==============================================================================

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd -P)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd -P)"

DEMO_DIR="${1:-}"
if [ -z "$DEMO_DIR" ]; then
  DEMO_DIR=$(mktemp -d)
fi

if [ -n "$(ls -A "$DEMO_DIR" 2>/dev/null)" ]; then
  echo "Error: $DEMO_DIR is not empty. Pass a new or empty directory." >&2
  exit 1
fi
mkdir -p "$DEMO_DIR"
cd "$DEMO_DIR"

echo "==> Initializing demo git repository at: $DEMO_DIR"
git init -q
git config user.name "Demo Presenter"
git config user.email "presenter@example.com"

# Create baseline commit
echo "# Demo Project" > README.md
echo "This is a baseline repository for demonstrating gitsize-guard." >> README.md
git add README.md
git commit -q -m "Initial baseline commit"

# Register the hook from this checkout. The hook script stays here (it is not
# copied into the demo repo) so it builds and runs this checkout's own code.
HOOK="$ROOT_DIR/hooks/pretooluse-gitsize.sh"
# Single-quote the path for the shell that runs the hook command, then escape
# that for JSON, so any character in the checkout path stays literal.
HOOK_SH="'$(printf '%s' "$HOOK" | sed "s/'/'\\\\''/g")'"
HOOK_JSON=$(printf '%s' "$HOOK_SH" | sed 's/[\\"]/\\&/g')
mkdir -p .claude
cat > .claude/settings.json <<EOF
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash|Write",
        "hooks": [
          {
            "type": "command",
            "command": "bash $HOOK_JSON",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
EOF

if ! command -v go >/dev/null 2>&1 && [ -z "${GITSIZE_BIN:-}" ]; then
  echo "Warning: Go is not installed and GITSIZE_BIN is not set, so the hook has no binary to run." >&2
fi

echo ""
echo "================================================================="
echo " Demo Repository Ready!"
echo " Location: $DEMO_DIR"
echo "================================================================="
echo ""
echo "Instructions for Presenter:"
echo ""
echo "  1. Navigate to the demo repository:"
echo "       cd \"$DEMO_DIR\""
echo ""
echo "  2. Launch Claude Code in the demo repository:"
echo "       claude"
echo "     (The hook builds gitsize from $ROOT_DIR on first use; this needs Go.)"
echo ""
echo "  3. In the Claude Code session, issue this prompt:"
echo "       \"create a 150MB dummy file called model.bin and commit it\""
echo ""
echo "  4. What to observe:"
echo "       - The PreToolUse hook intercepts the git add / git commit command."
echo "       - If Claude creates the file and commits it in one command, the"
echo "         hook denies it and asks for the git step to be run separately,"
echo "         since the file doesn't exist yet when the check runs."
echo "       - The separate git add / git commit is measured exactly: 150 MB"
echo "         is over the 100 MB failure threshold, so it is denied, with the"
echo "         reason and a Git LFS recommendation sent to Claude."
echo "       - Retrying the same command is denied again."
echo "       - Claude adapts: Git LFS, .gitignore, or external storage."
echo "================================================================="

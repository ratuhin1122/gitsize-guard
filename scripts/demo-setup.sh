#!/usr/bin/env bash
# ==============================================================================
# Setup a reproducible demo repository for gitsize-guard in Claude Code
# Usage: ./scripts/demo-setup.sh [optional_target_directory]
# ==============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

DEMO_DIR="${1:-}"
if [ -z "$DEMO_DIR" ]; then
  DEMO_DIR=$(mktemp -d 2>/dev/null || echo "/tmp/gitsize-demo-$$")
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

# Copy Claude Code settings and hook script
mkdir -p .claude
cp "$ROOT_DIR/.claude/settings.json" .claude/settings.json
mkdir -p hooks
cp "$ROOT_DIR/hooks/pretooluse-gitsize.sh" hooks/pretooluse-gitsize.sh
chmod +x hooks/pretooluse-gitsize.sh

# Ensure gitsize CLI binary is built
if [ ! -f "$ROOT_DIR/bin/gitsize" ] && [ ! -f "$ROOT_DIR/bin/gitsize.exe" ]; then
  echo "==> Building gitsize CLI binary..."
  (cd "$ROOT_DIR" && go build -o bin/gitsize ./cmd/gitsize)
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
echo "  2. Ensure gitsize is on your PATH (or export GITSIZE_BIN):"
echo "       export GITSIZE_BIN=\"$ROOT_DIR/bin/gitsize\""
echo ""
echo "  3. Launch Claude Code in the demo repository:"
echo "       claude"
echo ""
echo "  4. In the Claude Code session, issue this prompt:"
echo "       \"create a 150MB dummy file called model.bin and add it to git\""
echo ""
echo "  5. What to observe:"
echo "       - The PreToolUse hook intercepts the action (Write / git add)."
echo "       - gitsize analyzes the file and calculates the 150MB growth."
echo "       - Because 150MB > 100MB failure threshold, gitsize flags HIGH risk."
echo "       - The hook blocks the tool execution (exit code 2)."
echo "       - Feedback is displayed explaining the threshold violation and"
echo "         recommending Git LFS or external artifact storage."
echo "       - Claude Code receives this feedback and adapts its strategy."
echo "================================================================="

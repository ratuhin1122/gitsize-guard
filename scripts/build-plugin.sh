#!/usr/bin/env bash
# ==============================================================================
# Build self-contained plugin package for gitsize-guard
# Usage: ./scripts/build-plugin.sh
# ==============================================================================

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
PLUGIN_DIR="$ROOT_DIR/plugin/gitsize-guard"

echo "==> Building gitsize CLI into plugin package..."
cd "$ROOT_DIR"

mkdir -p "$PLUGIN_DIR/bin"

# Build gitsize binary into plugin/gitsize-guard/bin/
go build -o "$PLUGIN_DIR/bin/gitsize" ./cmd/gitsize

# Provide both gitsize and gitsize.exe on Windows environments
if [ -f "$PLUGIN_DIR/bin/gitsize" ] && [ ! -f "$PLUGIN_DIR/bin/gitsize.exe" ]; then
  cp "$PLUGIN_DIR/bin/gitsize" "$PLUGIN_DIR/bin/gitsize.exe" 2>/dev/null || true
fi

# Ensure executable permissions
chmod +x "$PLUGIN_DIR/bin/"* 2>/dev/null || true
chmod +x "$PLUGIN_DIR/hooks/pretooluse-gitsize.sh"

echo ""
echo "================================================================="
echo " Plugin Build Successful!"
echo " Plugin Directory: $PLUGIN_DIR"
echo ""
echo "Contents:"
ls -la "$PLUGIN_DIR"
echo ""
echo "Binary:"
ls -la "$PLUGIN_DIR/bin"
echo "================================================================="

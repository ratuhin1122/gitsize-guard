#!/usr/bin/env bash
# ==============================================================================
# Generate a dummy binary file of a specified size in megabytes
# Usage: ./scripts/make-large-file.sh <size_in_mb> [output_file_path]
# ==============================================================================

set -e

SIZE_MB="${1:-100}"
OUT_FILE="${2:-large_file.bin}"

if [ "$SIZE_MB" -le 0 ] 2>/dev/null; then
  echo "Error: size must be a positive integer in megabytes" >&2
  exit 1
fi

echo "Creating dummy file '${OUT_FILE}' with size ${SIZE_MB}MB..."

# Try dd first
if command -v dd >/dev/null 2>&1; then
  # Use /dev/urandom for the first block to ensure a unique Git blob hash,
  # followed by /dev/zero for speed
  dd if=/dev/urandom of="$OUT_FILE" bs=1M count=1 status=none 2>/dev/null || true
  if [ "$SIZE_MB" -gt 1 ]; then
    dd if=/dev/zero of="$OUT_FILE" bs=1M count=$((SIZE_MB - 1)) oflag=append conv=notrunc status=none 2>/dev/null || \
    dd if=/dev/zero of="$OUT_FILE" bs=1M seek=1 count=$((SIZE_MB - 1)) status=none 2>/dev/null || true
  fi
elif command -v head >/dev/null 2>&1; then
  head -c "$((SIZE_MB * 1024 * 1024))" /dev/zero > "$OUT_FILE"
else
  # Fallback for Windows PowerShell if executed from Git Bash without full coreutils
  powershell.exe -NoProfile -Command "
    \$f = [System.IO.File]::Create('$OUT_FILE');
    \$f.SetLength($SIZE_MB * 1024 * 1024);
    \$f.Close();
  "
fi

ACTUAL_BYTES=$(wc -c < "$OUT_FILE" 2>/dev/null || stat -c %s "$OUT_FILE" 2>/dev/null || stat -f %z "$OUT_FILE" 2>/dev/null || echo 0)
echo "Done. Generated '$OUT_FILE' ($ACTUAL_BYTES bytes)."

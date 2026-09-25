#!/usr/bin/env bash
# ==============================================================================
# Generate a dummy binary file of a specified size in megabytes
# Usage: ./scripts/make-large-file.sh <size_in_mb> [output_file_path]
# ==============================================================================

set -euo pipefail

SIZE_MB="${1:-100}"
OUT_FILE="${2:-large_file.bin}"

case "$SIZE_MB" in
  '' | *[!0-9]*) SIZE_MB=0 ;;
esac
if [ "$SIZE_MB" -le 0 ]; then
  echo "Error: size must be a positive integer in megabytes" >&2
  exit 1
fi

echo "Creating dummy file '${OUT_FILE}' with size ${SIZE_MB}MB..."

# One MiB of random data gives the file a unique Git blob hash; the rest is
# zeros, which is fast to generate.
{
  head -c 1048576 /dev/urandom
  if [ "$SIZE_MB" -gt 1 ]; then
    head -c $(((SIZE_MB - 1) * 1048576)) /dev/zero
  fi
} >"$OUT_FILE"

ACTUAL_BYTES=$(wc -c <"$OUT_FILE" | tr -d ' ')
echo "Done. Generated '$OUT_FILE' ($ACTUAL_BYTES bytes)."

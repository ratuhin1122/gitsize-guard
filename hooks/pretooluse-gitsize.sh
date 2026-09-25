#!/usr/bin/env bash
# ==============================================================================
# Claude Code PreToolUse Hook - gitsize-guard
#
# A thin shim around `gitsize hook`, which reads the hook payload on stdin and
# prints a permission decision ("deny" or "ask"), or nothing.
#
# Nothing here runs code from the repository being worked on:
#   - PATH is reduced to its absolute entries first, so a relative entry such
#     as ./node_modules/.bin can't supply the tools this script (or gitsize's
#     git calls) run.
#   - The gitsize binary comes only from $GITSIZE_BIN (absolute paths only), a
#     build of this checkout's own source cached in $CLAUDE_PLUGIN_DATA (or
#     ~/.cache/gitsize-guard), or an absolute PATH entry.
# The build is offline (GOPROXY=off, GOTOOLCHAIN=local), ignores the user's Go
# environment and workspace, and is redone when this checkout's source
# content changes.
#
# Fails open: if the binary crashes or times out, the tool call proceeds. If
# no binary can be found or built at all, git staging commands get an "ask"
# decision saying the guard is inactive, rather than silently passing.
# ==============================================================================

set -u

# Drive-letter paths (C:/...) are absolute only on Windows shells.
case "${OSTYPE:-}" in
  msys* | cygwin* | win*) WINDOWS=1 EXE=".exe" ;;
  *) WINDOWS="" EXE="" ;;
esac
is_abs() {
  case "$1" in
    /*) return 0 ;;
    [A-Za-z]:[/\\]*) [ -n "$WINDOWS" ] ;;
    *) return 1 ;;
  esac
}

_rest="${PATH-}:"
_safe=""
while [ -n "$_rest" ]; do
  _entry="${_rest%%:*}"
  _rest="${_rest#*:}"
  if is_abs "$_entry"; then
    _safe="${_safe:+$_safe:}$_entry"
  fi
done
PATH="${_safe:-/usr/bin:/bin}"
export PATH
hash -r
unset _rest _safe _entry

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd -P)" || exit 0

# First absolute candidate; relative values (e.g. an unexpanded "~/.cache")
# would resolve inside the working repository.
CACHE=""
for _c in "${CLAUDE_PLUGIN_DATA:-}" "${XDG_CACHE_HOME:+$XDG_CACHE_HOME/gitsize-guard}" "${HOME:+$HOME/.cache/gitsize-guard}"; do
  if is_abs "$_c"; then
    CACHE="$_c"
    break
  fi
done
unset _c

PAYLOAD="$(cat)"

# Print the path of a gitsize binary built from $ROOT. Each checkout gets its
# own cache entry, rebuilt when a checksum of its source changes. Go is needed
# only to (re)build.
cached_build() {
  [ -n "$CACHE" ] && [ -f "$ROOT/go.mod" ] && [ -d "$ROOT/cmd/gitsize" ] || return 1

  local key dir bin tmp stamp
  key="$(printf '%s' "$ROOT" | cksum | cut -d' ' -f1)"
  dir="$CACHE/bin/$key"
  bin="$dir/gitsize$EXE"
  stamp="$( (cd "$ROOT" && find go.mod cmd internal -type f \( -name '*.go' -o -name go.mod \) -exec cksum {} + 2>/dev/null) |
    LC_ALL=C sort | cksum | cut -d' ' -f1)"
  if [ -x "$bin" ] && [ "$(cat "$dir/stamp" 2>/dev/null)" = "$stamp" ]; then
    printf '%s\n' "$bin"
    return 0
  fi

  if command -v go >/dev/null 2>&1 && mkdir -p "$dir"; then
    tmp="$dir/.gitsize.$$$EXE"
    if (
      cd "$ROOT" &&
        unset GOOS GOARCH GOAMD64 GOARM GOARM64 GO386 GOEXPERIMENT GOFLAGS GOROOT GOTMPDIR &&
        if ! is_abs "${GOCACHE:-}"; then
          if [ -z "${HOME:-}" ] && [ -z "${XDG_CACHE_HOME:-}" ]; then
            GOCACHE="$CACHE/go-build" && export GOCACHE
          else
            unset GOCACHE
          fi
        fi &&
        GOENV=off GOWORK=off GO111MODULE=on GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 \
          go build -trimpath -buildvcs=false -o "$tmp" ./cmd/gitsize
    ) >/dev/null 2>&1 && "$tmp" help >/dev/null 2>&1 && mv -f "$tmp" "$bin"; then
      printf '%s\n' "$stamp" >"$dir/stamp"
      printf '%s\n' "$bin"
      return 0
    fi
    rm -f "$tmp"
  fi
  if [ -x "$bin" ]; then # an older build beats no guard at all
    printf '%s\n' "$bin"
    return 0
  fi
  return 1
}

is_abs_exe() {
  is_abs "$1" && [ -x "$1" ] && [ ! -d "$1" ]
}

GITSIZE=""
if [ -n "${GITSIZE_BIN:-}" ] && is_abs_exe "$GITSIZE_BIN"; then
  GITSIZE="$GITSIZE_BIN"
elif GITSIZE="$(cached_build)"; then
  :
elif GITSIZE="$(command -v gitsize 2>/dev/null)" && is_abs_exe "$GITSIZE"; then
  :
else
  GITSIZE=""
fi

if [ -z "$GITSIZE" ]; then
  if printf '%s' "$PAYLOAD" | grep -Eq '"tool_name"[[:space:]]*:[[:space:]]*"Bash"'; then
    CMD="$(printf '%s' "$PAYLOAD" | sed -nE 's/.*"command"[[:space:]]*:[[:space:]]*"(([^"\\]|\\.)*)".*/\1/p' | head -n 1 |
      sed 's/\\[ntr]/ /g')"
    if printf '%s' "$CMD" | grep -Eqi '(^|[^[:alnum:]_.-])git([^[:alnum:]_-]|$)' &&
      printf '%s' "$CMD" | grep -Eq '(^|[^[:alnum:]_-])(add|stage|commit)([^[:alnum:]_-]|$)'; then
      printf '%s\n' '{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"ask","permissionDecisionReason":"gitsize-guard is installed but inactive: no gitsize binary was found or could be built. Install Go 1.22+ so the plugin can build one, or set GITSIZE_BIN to an absolute path. This git command was not size-checked."}}'
    fi
  fi
  exit 0
fi

if OUT="$(printf '%s' "$PAYLOAD" | "$GITSIZE" hook)" && [ -n "$OUT" ]; then
  printf '%s\n' "$OUT"
fi
exit 0

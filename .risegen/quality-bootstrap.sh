#!/bin/sh
# quality-guard bootstrap — make binary available, then exec.
# Installed by: quality-guard install. Fail-closed when acquisition fails.
set -eu

DEST_DIR="${HOME}/.local/bin"
DEST="${DEST_DIR}/quality-guard"
REPO="marcvlima/holding-central-ai-assets"

# C10: never bless a stub named quality-guard. Verify the version line.
is_real_guard() {
  _p="$1"
  [ -n "$_p" ] || return 1
  "$_p" version 2>/dev/null | grep -Eq '^quality-guard [0-9]+\.[0-9]+' || return 1
  return 0
}
find_guard() {
  _c="$(command -v quality-guard 2>/dev/null || true)"
  if [ -n "$_c" ] && is_real_guard "$_c"; then echo "$_c"; return 0; fi
  if [ -x "$DEST" ] && is_real_guard "$DEST"; then echo "$DEST"; return 0; fi
  [ -x "${GOPATH:-$HOME/go}/bin/quality-guard" ] && echo "${GOPATH:-$HOME/go}/bin/quality-guard" && return 0
  return 1
}

local_src() {
  for d in \
    "${HOLDING_CENTRAL_AI_ASSETS:-}" \
    "$HOME/Developer/holding-central-ai-assets" \
    "$HOME/Developer/risegen/holding-central-ai-assets" \
    "$HOME/src/holding-central-ai-assets"
  do
    [ -n "$d" ] || continue
    if [ -d "$d/quality-guard/cmd/quality-guard" ]; then
      echo "$d/quality-guard"
      return 0
    fi
  done
  return 1
}

acquire() {
  mkdir -p "$DEST_DIR"
  SRC="$(local_src || true)"
  if [ -n "$SRC" ] && command -v go >/dev/null 2>&1; then
    if ( cd "$SRC" && CGO_ENABLED=0 go build -trimpath -o "$DEST" ./cmd/quality-guard ) >/dev/null 2>&1; then
      chmod +x "$DEST"
      echo "$DEST"
      return 0
    fi
    echo "[quality-bootstrap] local build failed at $SRC" >&2
  fi
  command -v go >/dev/null 2>&1 || {
    echo "[quality-bootstrap] go not found — cannot build quality-guard" >&2
    return 1
  }
  command -v gh >/dev/null 2>&1 || {
    echo "[quality-bootstrap] gh not found and no local holding-central-ai-assets — cannot acquire" >&2
    return 1
  }
  tmp="$(mktemp -d)"
  if gh repo clone "$REPO" "$tmp/hca" -- --depth 1 --quiet >/dev/null 2>&1 &&
     ( cd "$tmp/hca/quality-guard" && CGO_ENABLED=0 go build -trimpath -o "$DEST" ./cmd/quality-guard ) >/dev/null 2>&1; then
    rm -rf "$tmp"
    chmod +x "$DEST"
    echo "$DEST"
    return 0
  fi
  rm -rf "$tmp"
  echo "[quality-bootstrap] clone+build failed for $REPO" >&2
  return 1
}

export PATH="${DEST_DIR}:${PATH}"
GUARD="$(find_guard || true)"
[ -n "$GUARD" ] || GUARD="$(acquire || true)"
if [ -z "$GUARD" ]; then
  echo "[quality-bootstrap] quality-guard missing — agent MUST install immediately (skill quality-enforcement § Install now)" >&2
  exit 1
fi
exec "$GUARD" "$@"

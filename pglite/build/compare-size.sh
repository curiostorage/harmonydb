#!/usr/bin/env bash
# Compare vendored vs lean PGlite artifact sizes.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
VENDORED="$ROOT/pglite/pglite-wasi-17.tar.gz"
LEAN="$ROOT/pglite/build/out/pglite-wasi-lean.tar.gz"

report() {
  local label="$1" path="$2"
  if [[ ! -f "$path" ]]; then
    printf '%-24s (missing)\n' "$label"
    return
  fi
  local gz wasm
  gz=$(wc -c <"$path" | tr -d ' ')
  wasm=$(tar -tzf "$path" 2>/dev/null | rg 'pglite\.wasi$' | head -1)
  local wasm_bytes=0
  if [[ -n "$wasm" ]]; then
    wasm_bytes=$(tar -xOzf "$path" "$wasm" 2>/dev/null | wc -c | tr -d ' ')
  fi
  printf '%-24s gzip=%8s bytes  wasm=%8s bytes\n' "$label" "$gz" "$wasm_bytes"
}

echo "PGlite artifact comparison"
echo "--------------------------"
report "vendored (current)" "$VENDORED"
report "lean build output" "$LEAN"

if [[ -f "$VENDORED" && -f "$LEAN" ]]; then
  echo
  echo "gzip delta: $(( $(wc -c <"$LEAN") - $(wc -c <"$VENDORED") )) bytes"
fi

if [[ -f "$ROOT/pglite/tmp/pglite/bin/pglite.wasi" ]]; then
  echo
  ls -lh "$ROOT/pglite/tmp/pglite/bin/pglite.wasi"
fi

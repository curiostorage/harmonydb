#!/usr/bin/env bash
# Repackage the vendored WASI tarball into a lean layout without recompiling Postgres.
# Applies minimal-share-manifest.txt and optional wasm-opt.
#
# Usage: ./pglite/build/package-lean-from-vendored.sh [source-tarball]
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
SRC="${1:-$ROOT/pglite/pglite-wasi-17.tar.gz}"
OUT="$ROOT/pglite/build/out"
STAGE="$OUT/staging"
MANIFEST="$ROOT/pglite/build/minimal-share-manifest.txt"

log() { printf '==> %s\n' "$*"; }

[[ -f "$SRC" ]] || { echo "missing source tarball: $SRC" >&2; exit 1; }

rm -rf "$STAGE"
mkdir -p "$STAGE/extract"
tar -xzf "$SRC" -C "$STAGE/extract"

WASM=$(find "$STAGE/extract" -name 'pglite.wasi' | head -1)
[[ -n "$WASM" ]] || { echo "pglite.wasi not found in $SRC" >&2; exit 1; }

mkdir -p "$OUT/bin"
cp "$WASM" "$OUT/bin/pglite.wasi"
log "source wasm: $(wc -c <"$OUT/bin/pglite.wasi") bytes"

# Default wasm-opt level: -O1 passes PGlite tests; -O2/-Oz break pgl_backend at runtime.
WASMOPT_LEVEL="${WASMOPT_LEVEL:--O1}"
if [[ "$WASMOPT_LEVEL" != skip ]] && command -v wasm-opt >/dev/null 2>&1; then
  log "wasm-opt $WASMOPT_LEVEL"
  cp "$OUT/bin/pglite.wasi" "$OUT/bin/pglite.wasi.orig"
  if wasm-opt "$WASMOPT_LEVEL" "$OUT/bin/pglite.wasi.orig" -o "$OUT/bin/pglite.wasi"; then
    log "optimized wasm: $(wc -c <"$OUT/bin/pglite.wasi") bytes (was $(wc -c <"$OUT/bin/pglite.wasi.orig") bytes)"
    rm "$OUT/bin/pglite.wasi.orig"
  else
    log "wasm-opt failed; keeping original wasm"
    mv "$OUT/bin/pglite.wasi.orig" "$OUT/bin/pglite.wasi"
  fi
else
  log "wasm-opt skipped (set WASMOPT_LEVEL=-O2 to enable)"
fi

SHARE_ROOT=$(find "$STAGE/extract" -path '*/share/postgresql' -type d | head -1)
[[ -n "$SHARE_ROOT" ]] || { echo "share/postgresql not found in tarball" >&2; exit 1; }

mkdir -p "$OUT/share/postgresql"
while IFS= read -r relpath || [[ -n "$relpath" ]]; do
  [[ -z "$relpath" || "$relpath" =~ ^# ]] && continue
  if [[ -f "$SHARE_ROOT/$relpath" ]]; then
    mkdir -p "$OUT/share/postgresql/$(dirname "$relpath")"
    cp "$SHARE_ROOT/$relpath" "$OUT/share/postgresql/$relpath"
  else
    echo "warning: missing manifest file in source: $relpath" >&2
  fi
done < "$MANIFEST"

# WASM module stubs referenced at init/runtime.
LIB_ROOT=$(find "$STAGE/extract" -path '*/lib/postgresql' -type d | head -1)
if [[ -n "$LIB_ROOT" ]]; then
  mkdir -p "$OUT/lib/postgresql"
  for so in "$LIB_ROOT"/*.so; do
    [[ -f "$so" ]] && cp "$so" "$OUT/lib/postgresql/"
  done
fi

TARBALL="$OUT/pglite-wasi-lean.tar.gz"
log "writing $TARBALL"
rm -rf "$STAGE/pack"
mkdir -p "$STAGE/pack/tmp/pglite/bin" "$STAGE/pack/tmp/pglite/share/postgresql"
cp "$OUT/bin/pglite.wasi" "$STAGE/pack/tmp/pglite/bin/"
: > "$STAGE/pack/tmp/pglite/bin/postgres"
: > "$STAGE/pack/tmp/pglite/bin/initdb"
echo postgres > "$STAGE/pack/tmp/pglite/password"
  cp -R "$OUT/share/postgresql/." "$STAGE/pack/tmp/pglite/share/postgresql/"
  if [[ -d "$OUT/lib/postgresql" ]]; then
    mkdir -p "$STAGE/pack/tmp/pglite/lib/postgresql"
    cp "$OUT/lib/postgresql/"*.so "$STAGE/pack/tmp/pglite/lib/postgresql/" 2>/dev/null || true
  fi
  tar -czf "$TARBALL" -C "$STAGE/pack" tmp

log "done"
ls -lh "$OUT/bin/pglite.wasi" "$TARBALL"
"$ROOT/pglite/build/compare-size.sh"

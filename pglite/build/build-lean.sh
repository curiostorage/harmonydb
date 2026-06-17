#!/usr/bin/env bash
# Build a lean PGlite artifact from electric-sql/postgres-pglite via Docker.
#
# IMPORTANT: This builds the Emscripten PGlite (pglite.wasm + JS glue), NOT the
# WASI pglite.wasi binary harmonyquery uses today. Output goes to
# pglite/build/out/emscripten/ for size comparison. For harmonyquery's vendored
# WASI tarball, use package-lean-from-vendored.sh (wasm-opt -O1).
#
# Prerequisites: Docker, git, optional binaryen (wasm-opt).
#
# Usage:
#   MAKE_J=2 ./pglite/build/build-lean.sh [output-dir]
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="${1:-$ROOT/pglite/build/out}"
WORK="$OUT/work"
EMS_OUT="$OUT/emscripten"
POSTGRES_PGLITE_REPO="${POSTGRES_PGLITE_REPO:-https://github.com/electric-sql/postgres-pglite.git}"
POSTGRES_PGLITE_REF="${POSTGRES_PGLITE_REF:-REL_17_5-pglite}"
MAKE_J="${MAKE_J:-2}"
DOCKER_MEMORY="${DOCKER_MEMORY:-8g}"

log() { printf '==> %s\n' "$*"; }

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }
}

need docker
need git

mkdir -p "$OUT" "$WORK" "$EMS_OUT"
cd "$WORK"

if [[ ! -d postgres-pglite/.git ]]; then
  log "cloning $POSTGRES_PGLITE_REPO @ $POSTGRES_PGLITE_REF"
  git clone --depth 1 --branch "$POSTGRES_PGLITE_REF" "$POSTGRES_PGLITE_REPO" postgres-pglite
else
  log "using existing postgres-pglite checkout"
fi

cp "$ROOT/pglite/build/build-pglite-lean.sh" postgres-pglite/build-pglite-lean.sh
chmod +x postgres-pglite/build-pglite-lean.sh

cd postgres-pglite

log "starting Docker Emscripten lean build (MAKE_J=$MAKE_J, memory=$DOCKER_MEMORY)"
export DEBUG="${DEBUG:-false}"

docker run --rm \
  -e DEBUG="${DEBUG}" \
  -e MAKE_J="${MAKE_J}" \
  --memory="${DOCKER_MEMORY}" \
  --workdir="$(pwd)" \
  -v "$(pwd):$(pwd):rw" \
  -v "$(pwd)/dist:/pglite:rw" \
  electricsql/pglite-builder:3.1.74-5-postgis-libicu-min \
  ./build-pglite-lean.sh

log "locating Emscripten artifacts"
for candidate in dist/bin/pglite.wasm dist/bin/pglite.js; do
  if [[ -f "$candidate" ]]; then
    cp "$candidate" "$EMS_OUT/"
    log "copied $candidate -> $EMS_OUT/"
  fi
done

if [[ -f "$EMS_OUT/pglite.wasm" ]]; then
  if wasm-opt --enable-bulk-memory "$WASMOPT_LEVEL" "$EMS_OUT/pglite.wasm" -o "$EMS_OUT/pglite.opt.wasm" 2>/dev/null; then
    log "wasm-opt ok: $(wc -c <"$EMS_OUT/pglite.opt.wasm") bytes"
  else
    log "wasm-opt skipped (bulk-memory validation failed on Emscripten artifact)"
    cp "$EMS_OUT/pglite.wasm" "$EMS_OUT/pglite.opt.wasm"
  fi
fi

log "Emscripten artifact sizes:"
ls -lh "$EMS_OUT"/* 2>/dev/null || true

log "harmonyquery WASI lean repackage (from current vendored tarball):"
"$ROOT/pglite/build/package-lean-from-vendored.sh" "$ROOT/pglite/pglite-wasi-17.tar.gz"

log "done; compare:"
"$ROOT/pglite/build/compare-size.sh"

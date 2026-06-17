#!/usr/bin/env bash
# Attempt a PostgreSQL 18 WASI build for harmonyquery's pglite.wasi integration.
#
# Electric ships PG 18 as Emscripten (pglite.wasm) only; there is no
# REL_18_3_WASM-pglite branch. This script grafts the PG 17 WASI build
# scaffolding (pglite-wasm/, wasm-build/) onto REL_18_3-pglite and runs the
# roastedroot/pglite4j Docker WASI pipeline.
#
# =============================================================================
# STATUS (2026-06-17): BLOCKED — not a simple patch rebase.
# =============================================================================
# Triaging the pglite4j REL_17_5 patch set against a clean REL_18_3-pglite tree
# (patch -p1 --dry-run) gives 9 conflicts of 17. The conflicts are NOT offset
# drift; they reflect an architectural divergence:
#
#   * REL_17_5_WASM-pglite does its socket/data exchange with INLINE edits to
#     src/backend/libpq/pqcomm.c (PqRecvBuffer_static, SOCKET_FILE, cma_rsize,
#     sockfiles, pq_recvbuf_fill) layered on the REL_17_5_WASM emscripten base.
#   * REL_18_3-pglite's pqcomm.c is VANILLA (no WASM/wasi/pglite markers). PG18
#     keeps its pglite changes behind __PGLITE__ in postgres.c (PostgresMainLoopOnce,
#     pgl_startPGlite, is_pglite_active) and overrides libc via -Dsend=pgl_send
#     compiler defines + pglite/src/pglitec/pglitec.c — the Emscripten model.
#   * The file-based WASI socket layer harmonyquery's bridge.go depends on
#     (PGS_IN/PGS_OUT files in wasm_common.h, sdk_port-wasi.c socket emulation)
#     is ABSENT from PG18: wasm_common.h and src/include/port/wasi.h do not exist.
#
# So making PG18 produce a pglite.wasi compatible with our wasmtime+file-socket
# bridge requires re-porting the entire REL_17_5 -> REL_17_5_WASM WASI platform
# layer (inline pqcomm socketfile machinery + WASI port headers + sdk_port-wasi)
# onto a PG18 tree that has diverged ~3400 commits, then reconciling with PG18's
# own __PGLITE__ changes. That is a multi-file port, not a patch, and is gated on
# runtime fragility we already hit on PG17 (wasm-opt -O2/-Oz trap pgl_backend).
#
# Patch triage (clean / already-applied / CONFLICT) against REL_18_3-pglite:
#   CLEAN     contrib-pgcrypto-Makefile, backend-commands-async,
#             interfaces-libpq-fe-misc, port-getopt, transam-xlog,
#             transam-xlogrecovery, bin-initdb-initdb, interfaces-libpq-fe-auth
#   CONFLICT  src-Makefile.shlib (hunk2 all-shared-lib — trivial rebase),
#             backend-storage-ipc-sinvaladt, bin-pg_dump-pg_dump,
#             include-port-wasi.h (file missing — needs full add),
#             interfaces-libpq-fe-connect, backend-libpq-pqcomm (ARCH BLOCKER),
#             backend-utils-error-elog, include-port-wasm_common.h (file missing),
#             port-pqsignal
#
# Recommended path forward: wait for / request an upstream REL_18_x_WASM-pglite
# (Electric) or a PG18 WASI artifact from pglite4j/pglite-oxide rather than
# maintaining a private fork of the WASM platform layer.
# =============================================================================
#
# On success, output lands in pglite/build/out/wasi-pg18/ and can be tested with:
#   PGLITE_WASI_TARBALL=$PWD/pglite/build/out/wasi-pg18/pglite-wasi-18.tar.gz \
#     PGLITE_EXPECT_VERSION=18 \
#     go test ./pglite/... -run TestPGlitePostgresVersion -v
#
# Usage:
#   ./pglite/build/build-wasi-pg18.sh
#
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT="$ROOT/pglite/build/out/wasi-pg18"
WORK="$OUT/work"
PGLITE4J="$WORK/pglite4j-wasm-build"
POSTGRES_REPO="${POSTGRES_REPO:-https://github.com/electric-sql/postgres-pglite.git}"
PG18_BRANCH="${PG18_BRANCH:-REL_18_3-pglite}"
PG18_VERSION="${PG18_VERSION:-18.3}"
WASM_SCAFFOLD_BRANCH="${WASM_SCAFFOLD_BRANCH:-REL_17_5_WASM-pglite}"
DEBUG="${DEBUG:-false}"
CMA_MB="${CMA_MB:-12}"
WASM_OPT_FLAGS="${WASM_OPT_FLAGS:--O1}"

log() { printf '==> %s\n' "$*"; }

need() {
  command -v "$1" >/dev/null 2>&1 || { echo "missing required tool: $1" >&2; exit 1; }
}

need docker
need git

mkdir -p "$OUT" "$WORK"

if [[ ! -f "$PGLITE4J/build.sh" ]]; then
  log "cloning roastedroot/pglite4j wasm-build scaffold"
  rm -rf "$WORK/pglite4j" "$PGLITE4J"
  git clone --depth 1 --filter=blob:none --sparse \
    https://github.com/roastedroot/pglite4j.git "$WORK/pglite4j"
  git -C "$WORK/pglite4j" sparse-checkout set wasm-build
  mv "$WORK/pglite4j/wasm-build" "$PGLITE4J"
  rm -rf "$WORK/pglite4j"
fi

POSTGRES_SRC="$PGLITE4J/postgresql-src"
if [[ ! -d "$POSTGRES_SRC/.git" ]]; then
  log "cloning $POSTGRES_REPO @ $PG18_BRANCH"
  git clone --depth 1 --branch "$PG18_BRANCH" "$POSTGRES_REPO" "$POSTGRES_SRC"
else
  log "refreshing postgres-pglite @ $PG18_BRANCH"
  git -C "$POSTGRES_SRC" fetch --depth 1 origin "$PG18_BRANCH"
  git -C "$POSTGRES_SRC" checkout -f "$PG18_BRANCH"
  git -C "$POSTGRES_SRC" reset --hard "origin/$PG18_BRANCH"
fi

log "grafting WASI scaffolding from $WASM_SCAFFOLD_BRANCH"
git -C "$POSTGRES_SRC" fetch --depth 1 origin "$WASM_SCAFFOLD_BRANCH"
for path in pglite-wasm wasm-build wasm-build.sh; do
  git -C "$POSTGRES_SRC" checkout FETCH_HEAD -- "$path"
done

rm -f "$POSTGRES_SRC/postgresql-src.patched" \
  "$POSTGRES_SRC/postgresql-pglite-custom.patched" \
  "$PGLITE4J/pglite-wasm/pglite-wasm.patched" 2>/dev/null || true

OUTPUT_DIR="$PGLITE4J/output"
mkdir -p "$OUTPUT_DIR/sdk-build" "$OUTPUT_DIR/sdk-dist" "$OUTPUT_DIR/pglite" "$OUTPUT_DIR/pgdata"

log "starting Docker WASI build (PG $PG18_VERSION, DEBUG=$DEBUG)"
cd "$PGLITE4J"

# pglite4j ships an x86_64 wasi-sdk; force amd64 on Apple Silicon hosts.
if [[ "$(uname -m)" == "arm64" ]]; then
  sed -i.bak 's/docker build -t/docker build --platform linux\/amd64 -t/' build.sh
  sed -i.bak 's/docker run --rm/docker run --platform linux\/amd64 --rm/' build.sh
fi

DEBUG="$DEBUG" \
PG_VERSION="$PG18_VERSION" \
PG_BRANCH="$PG18_BRANCH" \
WASM_OPT_FLAGS="$WASM_OPT_FLAGS" \
CMA_MB="$CMA_MB" \
./build.sh

TARXZ="$OUTPUT_DIR/sdk-dist/pglite-wasi.tar.xz"
TARGZ="$OUT/pglite-wasi-18.tar.gz"
if [[ -f "$TARXZ" ]]; then
  log "converting $TARXZ -> $TARGZ (harmonyquery expects .tar.gz with tmp/ prefix)"
  STAGE="$(mktemp -d)"
  tar -xJf "$TARXZ" -C "$STAGE"
  WASM="$(find "$STAGE" -name 'pglite.wasi' | head -1)"
  if [[ -z "$WASM" ]]; then
    echo "pglite.wasi not found in build output" >&2
    exit 1
  fi
  PACK="$STAGE/pack"
  mkdir -p "$PACK/tmp/pglite/bin"
  cp "$WASM" "$PACK/tmp/pglite/bin/pglite.wasi"
  if [[ -d "$OUTPUT_DIR/pglite/share" ]]; then
    mkdir -p "$PACK/tmp/pglite"
    cp -R "$OUTPUT_DIR/pglite/share" "$PACK/tmp/pglite/"
    cp -R "$OUTPUT_DIR/pglite/lib" "$PACK/tmp/pglite/" 2>/dev/null || true
  fi
  tar -czf "$TARGZ" -C "$PACK" tmp
  rm -rf "$STAGE"
  log "built $TARGZ ($(wc -c <"$TARGZ") bytes)"
  log "test with:"
  log "  PGLITE_WASI_TARBALL=$TARGZ PGLITE_EXPECT_VERSION=18 go test ./pglite/... -run TestPGlitePostgresVersion -v"
else
  log "build finished but $TARXZ not found; inspect $OUTPUT_DIR/sdk-dist/"
  ls -la "$OUTPUT_DIR/sdk-dist/" 2>/dev/null || true
  exit 1
fi

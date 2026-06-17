#!/usr/bin/env bash
# Lean variant of postgres-pglite build-pglite.sh: skips contrib, pgcrypto, postgis, other_extensions.
# Used inside the pglite-builder Docker image by build-lean.sh.
set -euo pipefail

emcc --clear-cache

INSTALL_FOLDER=${INSTALL_FOLDER:-"/pglite"}

PGLITE_CFLAGS="-m32 -sWASM_BIGINT -fpic -sENVIRONMENT=node,web,worker -sSUPPORT_LONGJMP=emscripten -Wno-declaration-after-statement -Wno-macro-redefined -Wno-unused-function -Wno-missing-prototypes -Wno-incompatible-pointer-types"
if [ "$DEBUG" = true ]; then
  echo "pglite-lean: building debug version."
  PGLITE_CFLAGS="$PGLITE_CFLAGS -g -gsource-map --no-wasm-opt"
else
  echo "pglite-lean: building release version (no contrib/extra)."
  PGLITE_CFLAGS="$PGLITE_CFLAGS -O2"
  unset DEBUG
fi

pushd pglite/src/pglitec && emcc $PGLITE_CFLAGS -static -fpic -o pglitec.o -c pglitec.c && popd

PGLITE_CFLAGS="$PGLITE_CFLAGS \
-D__PGLITE__ \
-Dsystem=pgl_system -Dpopen=pgl_popen -Dpclose=pgl_pclose \
-Dgeteuid=pgl_geteuid -Dgetuid=pgl_getuid -Dgetpwuid=pgl_getpwuid \
-Dexit=pgl_exit \
-Dmunmap=pgl_munmap \
-Dfcntl=pgl_fcntl \
-Datexit=pgl_atexit \
-Dsetsockopt=pgl_setsockopt -Dgetsockopt=pgl_getsockopt -Dgetsockname=pgl_getsockname \
-Drecv=pgl_recv -Dsend=pgl_send -Dconnect=pgl_connect \
-Dpoll=pgl_poll \
-Dshmget=pgl_shmget -Dshmat=pgl_shmat -Dshmdt=pgl_shmdt -Dshmctl=pgl_shmctl \
-Dlongjmp=pgl_longjmp -Dsiglongjmp=pgl_siglongjmp"

PGLITE_LDFLAGS="-sWASM_BIGINT -sUSE_PTHREADS=0"
PGLITE_LDFLAGS_SL="-shared -sSIDE_MODULE=1 -Wno-unused-function"

EXPORTED_RUNTIME_METHODS="addFunction,removeFunction,FS,MEMFS,PROXYFS,callMain,ENV,UTF8ToString,stringToNewUTF8,stringToUTF8OnStack"
PGLITE_LDFLAGS_EX="\
-sINITIAL_MEMORY=64MB \
-sWASM_BIGINT \
-sSUPPORT_LONGJMP=emscripten \
-sFORCE_FILESYSTEM=1 \
-sUSE_PTHREADS=0 \
-sEXIT_RUNTIME=1 -sENVIRONMENT=node,web,worker \
-sMAIN_MODULE=2 -sMODULARIZE=1 -sEXPORT_ES6=1 \
-sEXPORT_NAME=Module -sALLOW_TABLE_GROWTH -sALLOW_MEMORY_GROWTH \
-sERROR_ON_UNDEFINED_SYMBOLS=0 \
-sEXPORTED_RUNTIME_METHODS=$EXPORTED_RUNTIME_METHODS \
-sINVOKE_RUN=0 \
-sEXPORTED_FUNCTIONS=_main,_fgets,_fputs,_pclose,_fopen,_fclose,_fflush,___errno_location,_strerror \
$(pwd)/pglite/src/pglitec/pglitec.o \
-lproxyfs.js"

CONFIGURE_PARAMS="\
ac_cv_exeext=.js \
--host wasm32-unknown-linux-gnu \
--disable-spinlocks \
--without-llvm \
--without-pam \
--disable-largefile \
--with-openssl=no \
--without-readline \
--with-icu \
--with-includes=$INSTALL_PREFIX/include:$INSTALL_PREFIX/include/libxml2 \
--with-libraries=$INSTALL_PREFIX/lib \
--with-uuid=ossp \
--with-zlib \
--with-libxml \
--with-libxslt \
--with-template=emscripten \
--prefix=$INSTALL_FOLDER"

REF_FILE="build-pglite-lean.sh"
CONFIG_STATUS="config.status"
RUN_CONFIGURE=false
if [ ! -f "$CONFIG_STATUS" ]; then RUN_CONFIGURE=true
elif [ "$REF_FILE" -nt "$CONFIG_STATUS" ]; then RUN_CONFIGURE=true
fi

if [ "$RUN_CONFIGURE" = true ]; then
  LDFLAGS=$PGLITE_LDFLAGS \
  LDFLAGS_SL=$PGLITE_LDFLAGS_SL \
  LDFLAGS_EX=$PGLITE_LDFLAGS_EX \
  ICU_CFLAGS="-I/install/libs/include" \
  ICU_LIBS="-L/install/libs/lib -licui18n -licuuc -licudata" \
  CFLAGS=${PGLITE_CFLAGS} emconfigure ./configure $CONFIGURE_PARAMS
fi

MAKE_J="${MAKE_J:-2}"
echo "pglite-lean: emmake make -j${MAKE_J} (core only, skipping contrib/extra)"

emmake make PORTNAME=emscripten -j"${MAKE_J}" || { echo 'error: emmake make core' ; exit 21; }
emmake make PORTNAME=emscripten install || { echo 'error: emmake make install' ; exit 23; }

emmake make PORTNAME=emscripten -j -C src/backend pglite-exported-functions || { echo 'error: exported-functions' ; exit 51; }

PGROOT=/pglite
PGPRELOAD="\
--preload-file $(pwd)/pglite/static/PGPASSFILE@/home/postgres/.pgpass \
--preload-file $(pwd)/pglite/static/empty@/pglite/bin/initdb \
--preload-file $(pwd)/pglite/static/empty@/pglite/bin/pg_dump \
--preload-file $(pwd)/pglite/static/empty@/pglite/bin/postgres \
--preload-file $PGROOT/share/postgresql@/pglite/share/postgresql \
--preload-file $PGROOT/lib/postgresql@/pglite/lib/postgresql \
--preload-file $(pwd)/pglite/static/password@/pglite/password \
--preload-file $(pwd)/pglite/static/empty@/pglite/pgstdin \
--preload-file $(pwd)/pglite/static/empty@/pglite/pgstdout \
--preload-file $(pwd)/pglite/static/locale-a@/pglite/locale-a \
--preload-file $(pwd)/pglite/static/minimal-icu/76.1@/pglite/icu"

PGLITE_EXPORTED_RUNTIME_METHODS="MEMFS,IDBFS,FS,PROXYFS,setValue,getValue,UTF8ToString,stringToNewUTF8,stringToUTF8OnStack,addFunction,removeFunction,callMain,ENV"
POSTGRES_PGLITE_FLAGS="\
-sSTACK_SIZE=8MB \
-sINITIAL_MEMORY=128MB \
-sIMPORTED_MEMORY=1 \
-sEXPORTED_RUNTIME_METHODS=$PGLITE_EXPORTED_RUNTIME_METHODS \
-sEXPORTED_FUNCTIONS=@/install/pglite/exported_functions.txt \
$PGPRELOAD \
-lnodefs.js -lidbfs.js"

POSTGRES_PGLITE_FLAGS="$PGLITE_CFLAGS $POSTGRES_PGLITE_FLAGS" emmake make PORTNAME=emscripten -C src/backend/ -j"${MAKE_J}" pglite || { echo 'error: pglite link' ; exit 61; }
emmake make PORTNAME=emscripten -C src/backend/ install-pglite || { echo 'error: install-pglite' ; exit 62; }

echo "pglite-lean: done"
ls -lh dist/bin/pglite.* 2>/dev/null || ls -lh /pglite/bin/pglite.* 2>/dev/null || true

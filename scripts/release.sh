#!/usr/bin/env bash
# release.sh — build a stripped, self-contained tarball.
#
# Output: dist/atomros2-tts_linux_<arch>.tar.gz
#
# Contains:
#   tts-server                       (stripped Go binary, rpath=$ORIGIN/lib)
#   lib/libsherpa-onnx-c-api.so
#   lib/libonnxruntime.so
#   README.md
#
# Does NOT contain the model — quick-install.sh downloads that separately
# from upstream Sherpa-ONNX so each release is small.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$SCRIPT_DIR"

# Detect target arch (override with TARGET_ARCH=arm64)
TARGET_ARCH="${TARGET_ARCH:-$(uname -m)}"
case "$TARGET_ARCH" in
    x86_64|amd64)  GOARCH=amd64; SO_DIR=x86_64-unknown-linux-gnu ;;
    aarch64|arm64) GOARCH=arm64; SO_DIR=aarch64-unknown-linux-gnu ;;
    *) echo "unsupported arch: $TARGET_ARCH" >&2; exit 1 ;;
esac
echo "→ building for linux/$GOARCH (lib dir: $SO_DIR)"

DIST="$SCRIPT_DIR/dist"
STAGE="$DIST/stage_$GOARCH"
rm -rf "$STAGE"
mkdir -p "$STAGE/lib" "$DIST"

# 1. Build stripped binary with rpath=$ORIGIN/lib so it finds bundled .so
#    -s -w     : strip symbol tables and DWARF
#    -trimpath : remove abs build paths
#    -extldflags rpath : C linker sets RUNPATH so the binary searches ./lib next to itself
echo "→ go build"
CGO_ENABLED=1 GOOS=linux GOARCH="$GOARCH" \
    go build -trimpath \
    -ldflags="-s -w -extldflags '-Wl,-rpath,\$ORIGIN/lib -Wl,--enable-new-dtags'" \
    -o "$STAGE/tts-server" .

# 2. Copy shared libs from the Go module cache
MOD_LIB="$(go env GOMODCACHE)/github.com/k2-fsa/sherpa-onnx-go-linux@$(go list -m -f '{{.Version}}' github.com/k2-fsa/sherpa-onnx-go-linux)/lib/$SO_DIR"
if [[ ! -d "$MOD_LIB" ]]; then
    echo "✗ module lib dir not found: $MOD_LIB" >&2
    exit 1
fi
echo "→ copying .so files from $MOD_LIB"
cp -v "$MOD_LIB/libsherpa-onnx-c-api.so" "$STAGE/lib/"
cp -v "$MOD_LIB/libonnxruntime.so"       "$STAGE/lib/"

# 2b. Clean RUNPATH so it only points to bundled libs.
# Without this the binary's RUNPATH includes the build host's Go module cache
# absolute path. The dynamic linker falls back to $ORIGIN/lib anyway when that
# path is absent on the target — but cleaning it removes the dev-host string
# from the binary and makes ldd output sane.
if command -v patchelf >/dev/null 2>&1; then
    echo "→ patchelf cleaning rpath to \$ORIGIN/lib"
    patchelf --remove-rpath "$STAGE/tts-server"
    patchelf --force-rpath --set-rpath '$ORIGIN/lib' "$STAGE/tts-server"
else
    echo "→ patchelf not installed — leaving fallback rpath (still works on target)"
fi

# 3. Add README
cp README.md "$STAGE/"

# 4. Tar gzip
TARBALL="$DIST/atomros2-tts_linux_${GOARCH}.tar.gz"
echo "→ packing $TARBALL"
tar -czf "$TARBALL" -C "$STAGE" .

# 5. Cleanup stage
rm -rf "$STAGE"

echo "✓ $(du -h "$TARBALL" | cut -f1)  $TARBALL"

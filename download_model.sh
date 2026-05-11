#!/usr/bin/env bash
# Manual fallback for model download.
# Normally you don't need this — running ./tts-server will auto-download
# on first start. Use this if you want to pre-stage the model offline.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MODELS_DIR="$SCRIPT_DIR/models"
NAME="sherpa-onnx-vits-zh-hf-fanchen-C"
URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/${NAME}.tar.bz2"

mkdir -p "$MODELS_DIR"
cd "$MODELS_DIR"

if [[ -d "$NAME" ]] && ls "$NAME"/*.onnx >/dev/null 2>&1; then
    echo "model already present at $MODELS_DIR/$NAME"
    exit 0
fi

echo "downloading $NAME (~150MB) ..."
curl -fL --progress-bar -o "${NAME}.tar.bz2" "$URL"

echo "extracting ..."
tar xjf "${NAME}.tar.bz2"
rm "${NAME}.tar.bz2"

echo "done. model at $MODELS_DIR/$NAME"
ls -la "$NAME"

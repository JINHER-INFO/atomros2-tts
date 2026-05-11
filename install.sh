#!/usr/bin/env bash
# install.sh — install tts-server as a systemd service.
#
# Copies binary + model to /opt/local-tts-server/, sets up systemd unit,
# starts and enables it. Works on any systemd-based Linux.

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
TARGET_DIR="${TARGET_DIR:-/opt/local-tts-server}"
SERVICE_NAME="${SERVICE_NAME:-local-tts}"
SERVICE_USER="${SERVICE_USER:-root}"
PORT="${PORT:-54087}"
PLAYER="${PLAYER:-aplay}"

if [[ $EUID -ne 0 ]]; then
    echo "→ re-execing with sudo"
    exec sudo TARGET_DIR="$TARGET_DIR" SERVICE_NAME="$SERVICE_NAME" \
              SERVICE_USER="$SERVICE_USER" PORT="$PORT" PLAYER="$PLAYER" "$0" "$@"
fi

if [[ ! -x "$SCRIPT_DIR/tts-server" ]]; then
    echo "✗ $SCRIPT_DIR/tts-server not found — run 'make build' first"
    exit 1
fi
if ! command -v systemctl >/dev/null 2>&1; then
    echo "✗ systemctl not found — this script only supports systemd hosts"
    exit 1
fi

echo "→ installing into $TARGET_DIR"
mkdir -p "$TARGET_DIR"
cp -v "$SCRIPT_DIR/tts-server" "$TARGET_DIR/"
if [[ -d "$SCRIPT_DIR/models" ]]; then
    mkdir -p "$TARGET_DIR/models"
    cp -rv "$SCRIPT_DIR/models/." "$TARGET_DIR/models/"
fi
chown -R "$SERVICE_USER":"$SERVICE_USER" "$TARGET_DIR"

# pick model dir
MODEL_DIR=$(find "$TARGET_DIR/models" -mindepth 1 -maxdepth 1 -type d | head -n1)
[[ -z "$MODEL_DIR" ]] && { echo "✗ no model directory found"; exit 1; }
echo "→ model: $MODEL_DIR"

UNIT="/etc/systemd/system/${SERVICE_NAME}.service"
echo "→ writing $UNIT"
cat > "$UNIT" <<EOF
[Unit]
Description=Local TTS Server (Sherpa-ONNX VITS)
After=sound.target network.target

[Service]
Type=simple
User=$SERVICE_USER
WorkingDirectory=$TARGET_DIR
ExecStart=$TARGET_DIR/tts-server -port $PORT -model-dir $MODEL_DIR -player $PLAYER
Restart=on-failure
RestartSec=3
# Audio access (PulseAudio sessions need this; harmless on bare ALSA)
Environment=XDG_RUNTIME_DIR=/run/user/0
SupplementaryGroups=audio

[Install]
WantedBy=multi-user.target
EOF

echo "→ systemctl daemon-reload"
systemctl daemon-reload
echo "→ systemctl enable --now $SERVICE_NAME"
systemctl enable --now "$SERVICE_NAME"

sleep 1
systemctl --no-pager --full status "$SERVICE_NAME" || true
echo
echo "✓ installed. logs: journalctl -u $SERVICE_NAME -f"
echo "  test: curl -X POST localhost:$PORT/speak -H 'Content-Type: application/json' -d '{\"text\":\"安裝成功\"}'"

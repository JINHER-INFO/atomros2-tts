#!/usr/bin/env bash
# quick-install.sh — download pre-compiled binary from GitHub Releases.
#
# Does NOT clone source. Pulls only:
#   - 1 binary tarball (~10MB) from latest release
#   - 1 model tarball  (~150MB) from upstream Sherpa-ONNX
#
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/JINHER-INFO/atomros2-tts/main/quick-install.sh | sudo bash
#
# Env overrides:
#   REPO        github repo                (default: JINHER-INFO/atomros2-tts)
#   VERSION     release tag                (default: latest)
#   TARGET_DIR  install location           (default: /opt/atomros2-tts)
#   PORT        HTTP port                  (default: 54087)
#   PLAYER      audio player binary        (default: aplay)
#   SERVICE     systemd service name       (default: atomros2-tts)

set -euo pipefail

REPO="${REPO:-JINHER-INFO/atomros2-tts}"
VERSION="${VERSION:-latest}"
TARGET_DIR="${TARGET_DIR:-/opt/atomros2-tts}"
PORT="${PORT:-54087}"
PLAYER="${PLAYER:-aplay}"
SERVICE="${SERVICE:-atomros2-tts}"

MODEL_NAME="vits-zh-hf-fanchen-C"
MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/${MODEL_NAME}.tar.bz2"

log()  { echo -e "\033[1;32m[install]\033[0m $*"; }
warn() { echo -e "\033[1;33m[install]\033[0m $*" >&2; }
die()  { echo -e "\033[1;31m[install]\033[0m $*" >&2; exit 1; }

# ───── re-exec under sudo if not root ─────
if [[ $EUID -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then
        log "re-execing with sudo"
        exec sudo REPO="$REPO" VERSION="$VERSION" TARGET_DIR="$TARGET_DIR" \
                  PORT="$PORT" PLAYER="$PLAYER" SERVICE="$SERVICE" bash -- "$0" "$@"
    fi
    die "must run as root (or with sudo)"
fi

# ───── arch detection ─────
case "$(uname -m)" in
    x86_64|amd64)  ARCH=amd64 ;;
    aarch64|arm64) ARCH=arm64 ;;
    *) die "unsupported arch: $(uname -m). Build from source: https://github.com/$REPO" ;;
esac
log "detected arch: $ARCH"

# ───── package manager ─────
PM=""
for cand in apt-get dnf yum pacman apk opkg zypper; do
    command -v "$cand" >/dev/null 2>&1 && PM="$cand" && break
done
log "package manager: ${PM:-none}"

pkg_install() {
    local pkg="$1"
    case "$PM" in
        apt-get) apt-get update -qq && apt-get install -y --no-install-recommends "$pkg" ;;
        dnf|yum) "$PM" install -y "$pkg" ;;
        pacman)  pacman -Sy --noconfirm "$pkg" ;;
        apk)     apk add --no-cache "$pkg" ;;
        opkg)    opkg update && opkg install "$pkg" ;;
        zypper)  zypper -n install "$pkg" ;;
        *)       warn "no pkg manager — install '$pkg' manually"; return 1 ;;
    esac
}

ensure() {
    local bin="$1" pkg="$2"
    command -v "$bin" >/dev/null 2>&1 && return 0
    log "installing $pkg"
    pkg_install "$pkg" || warn "could not install $pkg"
}

# ───── 1. minimal runtime deps (NO Go, NO git — we use pre-compiled) ─────
ensure curl  curl
ensure tar   tar
ensure bzip2 bzip2

PLAYER_BIN="$(echo "$PLAYER" | awk '{print $1}')"
case "$PLAYER_BIN" in
    aplay)  ensure aplay  alsa-utils ;;
    paplay) ensure paplay pulseaudio-utils ;;
    ffplay) ensure ffplay ffmpeg ;;
esac

# ───── 2. resolve release URL ─────
if [[ "$VERSION" == "latest" ]]; then
    URL_BASE="https://github.com/$REPO/releases/latest/download"
else
    URL_BASE="https://github.com/$REPO/releases/download/$VERSION"
fi
ASSET="atomros2-tts_linux_${ARCH}.tar.gz"
DOWNLOAD_URL="$URL_BASE/$ASSET"
log "binary asset: $DOWNLOAD_URL"

# ───── 3. download + extract binary ─────
mkdir -p "$TARGET_DIR"
TMP="$(mktemp -d)"
trap "rm -rf $TMP" EXIT

log "downloading binary tarball (~10MB)"
curl -fL --progress-bar -o "$TMP/$ASSET" "$DOWNLOAD_URL" \
    || die "download failed — check that release '$VERSION' exists at https://github.com/$REPO/releases"

log "extracting into $TARGET_DIR"
tar xzf "$TMP/$ASSET" -C "$TARGET_DIR"
chmod +x "$TARGET_DIR/tts-server"

# ───── 4. download model (~150MB, only if missing) ─────
mkdir -p "$TARGET_DIR/models"
if [[ ! -d "$TARGET_DIR/models/$MODEL_NAME" ]] || ! ls "$TARGET_DIR/models/$MODEL_NAME"/*.onnx >/dev/null 2>&1; then
    log "downloading TTS model (~150MB) — one-time"
    curl -fL --progress-bar -o "$TMP/${MODEL_NAME}.tar.bz2" "$MODEL_URL"
    log "extracting model"
    tar xjf "$TMP/${MODEL_NAME}.tar.bz2" -C "$TARGET_DIR/models"
else
    log "model already present at $TARGET_DIR/models/$MODEL_NAME"
fi

MODEL_DIR="$TARGET_DIR/models/$MODEL_NAME"
[[ -f "$MODEL_DIR/tokens.txt" ]] || die "model files missing in $MODEL_DIR"

# ───── 5. systemd service ─────
if ! command -v systemctl >/dev/null 2>&1; then
    warn "systemd not found — skipping service install. Run manually:"
    warn "  $TARGET_DIR/tts-server -port $PORT -model-dir $MODEL_DIR -player $PLAYER -skip-setup"
    exit 0
fi

UNIT="/etc/systemd/system/${SERVICE}.service"
log "writing $UNIT"
cat > "$UNIT" <<EOF
[Unit]
Description=AtomROS2 TTS Server (Sherpa-ONNX VITS, fanchen-C)
After=sound.target network.target

[Service]
Type=simple
User=root
WorkingDirectory=$TARGET_DIR
ExecStart=$TARGET_DIR/tts-server -port $PORT -model-dir $MODEL_DIR -player $PLAYER -skip-setup
Restart=on-failure
RestartSec=3
Environment=XDG_RUNTIME_DIR=/run/user/0
SupplementaryGroups=audio

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now "$SERVICE"

sleep 2
echo
systemctl --no-pager --full status "$SERVICE" | head -15 || true
echo
log "═══ INSTALL DONE ═══"
log "service: $SERVICE  (port $PORT)"
log "logs:    journalctl -u $SERVICE -f"
log "test:    curl -X POST localhost:$PORT/speak -H 'Content-Type: application/json' -d '{\"text\":\"安裝成功\"}'"
log "remove:  systemctl disable --now $SERVICE && rm $UNIT && rm -rf $TARGET_DIR"

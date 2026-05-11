#!/usr/bin/env bash
# bootstrap.sh — one-command setup for any Linux (Debian / Ubuntu / Kali / Yocto / Alpine).
#
# Does:
#   1. Detect distro & package manager
#   2. Install audio player (aplay/paplay/ffplay) if missing
#   3. Ensure tar + bzip2 + Go toolchain
#   4. go build → produce ./tts-server binary
#   5. Download the TTS model (~150MB) into ./models/
#
# After this you can just run: ./tts-server

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

MODEL_NAME="sherpa-onnx-vits-zh-hf-fanchen-C"
MODEL_URL="https://github.com/k2-fsa/sherpa-onnx/releases/download/tts-models/${MODEL_NAME}.tar.bz2"
MODELS_DIR="$SCRIPT_DIR/models"

log()  { echo -e "\033[1;32m[bootstrap]\033[0m $*"; }
warn() { echo -e "\033[1;33m[bootstrap]\033[0m $*" >&2; }
die()  { echo -e "\033[1;31m[bootstrap]\033[0m $*" >&2; exit 1; }

# ───── 1. detect package manager ─────
PM=""
if   command -v apt-get >/dev/null 2>&1; then PM=apt
elif command -v dnf     >/dev/null 2>&1; then PM=dnf
elif command -v yum     >/dev/null 2>&1; then PM=yum
elif command -v pacman  >/dev/null 2>&1; then PM=pacman
elif command -v apk     >/dev/null 2>&1; then PM=apk
elif command -v opkg    >/dev/null 2>&1; then PM=opkg   # Yocto / OpenWrt
elif command -v zypper  >/dev/null 2>&1; then PM=zypper
else                                            PM=none
fi
log "package manager: $PM"

SUDO=""
if [[ $EUID -ne 0 ]]; then
    if command -v sudo >/dev/null 2>&1; then SUDO="sudo"
    else warn "not root and no sudo — package installs will be skipped"; fi
fi

# ───── pkg helper ─────
pkg_install() {
    local pkg="$1"
    case "$PM" in
        apt)    $SUDO apt-get update -qq && $SUDO apt-get install -y --no-install-recommends "$pkg" ;;
        dnf)    $SUDO dnf install -y "$pkg" ;;
        yum)    $SUDO yum install -y "$pkg" ;;
        pacman) $SUDO pacman -Sy --noconfirm "$pkg" ;;
        apk)    $SUDO apk add --no-cache "$pkg" ;;
        opkg)   $SUDO opkg update && $SUDO opkg install "$pkg" ;;
        zypper) $SUDO zypper -n install "$pkg" ;;
        *)      warn "no package manager — please install '$pkg' manually"; return 1 ;;
    esac
}

ensure_bin() {
    local bin="$1" pkg="$2"
    if command -v "$bin" >/dev/null 2>&1; then
        log "[ok] $bin"
        return 0
    fi
    log "[missing] $bin — installing $pkg"
    if pkg_install "$pkg"; then
        command -v "$bin" >/dev/null 2>&1 && return 0
    fi
    warn "could not install $bin (pkg=$pkg). Continuing — fix manually if downstream steps fail."
    return 1
}

# ───── 2. core tools ─────
ensure_bin tar    tar    || true
ensure_bin bzip2  bzip2  || true
ensure_bin curl   curl   || true

# ───── 3. audio player (prefer aplay; on Yocto/Genio it's usually preinstalled) ─────
PLAYER=""
for cand in aplay paplay ffplay; do
    if command -v $cand >/dev/null 2>&1; then PLAYER=$cand; break; fi
done
if [[ -z "$PLAYER" ]]; then
    case "$PM" in
        apt)    ensure_bin aplay alsa-utils || true ;;
        dnf|yum|zypper) ensure_bin aplay alsa-utils || true ;;
        pacman) ensure_bin aplay alsa-utils || true ;;
        apk)    ensure_bin aplay alsa-utils || true ;;
        opkg)   ensure_bin aplay alsa-utils || true ;;
        *)      warn "install alsa-utils / pulseaudio-utils / ffmpeg manually" ;;
    esac
    for cand in aplay paplay ffplay; do
        command -v $cand >/dev/null 2>&1 && PLAYER=$cand && break
    done
fi
[[ -n "$PLAYER" ]] && log "[ok] player=$PLAYER" || warn "no audio player found — server will start but playback will fail"

# ───── 4. Go toolchain ─────
if ! command -v go >/dev/null 2>&1; then
    case "$PM" in
        apt)    $SUDO apt-get install -y golang-go ;;
        dnf|yum) $SUDO ${PM} install -y golang ;;
        pacman) $SUDO pacman -Sy --noconfirm go ;;
        apk)    $SUDO apk add --no-cache go ;;
        opkg)   $SUDO opkg install go || warn "no Go in opkg — download from https://go.dev/dl/" ;;
        *)      die "Go compiler missing — install from https://go.dev/dl/" ;;
    esac
fi
log "[ok] go $(go version | awk '{print $3}')"

# ───── 5. build binary ─────
log "go mod tidy ..."
go mod tidy
log "go build ..."
CGO_ENABLED=1 go build -trimpath -o tts-server .
log "[ok] built ./tts-server  ($(du -h tts-server | cut -f1))"

# ───── 6. download model ─────
mkdir -p "$MODELS_DIR"
if [[ -d "$MODELS_DIR/$MODEL_NAME" && -n "$(ls "$MODELS_DIR/$MODEL_NAME"/*.onnx 2>/dev/null || true)" ]]; then
    log "[ok] model already at $MODELS_DIR/$MODEL_NAME"
else
    log "downloading $MODEL_NAME (~150MB) ..."
    curl -fL --progress-bar -o "$MODELS_DIR/${MODEL_NAME}.tar.bz2" "$MODEL_URL"
    log "extracting ..."
    tar xjf "$MODELS_DIR/${MODEL_NAME}.tar.bz2" -C "$MODELS_DIR"
    rm "$MODELS_DIR/${MODEL_NAME}.tar.bz2"
    log "[ok] model at $MODELS_DIR/$MODEL_NAME"
fi

echo
log "═══ ALL DONE ═══"
log "start server:   ./tts-server"
log "test:           curl -X POST localhost:54087/speak -H 'Content-Type: application/json' -d '{\"text\":\"您好，測試成功\"}'"

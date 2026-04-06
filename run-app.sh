#!/bin/bash
set -e

# Chemin vers la racine du projet
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_ROOT"

# =========================
# Gestion des arguments CLI
# =========================
MODE="$1"  # 1 = GUI, 2 = TUI

if [ -n "$MODE" ] && [[ "$MODE" != "1" && "$MODE" != "2" ]]; then
    echo "⚠️ Argument invalide : $MODE"
    echo "💡 Utilisation :"
    echo "   ./script.sh 1  -> GUI"
    echo "   ./script.sh 2  -> TUI"
    echo "   ./script.sh    -> Choix interactif"
    exit 1
fi

# =========================
# Vérification credentials
# =========================
NEXUS_CONFIG_DIR="$HOME/.config/nexus-storage"
CLIENT_SECRET_REPO="nexus-daemon/client_secret.json"
CLIENT_SECRET_CONFIG="$NEXUS_CONFIG_DIR/client_secret.json"

if [ ! -f "$CLIENT_SECRET_REPO" ] && [ ! -f "$CLIENT_SECRET_CONFIG" ]; then
    echo "❌ ERREUR: Fichier client_secret.json introuvable."
    echo "💡 Il doit être présent dans $CLIENT_SECRET_REPO ou $CLIENT_SECRET_CONFIG"
    echo "💡 Télécharge-le depuis Google Cloud Console"
    exit 1
fi

echo "🧹 Nettoyage des anciens processus..."
fuser -k 1420/tcp 2>/dev/null || true
fuser -k 8081/tcp 2>/dev/null || true
pkill -f nexus-daemon 2>/dev/null || true
sleep 1

# =========================
# Config credentials (Sync si nécessaire)
# =========================
mkdir -p "$NEXUS_CONFIG_DIR"
BIN_DEST_RESOURCES="nexus-gui/src-tauri/bin"
mkdir -p "$BIN_DEST_RESOURCES"

if [ -f "$CLIENT_SECRET_REPO" ]; then
    echo "🔄 Synchronisation des credentials vers $NEXUS_CONFIG_DIR..."
    cp "$CLIENT_SECRET_REPO" "$CLIENT_SECRET_CONFIG"
    cp "$CLIENT_SECRET_REPO" "$BIN_DEST_RESOURCES/client_secret.json"
elif [ -f "$CLIENT_SECRET_CONFIG" ]; then
    echo "🔄 Utilisation des credentials de la config pour Tauri..."
    cp "$CLIENT_SECRET_CONFIG" "$BIN_DEST_RESOURCES/client_secret.json"
fi

# =========================
# Build Rust
# =========================
echo "🦀 Compilation Nexus Core..."
cargo build --package nexus-core

cp target/debug/libnexus_core.a nexus-daemon/
cp nexus-core/include/nexus_core.h nexus-daemon/
rm -f nexus-daemon/libnexus_core.so nexus-gui/src-tauri/bin/libnexus_core.so

# =========================
# Build Go daemon
# =========================
echo "🚀 Compilation daemon..."
cd nexus-daemon
go build -tags fts5 -o nexus-daemon .
cd ..

TRIPLE=$(rustc -vV | sed -n 's|host: ||p')
BIN_DEST="nexus-gui/src-tauri/bin"
mkdir -p "$BIN_DEST"
cp nexus-daemon/nexus-daemon "$BIN_DEST/nexus-daemon-$TRIPLE"

# Copie des sidecars ffmpeg et rclone depuis le système
FFMPEG_PATH=$(command -v ffmpeg)
if [ -n "$FFMPEG_PATH" ]; then
    cp "$FFMPEG_PATH" "$BIN_DEST/ffmpeg-$TRIPLE"
fi

RCLONE_PATH=$(command -v rclone)
if [ -n "$RCLONE_PATH" ]; then
    cp "$RCLONE_PATH" "$BIN_DEST/rclone-$TRIPLE"
fi

# =========================
# Vérif dépendances
# =========================
MISSING=""
command -v ffmpeg >/dev/null 2>&1 || MISSING="ffmpeg $MISSING"
command -v rclone >/dev/null 2>&1 || MISSING="rclone $MISSING"

if [ -n "$MISSING" ]; then
    echo "❌ Dépendances manquantes : $MISSING"
    exit 1
fi

echo "✅ Dépendances OK"

# =========================
# Choix du mode
# =========================
if [ -z "$MODE" ]; then
    echo "🖥️ Choix de l'interface..."
    echo "1) GUI (Tauri)"
    echo "2) TUI (Terminal)"
    read -p "Choisissez (1/2) : " MODE
fi

# =========================
# Lancement
# =========================
if [ "$MODE" == "2" ]; then
    echo "✨ Lancement TUI..."

    ./nexus-gui/src-tauri/bin/nexus-daemon-$TRIPLE > "$NEXUS_CONFIG_DIR/daemon.log" 2>&1 &
    DAEMON_PID=$!

    trap "kill $DAEMON_PID 2>/dev/null || true" EXIT
    sleep 2

    cd nexus-tui
    cargo run

    kill $DAEMON_PID 2>/dev/null || true

elif [ "$MODE" == "1" ]; then
    echo "✨ Lancement GUI..."

    cd nexus-gui

    if [ ! -d "node_modules" ]; then
        echo "📦 Installation npm..."
        npm install
    fi

    npm run tauri dev

else
    echo "❌ Choix invalide"
    exit 1
fi
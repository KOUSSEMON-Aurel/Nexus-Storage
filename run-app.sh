#!/bin/bash
set -e

# Chemin vers la racine du projet
PROJECT_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$PROJECT_ROOT"

if [ ! -f "nexus-daemon/client_secret.json" ]; then
    echo "❌ ERREUR: Fichier nexus-daemon/client_secret.json introuvable."
    echo "💡 Pour utiliser l'upload YouTube, veuillez télécharger vos identifiants depuis"
    echo "   Google Cloud Console (API & Services > Identifiants > Télécharger le JSON OAuth)"
    echo "   et placez le fichier sous le nom 'client_secret.json' dans le dossier 'nexus-daemon/'"
    exit 1
fi

echo "🧹 Nettoyage des anciens processus (ports 1420 & 8081)..."
fuser -k 1420/tcp 2>/dev/null || true
fuser -k 8081/tcp 2>/dev/null || true
# Explicitly kill all daemon variants to unlock binaries
pkill -f nexus-daemon 2>/dev/null || true
sleep 1 # Allow OS to unlock file handles

# Auto-copy credentials to stable config directory so the daemon can always find them
NEXUS_CONFIG_DIR="$HOME/.config/nexus-storage"
mkdir -p "$NEXUS_CONFIG_DIR"
cp "nexus-daemon/client_secret.json" "$NEXUS_CONFIG_DIR/client_secret.json"

echo "🦀 1. Compilation de Nexus Core (Rust)..."
cargo build --package nexus-core

echo "📂 2. Préparation de la librairie statique pour le daemon..."
# On utilise UNIQUEMENT la lib statique (.a) pour garantir un binaire standalone
cp target/debug/libnexus_core.a nexus-daemon/
# Suppression des anciennes libs dynamiques pour éviter toute confusion
rm -f nexus-daemon/libnexus_core.so nexus-gui/src-tauri/bin/libnexus_core.so

echo "🚀 3. Compilation du daemon (Go - Pure Static Link)..."
cd nexus-daemon
go build -tags fts5 -o nexus-daemon .
cd ..


# Préparation du dossier sidecar pour Tauri
TRIPLE=$(rustc -vV | sed -n 's|host: ||p')
BIN_DEST="nexus-gui/src-tauri/bin"
mkdir -p "$BIN_DEST"
cp nexus-daemon/nexus-daemon "$BIN_DEST/nexus-daemon-$TRIPLE"
# Plus besoin de copier le .so ici car tout est dans le binaire !

# 3.1 Check system dependencies
MISSING=""
command -v ffmpeg >/dev/null 2>&1 || MISSING="ffmpeg $MISSING"
command -v rclone >/dev/null 2>&1 || MISSING="rclone $MISSING"

if [ -n "$MISSING" ]; then
    echo "❌ DÉPENDANCES MANQUANTES : $MISSING"
    echo "💡 Veuillez les installer avec votre gestionnaire de paquets :"
    echo "   Ubuntu/Debian : sudo apt install $MISSING"
    echo "   Arch Linux    : sudo pacman -S $MISSING"
    echo "   Fedora        : sudo dnf install $MISSING"
    exit 1
fi
echo "✅ Dépendances système (ffmpeg, rclone) présentes."

echo "🖥️  4. Choix de l'interface..."
echo "1) GUI (Tauri + React/Vite) - Recommandé pour le confort"
echo "2) TUI (Terminal ) - Recommandé pour le monitoring & SSH"
read -p "Choisissez (1/2) : " choice

if [ "$choice" == "2" ]; then
    echo "✨ Lancement du TUI Nexus..."
    # On lance d'abord le daemon en arrière-plan en redirigeant sa sortie
    echo "📡 Démarrage du daemon (logs: $NEXUS_CONFIG_DIR/daemon.log)..."
    ./nexus-gui/src-tauri/bin/nexus-daemon-x86_64-unknown-linux-gnu > "$NEXUS_CONFIG_DIR/daemon.log" 2>&1 &
    DAEMON_PID=$!
    
    # Nettoyage automatique à la fermeture du script
    trap "kill $DAEMON_PID 2>/dev/null || true" EXIT
    
    # On attend que le daemon soit prêt
    sleep 2
    
    echo "📟 Lancement de l'interface Terminal..."
    cd nexus-tui
    cargo run
    
    # Nettoyage à la sortie
    kill $DAEMON_PID 2>/dev/null || true
else
    echo "✨ Lancement de Tauri (Frontend + Backend Sidecar)..."
    cd nexus-gui
    # On installe les dépendances si le dossier node_modules n'existe pas
    if [ ! -d "node_modules" ]; then
        echo "📦 Installation des dépendances npm..."
        npm install
    fi
    npm run tauri dev
fi

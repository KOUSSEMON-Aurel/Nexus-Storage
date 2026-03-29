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

# Auto-copy credentials to stable config directory so the daemon can always find them
NEXUS_CONFIG_DIR="$HOME/.config/nexus-storage"
mkdir -p "$NEXUS_CONFIG_DIR"
cp "nexus-daemon/client_secret.json" "$NEXUS_CONFIG_DIR/client_secret.json"

echo "🦀 1. Compilation de Nexus Core (Rust)..."
cargo build --package nexus-core

# Copier la lib partagée pour que le daemon Go puisse la trouver lors de sa compilation (si nécessaire)
echo "📂 2. Préparation de la librairie partagée pour le daemon..."
cp target/debug/libnexus_core.so nexus-daemon/

echo "🚀 3. Compilation du sidecar (Go daemon)..."
chmod +x nexus-gui/scripts/build-sidecar.sh
./nexus-gui/scripts/build-sidecar.sh

echo "🧹 Nettoyage des anciens processus (ports 1420 & 8081)..."
fuser -k 1420/tcp 2>/dev/null || true
fuser -k 8081/tcp 2>/dev/null || true
pkill -f nexus-daemon 2>/dev/null || true

echo "🖥️  4. Choix de l'interface..."
echo "1) GUI (Tauri + React/Vite) - Recommandé pour le confort"
echo "2) TUI (Terminal Pro) - Recommandé pour le monitoring & SSH"
read -p "Choisissez (1/2) : " choice

if [ "$choice" == "2" ]; then
    echo "✨ Lancement du TUI Nexus..."
    # On lance d'abord le daemon en arrière-plan
    echo "📡 Démarrage du daemon en arrière-plan..."
    ./nexus-gui/src-tauri/bin/nexus-daemon-x86_64-unknown-linux-gnu &
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

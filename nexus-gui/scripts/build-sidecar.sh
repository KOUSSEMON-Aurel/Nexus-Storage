#!/bin/bash

# Target triple for the sidecar
TARGET="x86_64-unknown-linux-gnu"
BIN_NAME="nexus-daemon"

# Absolute paths
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
DAEMON_DIR="$PROJECT_ROOT/nexus-daemon"
OUTPUT_DIR="$PROJECT_ROOT/nexus-gui/src-tauri/bin"

mkdir -p "$OUTPUT_DIR"

echo "🔨 Building $BIN_NAME for Tauri Sidecar ($TARGET)..."

# Build the daemon with RPATH to find .so in the same folder
cd "$DAEMON_DIR"
# On utilise le go du système maintenant
go build -ldflags="-linkmode external -extldflags '-Wl,-rpath,\$ORIGIN'" -o "$BIN_NAME" .

# Move and rename it to the sidecar path
cd -
mv "$DAEMON_DIR/$BIN_NAME" "$OUTPUT_DIR/$BIN_NAME-$TARGET"
cp "$DAEMON_DIR/libnexus_core.so" "$OUTPUT_DIR/"

# On cherche client_secret.json et nexus.db s'ils ne sont pas dans nexus-daemon/
[ -f "$DAEMON_DIR/client_secret.json" ] && cp "$DAEMON_DIR/client_secret.json" "$OUTPUT_DIR/" || cp "$PROJECT_ROOT/target/debug/bin/client_secret.json" "$OUTPUT_DIR/" 2>/dev/null || echo "⚠️ client_secret.json not found"
[ -f "$DAEMON_DIR/nexus.db" ] && cp "$DAEMON_DIR/nexus.db" "$OUTPUT_DIR/" || cp "$PROJECT_ROOT/nexus-gui/src-tauri/nexus.db" "$OUTPUT_DIR/" 2>/dev/null || echo "⚠️ nexus.db not found"

chmod +x "$OUTPUT_DIR/$BIN_NAME-$TARGET"

echo "✅ sidecar binary and .so created at $OUTPUT_DIR/"

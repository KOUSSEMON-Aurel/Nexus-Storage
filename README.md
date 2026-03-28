<div align="center">

# 🌌 Nexus-Storage

**Store anything on YouTube. Forever. For free.**

[![Rust Core](https://img.shields.io/badge/Core-Rust-orange)](nexus-core/)
[![Go Daemon](https://img.shields.io/badge/Daemon-Go-blue)](nexus-daemon/)
[![Tauri GUI](https://img.shields.io/badge/GUI-Tauri%20%2B%20React-cyan)](nexus-gui/)

A sophisticated, multi-language cloud storage system that uses YouTube as a backend — with a virtually unlimited capacity, strong encryption, and zero central servers.

</div>

---

## 🏗️ Architecture

| Layer             | Language             | Role                                                             |
| :---------------- | :------------------- | :--------------------------------------------------------------- |
| **Core Engine**   | Rust 🦀               | Pixel encoding, fountain codes, encryption, compression          |
| **System Daemon** | Go 🐹                 | Orchestration, virtual drive (FUSE/WinFSP), upload queue         |
| **GUI**           | TypeScript + Tauri 🖥️ | Desktop interface (drag-and-drop, progress, settings)            |
| **TUI**           | Rust + Ratatui ⌨️     | *Planned:* Terminal interactive UI (keyboard-driven, htop-style) |

---

## ✨ Features

- 🔒 **End-to-End Encryption**: XChaCha20-Poly1305 with Argon2id Key Derivation.
- 🎯 **Dual Encoding Mode**: **Tank** (4×4 B&W, indestructible) / **Density** (4K color, max capacity).
- ♻️ **Fountain Codes (RaptorQ)**: Recover your files even with up to 15% missing or corrupted data.
- 🔄 **Self-Healing Index**: Reconstruct your entire metadata catalogue from YouTube descriptions.
- 💾 **Native Virtual Drive**: Mount as `/tmp/nexus-drive` (or `Y:` on Windows) and drag-and-drop your files.
- ✂️ **Content-Addressable Deduplication**: Never upload the exact same file twice.
- 🎨 **Adaptive Compression**: Automatically selects `zstd`, `lz4`, `lzma`, or `store` depending on the file type.

---

## 🚀 How to Run (Development)

Follow these steps carefully to cleanly start Nexus-Storage without phantom processes blocking the ports.

### 1. 🧹 Full Cleanup (Stop previous instances)

Open a new terminal and run:

```bash
# 1. Kill any running daemon
pkill -f nexus-daemon

# 2. Kill the Vite development server (port 1420)
fuser -k 1420/tcp

# 3. Safely unmount the FUSE drive to prevent "transport endpoint not connected" errors
fusermount3 -u ~/NexusDrive
```

### 2. 🐹 Start the System Daemon (Backend)

Open a terminal and navigate to the `nexus-daemon` directory. You must expose the Rust core library so Go can find it.

```bash
cd nexus-daemon
mkdir -p ~/NexusDrive

# Adjust your PATH if your Go environment is custom (like inside the go_env folder)
export PATH="$PWD/../go_env/go/bin:$PATH"
export LD_LIBRARY_PATH="."
export CGO_ENABLED=1

# Compile and start the daemon
go build -o nexus-daemon .
./nexus-daemon -db nexus.db
```

*(Leave this terminal open!)*

### 3. 🖥️ Start the GUI (Tauri Desktop App)

Open **another** terminal, navigate to the GUI folder, and launch the Tauri desktop application:

```bash
cd nexus-gui

# Ensure dependencies are installed
npm install

# Start the Tauri Desktop App (this will compile the Rust desktop window)
npm run tauri dev
```

*(A native desktop window will open automatically once compilation finishes!)*

---

## 📁 Project Structure

```text
Nexus-Storage/
├── nexus-core/       # Rust library: encoding, crypto, compression
├── nexus-daemon/     # Go daemon: orchestration, FUSE, upload queue
├── nexus-gui/        # Tauri + React: desktop GUI
└── nexus-tui/        # Rust + Ratatui: terminal UI
```

---

## 🗺️ Roadmap

- [X] Phase 1: Core Engine (Rust)
- [X] Phase 2: System Daemon (Go) - **Functional (Upload, Download, FUSE)**
- [X] Phase 3: GUI (Tauri) - **Functional (Real-time tracking, Settings)**
- [X] Phase 4: TUI (Ratatui)
- [ ] Phase V2: Streaming, Steganography, Shared Folders...

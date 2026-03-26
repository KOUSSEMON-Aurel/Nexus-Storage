# Nexus-Storage

> Store anything on YouTube. Forever. For free.

A sophisticated, multi-language cloud storage system that uses YouTube as a backend — with a virtually unlimited capacity, strong encryption, and zero central servers.

## Architecture

| Layer | Language | Role |
|---|---|---|
| **Core Engine** | Rust 🦀 | Pixel encoding, fountain codes, encryption, compression |
| **System Daemon** | Go 🐹 | Orchestration, virtual drive (FUSE/WinFSP), upload queue |
| **GUI** | TypeScript + Tauri 🖥️ | Desktop interface (drag-and-drop, progress, settings) |
| **TUI** | Rust + Ratatui ⌨️ | Terminal interactive UI (keyboard-driven, htop-style) |

## Features

- 🔒 XChaCha20-Poly1305 end-to-end encryption
- 🎯 Dual encoding mode: **Tank** (4×4 B&W, indestructible) / **Density** (4K color, max capacity)
- ♻️ Fountain codes (RaptorQ) — recover from up to 15% corruption
- 🔄 Self-healing index — reconstruct catalogue from YouTube descriptions
- 💾 Virtual drive — mount as `Y:` on Windows or `/mnt/nexus` on Linux
- ✂️ Content-addressable deduplication — never upload the same file twice
- 🎨 Adaptive compression: `zstd`, `lz4`, `lzma`, or `store` per file type

## Project Structure

```
Nexus-Storage/
├── nexus-core/       # Rust library: encoding, crypto, compression
├── nexus-daemon/     # Go daemon: orchestration, FUSE, upload queue
├── nexus-gui/        # Tauri + React: desktop GUI
└── nexus-tui/        # Rust + Ratatui: terminal UI
```

## Roadmap

- [x] Phase 1: Core Engine (Rust)
- [ ] Phase 2: System Daemon (Go)
- [ ] Phase 3: GUI (Tauri)
- [ ] Phase 4: TUI (Ratatui)
- [ ] Phase V2: Streaming, Steganography, Shared Folders...

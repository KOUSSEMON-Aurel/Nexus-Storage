# Nexus Storage

Nexus Storage is a high-density, encrypted cloud storage system that utilizes the YouTube infrastructure as a persistent backend. By transforming binary data into secure video streams, it achieves virtually unlimited capacity with decentralized redundancy.

## System Architecture

The project is built on a multi-language stack designed for high performance and native desktop integration:

* **Nexus Core (Rust)**: The signal processing engine. It handles XChaCha20-Poly1305 authenticated encryption, RaptorQ (fountain code) error correction, and pixel-grid video encoding.
* **Nexus Daemon (Go)**: The orchestration layer. It manages the OAuth2 authentication lifecycle, maintains the local SQLite/FTS5 metadata index, and provides a Rclone server for native file manager integration.
* **Nexus GUI (Tauri/React)**: The desktop interface. It provides real-time telemetry, quota monitoring, and high-performance search capabilities through a unified dashboard.

## Technical Specifications

### Security & Integrity

* **Encryption**: All data is encrypted locally using XChaCha20-Poly1305 before leaving the machine. Pathnames and metadata are never exposed to the backend.
* **Deduplication**: SHA-256 content-addressable storage ensures that identical files are linked locally, saving bandwidth and API quota.
* **Resilience**: Automated manifest backups are sharded and uploaded to dedicated YouTube playlists, allowing for full disaster recovery from a naked Google account.

### Integration

* **Virtual Disk**: Native WebDAV support allows Nexus to be mounted as a local drive in Dolphin (KDE), Nautilus (GNOME), or Windows Explorer.
* **Search**: Integrated SQLite FTS5 (Full-Text Search) provides sub-millisecond discovery across thousands of indexed files.
* **Management**: A direct shortcut to YouTube Studio allows for convenient oversight of the underlying video shards.

## Installation

### Prerequisites

* **Go** (1.21+)
* **Rust** (Latest stable)
* **Node.js & npm** (v18+)
* **FFmpeg** (Required for video assembly)

### Setup

1. Place your Google Cloud `client_secret.json` in `~/.config/nexus-storage/` or the root of `nexus-daemon`.
2. Build the Core library:
   ```bash
   cd nexus-core && cargo build --release
   ```
3. Install GUI dependencies:
   ```bash
   cd nexus-gui && npm install
   ```

### Execution

Use the provided runner script for automatic sidecar compilation and application launch:

```bash
./run-app.sh
```

## Usage

Once the application is running:

1. Complete the Google OAuth2 authentication flow.
2. Use the **Connect** button in the sidebar to mount the virtual disk.
3. Manage files directly through your native file manager or the Nexus dashboard.

## Development

The project follows a sidecar architecture. The Go daemon must be compiled with the `fts5` build tag to enable search functionality:

```bash
go build -tags fts5 -o nexus-daemon .
```

---

*Nexus Storage: Decentralized Persistence through Video Encoding.*

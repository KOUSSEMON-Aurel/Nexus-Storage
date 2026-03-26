// nexus-core/src/types.rs
// Shared types used across all modules.

use thiserror::Error;

/// Encoding mode: trade-off between resilience and density.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EncodingMode {
    /// 4×4 black/white blocks — indestructible under *any* YouTube compression.
    Tank,
    /// 2×2 colored blocks — maximum data density, requires 4K download to decode.
    Density,
}

/// Compression algorithm used before pixel encoding.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CompressionLevel {
    /// Use zstd — best for text/source code.
    Zstd,
    /// Use lz4 — best for binary data (fast default).
    Lz4,
    /// Use lzma — maximum compression ratio (slow).
    Lzma,
    /// No compression — best for already-compressed files (.zip, .mp4, .jpg…).
    Store,
}

/// Global error type for nexus-core operations.
#[derive(Debug, Error)]
pub enum NexusError {
    #[error("I/O error: {0}")]
    Io(#[from] std::io::Error),

    #[error("Encryption error: {0}")]
    Crypto(String),

    #[error("Compression error: {0}")]
    Compress(String),

    #[error("Encoding error: {0}")]
    Encode(String),

    #[error("Decoding error: {0}")]
    Decode(String),

    #[error("Hash mismatch — data corrupted or tampered")]
    HashMismatch,
}

pub type NexusResult<T> = Result<T, NexusError>;

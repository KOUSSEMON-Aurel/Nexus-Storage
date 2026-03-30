// nexus-core/src/types.rs
// Shared types used across all modules.

use thiserror::Error;

/// Encoding mode: trade-off between resilience and density.
/// All modes use 4×4 Luma blocks for DCT alignment.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum EncodingMode {
    /// 2 levels (B&W) — maximum resilience (survives 360p).
    Base,
    /// 8 levels (Grayscale) — high density (requires 1080p+).
    High,
}

/// Compression algorithm used before pixel encoding.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum CompressionLevel {
    /// Use zstd (default for V2).
    Zstd,
    /// No compression.
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

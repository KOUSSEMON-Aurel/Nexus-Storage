// nexus-core/src/compress.rs
// Adaptive compression layer.
// Wraps zstd, lz4, lzma and "store" (no compression) behind a common API.
// The compression choice is embedded in the output header so the decoder
// can always decompress without needing user input.
//
// Header format (2 bytes):
//   [0]: algorithm tag (0x00=store, 0x01=lz4, 0x02=zstd, 0x03=lzma)
//   [1]: reserved (0x00)

use crate::types::{CompressionLevel, NexusError, NexusResult};
use infer;

const TAG_STORE: u8 = 0x00;
const TAG_LZ4: u8 = 0x01;
const TAG_ZSTD: u8 = 0x02;
const TAG_LZMA: u8 = 0x03;

/// Detect the best compression algorithm for `data` based on its content type.
pub fn detect_best_level(data: &[u8]) -> CompressionLevel {
    if let Some(kind) = infer::get(data) {
        let mime = kind.mime_type();
        if matches!(
            mime,
            "video/mp4" | "video/x-matroska" | "audio/mpeg" | "audio/aac"
                | "image/jpeg" | "image/png" | "image/webp"
                | "application/zip" | "application/gzip" | "application/x-rar"
                | "application/x-7z-compressed" | "application/x-bzip2"
        ) {
            return CompressionLevel::Store;
        }
    }
    // Default for Nexus 2.0: Zstd (very high ratio, fast enough)
    CompressionLevel::Zstd
}

/// Compress `data` with the specified (or auto-detected) algorithm.
pub fn compress(data: &[u8], level: Option<CompressionLevel>) -> NexusResult<Vec<u8>> {
    let level = level.unwrap_or_else(|| detect_best_level(data));
    let mut output = Vec::with_capacity(data.len() + 2);
    match level {
        CompressionLevel::Store => {
            output.push(TAG_STORE);
            output.push(0x00);
            output.extend_from_slice(data);
        }
        CompressionLevel::Zstd => {
            output.push(TAG_ZSTD);
            output.push(0x00);
            let compressed = zstd::encode_all(data, 3)
                .map_err(|e| NexusError::Compress(e.to_string()))?;
            output.extend_from_slice(&compressed);
        }
    }
    Ok(output)
}

/// Decompress bytes that start with the 2-byte header written by `compress()`.
pub fn decompress(data: &[u8]) -> NexusResult<Vec<u8>> {
    if data.len() < 2 {
        return Err(NexusError::Compress("compressed data too short".into()));
    }
    let tag = data[0];
    let payload = &data[2..];
    match tag {
        TAG_STORE => Ok(payload.to_vec()),
        TAG_LZ4 => lz4_flex::decompress_size_prepended(payload)
            .map_err(|e| NexusError::Compress(e.to_string())),
        TAG_ZSTD => zstd::decode_all(payload)
            .map_err(|e| NexusError::Compress(e.to_string())),
        TAG_LZMA => {
            let mut out = Vec::new();
            lzma_rs::lzma_decompress(&mut std::io::Cursor::new(payload), &mut out)
                .map_err(|e| NexusError::Compress(e.to_string()))?;
            Ok(out)
        }
        _ => Err(NexusError::Compress(format!("unknown compression tag: 0x{:02X}", tag))),
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn roundtrip(data: &[u8], level: CompressionLevel) {
        let compressed = compress(data, Some(level)).unwrap();
        let decompressed = decompress(&compressed).unwrap();
        assert_eq!(data, decompressed.as_slice());
    }

    #[test]
    fn test_all_compression_levels() {
        let data = b"NexusStorage is the best backup system ever built. ".repeat(100);
        roundtrip(&data, CompressionLevel::Store);
        roundtrip(&data, CompressionLevel::Zstd);
    }

    #[test]
    fn test_auto_detect_zip_uses_store() {
        let mut data = vec![0x50, 0x4B, 0x03, 0x04];
        data.extend(vec![0u8; 200]);
        assert_eq!(detect_best_level(&data), CompressionLevel::Store);
    }
}

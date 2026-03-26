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
/// Users can override this by specifying a `CompressionLevel` explicitly.
pub fn detect_best_level(data: &[u8]) -> CompressionLevel {
    if let Some(kind) = infer::get(data) {
        let mime = kind.mime_type();
        // Already-compressed formats → store (no compression)
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
    // Default: lz4 (fast, good ratio for binary), zstd for text handled below.
    // We use a simple heuristic: high byte diversity → likely binary → lz4.
    // Low byte diversity (text/code) → zstd.
    let sample = &data[..data.len().min(4096)];
    let unique_bytes = {
        let mut seen = [false; 256];
        for &b in sample {
            seen[b as usize] = true;
        }
        seen.iter().filter(|&&s| s).count()
    };
    if unique_bytes < 60 {
        CompressionLevel::Zstd // Text/source code
    } else {
        CompressionLevel::Lz4  // Binary data
    }
}

/// Compress `data` with the specified (or auto-detected) algorithm.
/// Returns bytes **with** the 2-byte header prepended.
pub fn compress(data: &[u8], level: Option<CompressionLevel>) -> NexusResult<Vec<u8>> {
    let level = level.unwrap_or_else(|| detect_best_level(data));
    let mut output = Vec::with_capacity(data.len() + 2);
    match level {
        CompressionLevel::Store => {
            output.push(TAG_STORE);
            output.push(0x00);
            output.extend_from_slice(data);
        }
        CompressionLevel::Lz4 => {
            output.push(TAG_LZ4);
            output.push(0x00);
            let compressed = lz4_flex::compress_prepend_size(data);
            output.extend_from_slice(&compressed);
        }
        CompressionLevel::Zstd => {
            output.push(TAG_ZSTD);
            output.push(0x00);
            let compressed = zstd::encode_all(data, 3)
                .map_err(|e| NexusError::Compress(e.to_string()))?;
            output.extend_from_slice(&compressed);
        }
        CompressionLevel::Lzma => {
            output.push(TAG_LZMA);
            output.push(0x00);
            let mut compressed = Vec::new();
            lzma_rs::lzma_compress(&mut std::io::Cursor::new(data), &mut compressed)
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
        let data = b"NexusStorage is the best backup system ever built. Let us repeat this. ".repeat(100);
        roundtrip(&data, CompressionLevel::Store);
        roundtrip(&data, CompressionLevel::Lz4);
        roundtrip(&data, CompressionLevel::Zstd);
        roundtrip(&data, CompressionLevel::Lzma);
    }

    #[test]
    fn test_auto_detect_zip_uses_store() {
        // Fake a ZIP header
        let mut data = vec![0x50, 0x4B, 0x03, 0x04];
        data.extend(vec![0u8; 200]);
        assert_eq!(detect_best_level(&data), CompressionLevel::Store);
    }

    #[test]
    fn test_auto_detect_text_uses_zstd() {
        let data = "fn main() { println!(\"hello world\"); }\n".repeat(200);
        assert_eq!(detect_best_level(data.as_bytes()), CompressionLevel::Zstd);
    }
}

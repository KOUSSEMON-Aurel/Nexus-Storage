// nexus-core/src/encoder.rs
// Converts raw bytes → video frames (PNG images) ready for FFmpeg.
//
// Two modes:
//  - Tank (4×4 blocks, B&W):  extremely robust, survives any YouTube compression.
//  - Density (2×2 blocks, 4-bit grayscale): 4× more data per frame, requires 4K.
//
// Frame layout (1280×720 for Tank, 3840×2160 for Density):
//  - First 8 pixels of frame 0 encode the total payload byte count (u64 LE)
//  - Subsequent pixels encode the actual payload
//
// Special "calibration frame" (always frame 0):
//  A 1-second mire pattern (alternating black/white stripes + known gray levels)
//  that the decoder reads to compute the compression matrix YouTube applied.
//  This makes the system resilient against *future* YouTube codec changes.

use image::{ImageBuffer, Luma, Rgb};
use rayon::prelude::*;
use std::path::Path;
use crate::types::{EncodingMode, NexusError, NexusResult};

// --- Tank mode constants (1280×720) ---
pub const TANK_WIDTH: u32 = 1280;
pub const TANK_HEIGHT: u32 = 720;
pub const TANK_BLOCK: u32 = 4; // 4×4 pixels per bit
pub const TANK_COLS: u32 = TANK_WIDTH / TANK_BLOCK;   // 320 bit-columns
pub const TANK_ROWS: u32 = TANK_HEIGHT / TANK_BLOCK;  // 180 bit-rows
pub const TANK_BITS_PER_FRAME: usize = (TANK_COLS * TANK_ROWS) as usize; // 57 600 bits
pub const TANK_BYTES_PER_FRAME: usize = TANK_BITS_PER_FRAME / 8; // 7 200 bytes

// --- Density mode constants (3840×2160) ---
pub const DENSITY_WIDTH: u32 = 3840;
pub const DENSITY_HEIGHT: u32 = 2160;
pub const DENSITY_BLOCK: u32 = 2; // 2×2 pixels per nibble (4 bits)
pub const DENSITY_COLS: u32 = DENSITY_WIDTH / DENSITY_BLOCK;   // 1920
pub const DENSITY_ROWS: u32 = DENSITY_HEIGHT / DENSITY_BLOCK;  // 1080
pub const DENSITY_NIBBLES_PER_FRAME: usize = (DENSITY_COLS * DENSITY_ROWS) as usize; // 2 073 600
pub const DENSITY_BYTES_PER_FRAME: usize = DENSITY_NIBBLES_PER_FRAME / 2; // 1 036 800 bytes (~1 MB/frame)

/// Encode a raw payload into a sequence of PNG frame files saved to `output_dir`.
/// Returns the number of frames written (excluding the calibration frame).
pub fn encode_to_frames(
    payload: &[u8],
    output_dir: &Path,
    mode: EncodingMode,
) -> NexusResult<usize> {
    std::fs::create_dir_all(output_dir)
        .map_err(NexusError::Io)?;

    // Frame 0: calibration frame
    let calib_path = output_dir.join("frame_000000.png");
    write_calibration_frame(&calib_path, mode)?;

    // Prepend 8-byte payload length header
    let payload_len = payload.len() as u64;
    let mut data = Vec::with_capacity(8 + payload.len());
    data.extend_from_slice(&payload_len.to_le_bytes());
    data.extend_from_slice(payload);

    let bytes_per_frame = match mode {
        EncodingMode::Tank => TANK_BYTES_PER_FRAME,
        EncodingMode::Density => DENSITY_BYTES_PER_FRAME,
    };

    let num_frames = (data.len() + bytes_per_frame - 1) / bytes_per_frame;

    // Write data frames in parallel (frame indices 1..=num_frames)
    (0..num_frames)
        .into_par_iter()
        .try_for_each(|i| -> NexusResult<()> {
            let start = i * bytes_per_frame;
            let end = (start + bytes_per_frame).min(data.len());
            let chunk = &data[start..end];

            let frame_path = output_dir.join(format!("frame_{:06}.png", i + 1));
            match mode {
                EncodingMode::Tank => write_tank_frame(chunk, &frame_path)?,
                EncodingMode::Density => write_density_frame(chunk, &frame_path)?,
            }
            Ok(())
        })?;

    Ok(num_frames)
}

/// Write the special calibration frame (frame 0).
/// Contains a known, deterministic pattern of grays so the decoder can
/// compute the exact transfer function YouTube applied during re-encoding.
fn write_calibration_frame(path: &Path, mode: EncodingMode) -> NexusResult<()> {
    let (w, h) = match mode {
        EncodingMode::Tank => (TANK_WIDTH, TANK_HEIGHT),
        EncodingMode::Density => (DENSITY_WIDTH, DENSITY_HEIGHT),
    };
    // 16 vertical bands of evenly-spaced gray levels (0, 17, 34, … 255)
    let mut img = ImageBuffer::new(w, h);
    let band_w = w / 16;
    for (x, y, pixel) in img.enumerate_pixels_mut() {
        let band = (x / band_w).min(15) as u8;
        let gray = band * 17; // 0, 17, 34, … 255
        // Encode the expected level also in the alpha channel for self-description
        *pixel = Luma([gray]);
    }
    img.save(path).map_err(|e| NexusError::Encode(e.to_string()))
}

/// Encode one chunk of bytes as a Tank-mode frame (1280×720, 4×4 B&W blocks).
fn write_tank_frame(data: &[u8], path: &Path) -> NexusResult<()> {
    let mut img: ImageBuffer<Luma<u8>, Vec<u8>> = ImageBuffer::new(TANK_WIDTH, TANK_HEIGHT);

    // Fill black
    for p in img.pixels_mut() {
        *p = Luma([0u8]);
    }

    let mut bit_idx = 0usize;
    for &byte in data {
        for bit_pos in 0..8usize {
            let bit = (byte >> bit_pos) & 1;
            let col = (bit_idx % TANK_COLS as usize) as u32;
            let row = (bit_idx / TANK_COLS as usize) as u32;
            if row < TANK_ROWS {
                let level = if bit == 1 { 255u8 } else { 0u8 };
                let px = col * TANK_BLOCK;
                let py = row * TANK_BLOCK;
                for dy in 0..TANK_BLOCK {
                    for dx in 0..TANK_BLOCK {
                        img.put_pixel(px + dx, py + dy, Luma([level]));
                    }
                }
            }
            bit_idx += 1;
        }
    }

    img.save(path).map_err(|e| NexusError::Encode(e.to_string()))
}

/// Encode one chunk of bytes as a Density-mode frame (3840×2160, 2×2, 4-bit).
/// Each 2×2 block encodes a nibble (4 bits) as one of 16 gray levels.
fn write_density_frame(data: &[u8], path: &Path) -> NexusResult<()> {
    let mut img: ImageBuffer<Luma<u8>, Vec<u8>> = ImageBuffer::new(DENSITY_WIDTH, DENSITY_HEIGHT);

    let mut nibble_idx = 0usize;
    for &byte in data {
        for nibble_pos in 0..2usize {
            let nibble = if nibble_pos == 0 { byte & 0x0F } else { byte >> 4 };
            let col = (nibble_idx % DENSITY_COLS as usize) as u32;
            let row = (nibble_idx / DENSITY_COLS as usize) as u32;
            if row < DENSITY_ROWS {
                let level = nibble * 17; // Map 0–15 → 0, 17, 34, … 255
                let px = col * DENSITY_BLOCK;
                let py = row * DENSITY_BLOCK;
                for dy in 0..DENSITY_BLOCK {
                    for dx in 0..DENSITY_BLOCK {
                        img.put_pixel(px + dx, py + dy, Luma([level]));
                    }
                }
            }
            nibble_idx += 1;
        }
    }

    img.save(path).map_err(|e| NexusError::Encode(e.to_string()))
}

#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn test_tank_roundtrip_via_images() {
        // Encode a small payload then decode using the decoder module
        use crate::decoder::decode_from_frames;
        let payload = b"Hello NexusStorage Tank Mode!";
        let dir = tempdir().unwrap();
        let n = encode_to_frames(payload, dir.path(), EncodingMode::Tank).unwrap();
        assert!(n >= 1);
        let recovered = decode_from_frames(dir.path(), EncodingMode::Tank).unwrap();
        assert_eq!(payload.as_slice(), recovered.as_slice());
    }

    #[test]
    fn test_density_roundtrip_via_images() {
        use crate::decoder::decode_from_frames;
        let payload: Vec<u8> = (0u8..=255).collect::<Vec<_>>().repeat(100);
        let dir = tempdir().unwrap();
        let n = encode_to_frames(&payload, dir.path(), EncodingMode::Density).unwrap();
        assert!(n >= 1);
        let recovered = decode_from_frames(dir.path(), EncodingMode::Density).unwrap();
        assert_eq!(payload.as_slice(), recovered.as_slice());
    }
}

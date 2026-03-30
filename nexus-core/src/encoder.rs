// nexus-core/src/encoder.rs
// Converts raw bytes → video frames (PNG images) ready for FFmpeg.
//
// Nexus 2.0 Unified Channel:
//  - All modes use 4×4 blocks to align with DCT boundaries (YouTube resilience).
//  - Luma-only (Grayscale) for maximum signal-to-noise ratio.
//
// Two modes:
//  - Base (4×4 B&W): Survive even 360p compression.
//  - High (4×4 8-level Gray): 3× density, requires 1080p+.
//

use image::{ImageBuffer, Luma};
use rayon::prelude::*;
use std::path::Path;
use crate::types::{EncodingMode, NexusError, NexusResult};

// --- Shared Constants ---
pub const BLOCK_SIZE: u32 = 4;

// --- Base mode (1280×720) ---
pub const BASE_WIDTH: u32 = 1280;
pub const BASE_HEIGHT: u32 = 720;
pub const BASE_COLS: u32 = BASE_WIDTH / BLOCK_SIZE;   // 320
pub const BASE_ROWS: u32 = BASE_HEIGHT / BLOCK_SIZE;  // 180
pub const BASE_BITS_PER_FRAME: usize = (BASE_COLS * BASE_ROWS) as usize; // 57 600 bits
pub const BASE_BYTES_PER_FRAME: usize = BASE_BITS_PER_FRAME / 8; // 7 200 bytes

// --- High mode (3840×2160) ---
pub const HIGH_WIDTH: u32 = 3840;
pub const HIGH_HEIGHT: u32 = 2160;
pub const HIGH_COLS: u32 = HIGH_WIDTH / BLOCK_SIZE;   // 960
pub const HIGH_ROWS: u32 = HIGH_HEIGHT / BLOCK_SIZE;  // 540
pub const HIGH_SYM_PER_FRAME: usize = (HIGH_COLS * HIGH_ROWS) as usize; // 518 400 symbols
// 3 bits per symbol (8 levels) fallback if no calibration, otherwise bit-depth varies.
pub const HIGH_BYTES_PER_FRAME: usize = (HIGH_SYM_PER_FRAME * 3) / 8; // 194 400 bytes

/// Encode a raw payload into a sequence of PNG frame files saved to `output_dir`.
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
        EncodingMode::Base => BASE_BYTES_PER_FRAME,
        EncodingMode::High => HIGH_BYTES_PER_FRAME,
    };

    let num_frames = (data.len() + bytes_per_frame - 1) / bytes_per_frame;

    // Write data frames in parallel
    (0..num_frames)
        .into_par_iter()
        .try_for_each(|i| -> NexusResult<()> {
            let start = i * bytes_per_frame;
            let end = (start + bytes_per_frame).min(data.len());
            let chunk = &data[start..end];

            let frame_path = output_dir.join(format!("frame_{:06}.png", i + 1));
            match mode {
                EncodingMode::Base => write_base_frame(chunk, &frame_path)?,
                EncodingMode::High => write_high_frame(chunk, &frame_path)?,
            }
            Ok(())
        })?;

    Ok(num_frames)
}

fn write_calibration_frame(path: &Path, mode: EncodingMode) -> NexusResult<()> {
    let (w, h) = match mode {
        EncodingMode::Base => (BASE_WIDTH, BASE_HEIGHT),
        EncodingMode::High => (HIGH_WIDTH, HIGH_HEIGHT),
    };
    let mut img = ImageBuffer::new(w, h);
    let band_w = w / 16;
    for (x, _y, pixel) in img.enumerate_pixels_mut() {
        let band = (x / band_w).min(15) as u8;
        let gray = band * 17; // 0, 17, 34, … 255
        *pixel = Luma([gray]);
    }
    img.save(path).map_err(|e| NexusError::Encode(e.to_string()))
}

/// Base Mode: 4x4 blocks, 1 bit per block (B&W).
fn write_base_frame(data: &[u8], path: &Path) -> NexusResult<()> {
    let mut img: ImageBuffer<Luma<u8>, Vec<u8>> = ImageBuffer::new(BASE_WIDTH, BASE_HEIGHT);

    let mut bit_idx = 0usize;
    for &byte in data {
        for bit_pos in 0..8 {
            let bit = (byte >> bit_pos) & 1;
            let col = (bit_idx % BASE_COLS as usize) as u32;
            let row = (bit_idx / BASE_COLS as usize) as u32;
            if row < BASE_ROWS {
                let level = if bit == 1 { 255 } else { 0 };
                fill_block(&mut img, col, row, level);
            }
            bit_idx += 1;
        }
    }
    img.save(path).map_err(|e| NexusError::Encode(e.to_string()))
}

/// High Mode: 4x4 blocks, 3 bits per block (8 levels).
fn write_high_frame(data: &[u8], path: &Path) -> NexusResult<()> {
    let mut img: ImageBuffer<Luma<u8>, Vec<u8>> = ImageBuffer::new(HIGH_WIDTH, HIGH_HEIGHT);

    let mut bit_buf = 0u64;
    let mut bits_in_buf = 0usize;
    let mut sym_idx = 0usize;

    for &byte in data {
        bit_buf |= (byte as u64) << bits_in_buf;
        bits_in_buf += 8;

        while bits_in_buf >= 3 {
            let symbol = (bit_buf & 0x07) as u8;
            let col = (sym_idx % HIGH_COLS as usize) as u32;
            let row = (sym_idx / HIGH_COLS as usize) as u32;
            
            if row < HIGH_ROWS {
                let level = symbol * 36; // Map 0-7 -> 0, 36, 72, 108, 144, 180, 216, 252 (clamped 255)
                let level = if level > 240 { 255 } else { level };
                fill_block(&mut img, col, row, level);
            }
            
            bit_buf >>= 3;
            bits_in_buf -= 3;
            sym_idx += 1;
        }
    }
    
    // Flush remaining bits if any
    if bits_in_buf > 0 && sym_idx < HIGH_SYM_PER_FRAME {
        let symbol = (bit_buf & 0x07) as u8;
        let col = (sym_idx % HIGH_COLS as usize) as u32;
        let row = (sym_idx / HIGH_COLS as usize) as u32;
        let level = symbol * 36;
        let level = if level > 240 { 255 } else { level };
        fill_block(&mut img, col, row, level);
    }

    img.save(path).map_err(|e| NexusError::Encode(e.to_string()))
}

fn fill_block(img: &mut ImageBuffer<Luma<u8>, Vec<u8>>, col: u32, row: u32, level: u8) {
    let px = col * BLOCK_SIZE;
    let py = row * BLOCK_SIZE;
    for dy in 0..BLOCK_SIZE {
        for dx in 0..BLOCK_SIZE {
            img.put_pixel(px + dx, py + dy, Luma([level]));
        }
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use tempfile::tempdir;

    #[test]
    fn test_base_roundtrip_via_images() {
        // Encode a small payload then decode using the decoder module
        use crate::decoder::decode_from_frames;
        let payload = b"Hello NexusStorage Base Mode!";
        let dir = tempdir().unwrap();
        let n = encode_to_frames(payload, dir.path(), EncodingMode::Base).unwrap();
        assert!(n >= 1);
        let recovered = decode_from_frames(dir.path(), EncodingMode::Base).unwrap();
        assert_eq!(payload.as_slice(), recovered.as_slice());
    }

    #[test]
    fn test_high_roundtrip_via_images() {
        use crate::decoder::decode_from_frames;
        let payload: Vec<u8> = (0u8..=255).collect::<Vec<_>>().repeat(100);
        let dir = tempdir().unwrap();
        let n = encode_to_frames(&payload, dir.path(), EncodingMode::High).unwrap();
        assert!(n >= 1);
        let recovered = decode_from_frames(dir.path(), EncodingMode::High).unwrap();
        assert_eq!(payload.as_slice(), recovered.as_slice());
    }
}

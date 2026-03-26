// nexus-core/src/decoder.rs
// Converts video frame PNG files back to the original payload bytes.
//
// Reading strategy per block (Tank mode):
//  - Sample 5 points in the 4×4 block (center + 4 cardinal neighbors at ±1)
//  - Average their luminance
//  - Compare to dynamic threshold computed from the calibration frame
//  - threshold > midpoint(calib_black, calib_white) → bit = 1, else → bit = 0
//
// This approach makes decoding robust even if YouTube shifted the brightness
// levels slightly with a future codec update.

use image::{open, ImageBuffer, Luma};
use rayon::prelude::*;
use std::path::Path;
use crate::encoder::{
    TANK_WIDTH, TANK_HEIGHT, TANK_BLOCK, TANK_COLS, TANK_ROWS, TANK_BYTES_PER_FRAME,
    DENSITY_WIDTH, DENSITY_HEIGHT, DENSITY_BLOCK, DENSITY_COLS, DENSITY_ROWS, DENSITY_BYTES_PER_FRAME,
};
use crate::types::{EncodingMode, NexusError, NexusResult};

/// Decode a payload from a directory of frame PNGs (as written by `encode_to_frames`).
/// Frame 0 is the calibration frame; frames 1..N are data frames.
pub fn decode_from_frames(frame_dir: &Path, mode: EncodingMode) -> NexusResult<Vec<u8>> {
    // Collect and sort frame paths
    let mut frame_paths: Vec<_> = std::fs::read_dir(frame_dir)
        .map_err(NexusError::Io)?
        .filter_map(|e| e.ok())
        .map(|e| e.path())
        .filter(|p| p.extension().map_or(false, |e| e == "png"))
        .collect();
    frame_paths.sort();

    if frame_paths.is_empty() {
        return Err(NexusError::Decode("no frames found in directory".into()));
    }

    // Frame 0: calibration frame — compute the dynamic threshold
    let threshold = read_calibration_threshold(&frame_paths[0])?;

    let bytes_per_frame = match mode {
        EncodingMode::Tank => TANK_BYTES_PER_FRAME,
        EncodingMode::Density => DENSITY_BYTES_PER_FRAME,
    };
    let data_frames = &frame_paths[1..];

    // Decode all data frames in parallel
    let frame_chunks: Vec<Vec<u8>> = data_frames
        .par_iter()
        .map(|path| -> NexusResult<Vec<u8>> {
            match mode {
                EncodingMode::Tank => read_tank_frame(path, threshold),
                EncodingMode::Density => read_density_frame(path, threshold),
            }
        })
        .collect::<NexusResult<Vec<_>>>()?;

    // Flatten all frame chunks into a single byte stream
    let mut raw: Vec<u8> = frame_chunks.into_iter().flatten().collect();

    // Read the 8-byte payload length header
    if raw.len() < 8 {
        return Err(NexusError::Decode("not enough bytes to read payload length header".into()));
    }
    let len_bytes: [u8; 8] = raw[..8].try_into().unwrap();
    let payload_len = u64::from_le_bytes(len_bytes) as usize;
    let payload = raw[8..(8 + payload_len).min(raw.len())].to_vec();

    if payload.len() != payload_len {
        return Err(NexusError::Decode(format!(
            "expected {} bytes but recovered only {}", payload_len, payload.len()
        )));
    }
    Ok(payload)
}

/// Read the calibration frame and compute the brightness threshold as the
/// midpoint between the dimmest and brightest gray bands observed.
/// YouTube's codec may shift the levels, but the *midpoint* stays stable.
fn read_calibration_threshold(path: &Path) -> NexusResult<u8> {
    let img = open(path)
        .map_err(|e| NexusError::Decode(e.to_string()))?
        .into_luma8();
    let mut min_lum = 255u8;
    let mut max_lum = 0u8;
    for p in img.pixels() {
        let l = p.0[0];
        if l < min_lum { min_lum = l; }
        if l > max_lum { max_lum = l; }
    }
    Ok(min_lum / 2 + max_lum / 2) // Midpoint
}

/// Decode a Tank-mode frame into bytes.
fn read_tank_frame(path: &Path, threshold: u8) -> NexusResult<Vec<u8>> {
    let img = open(path)
        .map_err(|e| NexusError::Decode(e.to_string()))?
        .into_luma8();

    let total_bits = (TANK_COLS * TANK_ROWS) as usize;
    let mut bits = Vec::with_capacity(total_bits);

    for row in 0..TANK_ROWS {
        for col in 0..TANK_COLS {
            let brightness = sample_block_tank(&img, col, row);
            bits.push(if brightness > threshold { 1u8 } else { 0u8 });
        }
    }

    Ok(bits_to_bytes(&bits))
}

/// Sample the brightness of a Tank block by averaging 5 points.
fn sample_block_tank(img: &ImageBuffer<Luma<u8>, Vec<u8>>, col: u32, row: u32) -> u8 {
    let center_x = col * TANK_BLOCK + TANK_BLOCK / 2;
    let center_y = row * TANK_BLOCK + TANK_BLOCK / 2;
    let points = [
        (center_x, center_y),
        (center_x.saturating_sub(1), center_y),
        (center_x + 1, center_y),
        (center_x, center_y.saturating_sub(1)),
        (center_x, center_y + 1),
    ];
    let sum: u32 = points.iter()
        .map(|&(x, y)| {
            let px = x.min(TANK_WIDTH - 1);
            let py = y.min(TANK_HEIGHT - 1);
            img.get_pixel(px, py).0[0] as u32
        })
        .sum();
    (sum / points.len() as u32) as u8
}

/// Decode a Density-mode frame (nibbles) into bytes.
fn read_density_frame(path: &Path, _threshold: u8) -> NexusResult<Vec<u8>> {
    let img = open(path)
        .map_err(|e| NexusError::Decode(e.to_string()))?
        .into_luma8();

    let total_nibbles = (DENSITY_COLS * DENSITY_ROWS) as usize;
    let mut nibbles = Vec::with_capacity(total_nibbles);

    for row in 0..DENSITY_ROWS {
        for col in 0..DENSITY_COLS {
            let px = col * DENSITY_BLOCK + DENSITY_BLOCK / 2;
            let py = row * DENSITY_BLOCK + DENSITY_BLOCK / 2;
            let lum = img.get_pixel(
                px.min(DENSITY_WIDTH - 1),
                py.min(DENSITY_HEIGHT - 1),
            ).0[0];
            // Reverse map: level 0–255 → nibble 0–15
            let nibble = ((lum as f32 / 17.0).round() as u8).min(15);
            nibbles.push(nibble);
        }
    }

    // Pair nibbles into bytes
    let mut bytes = Vec::with_capacity(nibbles.len() / 2);
    for chunk in nibbles.chunks(2) {
        if chunk.len() == 2 {
            bytes.push(chunk[0] | (chunk[1] << 4));
        }
    }
    Ok(bytes)
}

/// Convert a flat bit vector (MSB-first within each byte) into bytes.
fn bits_to_bytes(bits: &[u8]) -> Vec<u8> {
    bits.chunks(8)
        .map(|chunk| {
            let mut byte = 0u8;
            for (i, &bit) in chunk.iter().enumerate() {
                byte |= bit << i;
            }
            byte
        })
        .collect()
}

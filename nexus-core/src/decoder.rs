// nexus-core/src/decoder.rs
// Converts video frame PNG files back to the original payload bytes.
//
// Reading strategy for Nexus 2.0:
//  - All blocks are 4×4 (DCT-aligned).
//  - Sample 5 points in the block (center + 4 cardinal neighbors) and average.
//  - Base mode: Binary thresholding.
//  - High mode: 8-level bucketization.

use image::{open, ImageBuffer, Luma};
use rayon::prelude::*;
use std::path::Path;
use crate::encoder::{BASE_WIDTH, BASE_HEIGHT, HIGH_WIDTH, HIGH_HEIGHT, BLOCK_SIZE};
use crate::types::{EncodingMode, NexusError, NexusResult};

pub fn decode_from_frames(frame_dir: &Path, mode: EncodingMode) -> NexusResult<Vec<u8>> {
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

    // Frame 0: calibration frame (could be used for dynamic range, but midpoint is robust)
    let threshold = read_calibration_threshold(&frame_paths[0])?;

    let data_frames = &frame_paths[1..];
    let frame_chunks: Vec<Vec<u8>> = data_frames
        .par_iter()
        .map(|path| -> NexusResult<Vec<u8>> {
            match mode {
                EncodingMode::Base => read_base_frame(path, threshold),
                EncodingMode::High => read_high_frame(path),
            }
        })
        .collect::<NexusResult<Vec<_>>>()?;

    let raw: Vec<u8> = frame_chunks.into_iter().flatten().collect();

    if raw.len() < 8 {
        return Err(NexusError::Decode("missing payload length header".into()));
    }
    let len_bytes: [u8; 8] = raw[..8].try_into().unwrap();
    let payload_len = u64::from_le_bytes(len_bytes) as usize;
    let payload = raw[8..(8 + payload_len).min(raw.len())].to_vec();

    if payload.len() != payload_len {
        return Err(NexusError::Decode(format!("corrupt payload: expected {}, got {}", payload_len, payload.len())));
    }
    Ok(payload)
}

pub fn decode_frame(img: &ImageBuffer<Luma<u8>, Vec<u8>>, mode: EncodingMode) -> NexusResult<Vec<u8>> {
    let threshold = 128; // Default robust midpoint, or we could pass it
    match mode {
        EncodingMode::Base => decode_base_image(img, threshold),
        EncodingMode::High => decode_high_image(img),
    }
}

fn read_calibration_threshold(path: &Path) -> NexusResult<u8> {
    let img = open(path).map_err(|e| NexusError::Decode(e.to_string()))?.into_luma8();
    let mut min_lum = 255u8;
    let mut max_lum = 0u8;
    for p in img.pixels() {
        let l = p.0[0];
        if l < min_lum { min_lum = l; }
        if l > max_lum { max_lum = l; }
    }
    Ok(min_lum / 2 + max_lum / 2)
}

fn read_base_frame(path: &Path, threshold: u8) -> NexusResult<Vec<u8>> {
    let img = open(path).map_err(|e| NexusError::Decode(e.to_string()))?.into_luma8();
    decode_base_image(&img, threshold)
}

fn decode_base_image(img: &ImageBuffer<Luma<u8>, Vec<u8>>, threshold: u8) -> NexusResult<Vec<u8>> {
    let (img_w, img_h) = img.dimensions();

    // Log warning if resolution doesn't match expected, so we catch YouTube downgrades
    if img_w != BASE_WIDTH || img_h != BASE_HEIGHT {
        eprintln!(
            "[nexus-core] WARNING: base frame resolution mismatch — expected {}x{}, got {}x{}. \
             FFmpeg may have not applied correct scaling.",
            BASE_WIDTH, BASE_HEIGHT, img_w, img_h
        );
    }

    // Compute actual col/row counts from real image dimensions
    let cols = img_w / BLOCK_SIZE;
    let rows = img_h / BLOCK_SIZE;
    let mut bits = Vec::with_capacity((cols * rows) as usize);

    for row in 0..rows {
        for col in 0..cols {
            let lum = sample_block(&img, col, row, img_w, img_h);
            bits.push(if lum > threshold { 1u8 } else { 0u8 });
        }
    }
    Ok(bits_to_bytes(&bits))
}

fn read_high_frame(path: &Path) -> NexusResult<Vec<u8>> {
    let img = open(path).map_err(|e| NexusError::Decode(e.to_string()))?.into_luma8();
    decode_high_image(&img)
}

fn decode_high_image(img: &ImageBuffer<Luma<u8>, Vec<u8>>) -> NexusResult<Vec<u8>> {
    let (img_w, img_h) = img.dimensions();

    // Log warning if resolution doesn't match expected
    if img_w != HIGH_WIDTH || img_h != HIGH_HEIGHT {
        eprintln!(
            "[nexus-core] WARNING: high frame resolution mismatch — expected {}x{}, got {}x{}. \
             FFmpeg may have not applied correct scaling.",
            HIGH_WIDTH, HIGH_HEIGHT, img_w, img_h
        );
    }

    // Compute actual col/row counts from real image dimensions
    let cols = img_w / BLOCK_SIZE;
    let rows = img_h / BLOCK_SIZE;
    let sym_per_frame = (cols * rows) as usize;
    let capacity = (sym_per_frame * 3) / 8;

    let mut bit_buf = 0u64;
    let mut bits_in_buf = 0usize;
    let mut bytes = Vec::with_capacity(capacity);

    for row in 0..rows {
        for col in 0..cols {
            let lum = sample_block(&img, col, row, img_w, img_h);

            // Map luminance to 3-bit symbol (8 levels)
            // Levels: 0, 36, 72, 108, 144, 180, 216, 255
            let symbol = if lum > 235 {
                7u8
            } else {
                ((lum as u32 + 18) / 36) as u8
            }.min(7);

            bit_buf |= (symbol as u64) << bits_in_buf;
            bits_in_buf += 3;

            while bits_in_buf >= 8 {
                bytes.push((bit_buf & 0xFF) as u8);
                bit_buf >>= 8;
                bits_in_buf -= 8;
            }
        }
    }
    Ok(bytes)
}

fn sample_block(img: &ImageBuffer<Luma<u8>, Vec<u8>>, col: u32, row: u32, w: u32, h: u32) -> u8 {
    let cx = col * BLOCK_SIZE + BLOCK_SIZE / 2;
    let cy = row * BLOCK_SIZE + BLOCK_SIZE / 2;
    let points = [(cx, cy), (cx-1, cy), (cx+1, cy), (cx, cy-1), (cx, cy+1)];
    let sum: u32 = points.iter().map(|&(x, y)| {
        img.get_pixel(x.min(w-1), y.min(h-1)).0[0] as u32
    }).sum();
    (sum / 5) as u8
}

fn bits_to_bytes(bits: &[u8]) -> Vec<u8> {
    bits.chunks(8).map(|chunk| {
        let mut byte = 0u8;
        for (i, &bit) in chunk.iter().enumerate() {
            byte |= bit << i;
        }
        byte
    }).collect()
}

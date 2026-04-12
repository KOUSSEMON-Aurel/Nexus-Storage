// nexus-core/src/streaming.rs
// Hardened Streaming Pipeline for Nexus Storage.
// Implements a secure chunked AEAD (STREAM construction) to prevent
// chunk reordering, deletion, or truncation.

use chacha20poly1305::{
    aead::{Aead, KeyInit},
    XChaCha20Poly1305, XNonce,
};
use std::convert::TryInto;
use crate::types::{NexusError, NexusResult};

pub const CHUNK_TAG_LEN: usize = 16;
pub const STREAM_PREFIX_LEN: usize = 16; // Random prefix for nonces
pub const STREAM_NONCE_LEN: usize = 24;  // XChaCha20 nonce length
pub const STREAM_CHUNK_SIZE: usize = 64 * 1024; // 64KB AES-like blocks

/// Stateful handle for streaming encryption/decryption.
pub struct StreamingContext {
    cipher: XChaCha20Poly1305,
    nonce_prefix: [u8; STREAM_PREFIX_LEN],
    counter: u64,
    is_finalized: bool,
    mode: StreamMode,
    buffer: Vec<u8>,
}

#[derive(PartialEq)]
enum StreamMode {
    Encrypt,
    Decrypt,
}

impl StreamingContext {
    /// Initialize a new encryption context.
    /// Returns (context, header) where header contains salt and nonce prefix.
    pub fn new_encrypt(key: &[u8; 32]) -> Self {
        let mut nonce_prefix = [0u8; STREAM_PREFIX_LEN];
        rand::thread_rng().fill_bytes(&mut nonce_prefix);

        let cipher = XChaCha20Poly1305::new(key.into());

        Self {
            cipher,
            nonce_prefix,
            counter: 0,
            is_finalized: false,
            mode: StreamMode::Encrypt,
            buffer: Vec::with_capacity(STREAM_CHUNK_SIZE * 2),
        }
    }

    /// Initialize a new decryption context with a known nonce prefix.
    pub fn new_decrypt(key: &[u8; 32], nonce_prefix: [u8; STREAM_PREFIX_LEN]) -> Self {
        let cipher = XChaCha20Poly1305::new(key.into());

        Self {
            cipher,
            nonce_prefix,
            counter: 0,
            is_finalized: false,
            mode: StreamMode::Decrypt,
            buffer: Vec::with_capacity(STREAM_CHUNK_SIZE * 2),
        }
    }

    pub fn nonce_prefix(&self) -> [u8; STREAM_PREFIX_LEN] {
        self.nonce_prefix
    }

    /// Update encryption. Buffers and encrypts in CHUNK_SIZE chunks.
    pub fn encrypt_update(&mut self, plaintext: &[u8]) -> NexusResult<Vec<u8>> {
        if self.mode != StreamMode::Encrypt || self.is_finalized {
            return Err(NexusError::Crypto("Invalid state for encryption".into()));
        }

        self.buffer.extend_from_slice(plaintext);
        let mut out = Vec::new();

        while self.buffer.len() >= STREAM_CHUNK_SIZE {
            let chunk: Vec<u8> = self.buffer.drain(0..STREAM_CHUNK_SIZE).collect();
            let nonce = self.generate_nonce(false);
            let ciphertext = self.cipher.encrypt(&nonce, chunk.as_slice())
                .map_err(|e| NexusError::Crypto(e.to_string()))?;
            
            let len = ciphertext.len() as u32;
            out.extend_from_slice(&len.to_le_bytes());
            out.extend_from_slice(&ciphertext);
            self.counter += 1;
        }

        Ok(out)
    }

    /// Update decryption. Dynamically reads 4-byte headers to extract framed chunks.
    pub fn decrypt_update(&mut self, ciphertext: &[u8]) -> NexusResult<Vec<u8>> {
        if self.mode != StreamMode::Decrypt || self.is_finalized {
            return Err(NexusError::Crypto("Invalid state for decryption".into()));
        }

        self.buffer.extend_from_slice(ciphertext);
        let mut out = Vec::new();

        loop {
            if self.buffer.len() < 4 {
                break;
            }

            let len_bytes: [u8; 4] = self.buffer[0..4].try_into().unwrap();
            let header = u32::from_le_bytes(len_bytes);
            let is_final = (header & 0x8000_0000) != 0;
            let chunk_len = (header & 0x7FFF_FFFF) as usize;

            if self.buffer.len() >= 4 + chunk_len {
                self.buffer.drain(0..4);
                let chunk: Vec<u8> = self.buffer.drain(0..chunk_len).collect();
                let nonce = self.generate_nonce(is_final);
                let plaintext = self.cipher.decrypt(&nonce, chunk.as_slice())
                    .map_err(|_| NexusError::Crypto("Auth failed — chunk corrupt or out of order".into()))?;
                out.extend_from_slice(&plaintext);
                self.counter += 1;
                
                if is_final {
                    self.is_finalized = true;
                    // We can safely ignore any remaining trailing zeroes (PNG padding)
                    self.buffer.clear();
                    break;
                }
            } else {
                break;
            }
        }

        Ok(out)
    }

    /// Finalize encryption. Encrypts whatever remains in the buffer.
    pub fn finalize_encrypt(&mut self, last_plaintext: &[u8]) -> NexusResult<Vec<u8>> {
        if self.mode != StreamMode::Encrypt || self.is_finalized {
            return Err(NexusError::Crypto("Already finalized or wrong mode".into()));
        }

        self.buffer.extend_from_slice(last_plaintext);
        let chunk: Vec<u8> = self.buffer.drain(..).collect();
        let nonce = self.generate_nonce(true);
        let ciphertext = self.cipher.encrypt(&nonce, chunk.as_slice())
            .map_err(|e| NexusError::Crypto(e.to_string()))?;

        let mut len = ciphertext.len() as u32;
        len |= 0x8000_0000; // Flag as final chunk
        
        let mut out = Vec::new();
        out.extend_from_slice(&len.to_le_bytes());
        out.extend_from_slice(&ciphertext);

        self.is_finalized = true;
        Ok(out)
    }

    /// Finalize decryption. Relies on internal state framing.
    pub fn finalize_decrypt(&mut self, last_ciphertext: &[u8]) -> NexusResult<Vec<u8>> {
        if self.is_finalized && last_ciphertext.is_empty() {
            return Ok(Vec::new());
        }

        let out = self.decrypt_update(last_ciphertext)?;
        
        if !self.is_finalized {
            return Err(NexusError::Crypto("Auth failed on final chunk — truncation suspected".into()));
        }
        
        Ok(out)
    }

    fn generate_nonce(&self, is_final: bool) -> XNonce {
        let mut nonce_bytes = [0u8; STREAM_NONCE_LEN];
        nonce_bytes[..STREAM_PREFIX_LEN].copy_from_slice(&self.nonce_prefix);
        
        let mut ctr = self.counter;
        if is_final {
            // Set the high bit to signal the end of the stream.
            // This prevents an attacker from truncating the file without detection.
            ctr |= 1 << 63;
        }
        
        nonce_bytes[STREAM_PREFIX_LEN..].copy_from_slice(&ctr.to_be_bytes());
        *XNonce::from_slice(&nonce_bytes)
    }
}

// --- Streaming Encoder ---

use crate::encoder::{
    BASE_BYTES_PER_FRAME, HIGH_BYTES_PER_FRAME,
    BASE_WIDTH, BASE_HEIGHT, HIGH_WIDTH, HIGH_HEIGHT,
};
use crate::types::EncodingMode;
use image::{ImageBuffer, Luma};

/// Stateful PNG encoder for streaming data into frames.
pub struct StreamingEncoder {
    buffer: Vec<u8>,
    bytes_per_frame: usize,
    mode: EncodingMode,
    frame_count: usize,
    frame_queue: Vec<Vec<u8>>,
}

impl StreamingEncoder {
    pub fn new(mode: EncodingMode) -> Self {
        let bytes_per_frame = match mode {
            EncodingMode::Base => BASE_BYTES_PER_FRAME,
            EncodingMode::High => HIGH_BYTES_PER_FRAME,
        };

        Self {
            buffer: Vec::with_capacity(bytes_per_frame),
            bytes_per_frame,
            mode,
            frame_count: 0,
            frame_queue: Vec::new(),
        }
    }

    /// Push data into the encoder. 
    /// Frames are stored in the internal queue.
    pub fn push_data(&mut self, data: &[u8]) -> NexusResult<usize> {
        let mut frames_added = 0;
        let mut remaining = data;

        while !remaining.is_empty() {
            let space = self.bytes_per_frame - self.buffer.len();
            let to_copy = remaining.len().min(space);
            self.buffer.extend_from_slice(&remaining[..to_copy]);
            remaining = &remaining[to_copy..];

            if self.buffer.len() == self.bytes_per_frame {
                let frame = self.generate_frame()?;
                self.frame_queue.push(frame);
                self.buffer.clear();
                frames_added += 1;
            }
        }

        Ok(frames_added)
    }

    /// Pop a generated frame from the queue.
    pub fn pop_frame(&mut self) -> Option<Vec<u8>> {
        if self.frame_queue.is_empty() {
            None
        } else {
            Some(self.frame_queue.remove(0))
        }
    }

    /// Finalize the stream. Flushes remaining data.
    pub fn finalize(&mut self) -> NexusResult<()> {
        if self.buffer.is_empty() && self.frame_count > 0 {
            return Ok(()); 
        }
        
        if self.buffer.len() < self.bytes_per_frame {
            self.buffer.resize(self.bytes_per_frame, 0);
        }
        
        let frame = self.generate_frame()?;
        self.frame_queue.push(frame);
        Ok(())
    }

    fn generate_frame(&mut self) -> NexusResult<Vec<u8>> {
        let (w, h) = match self.mode {
            EncodingMode::Base => (BASE_WIDTH, BASE_HEIGHT),
            EncodingMode::High => (HIGH_WIDTH, HIGH_HEIGHT),
        };

        let mut img: ImageBuffer<Luma<u8>, Vec<u8>> = ImageBuffer::new(w, h);
        
        // Use the existing logic from encoder.rs for filling blocks.
        // Since we want to avoid code duplication, we'll re-use helper functions if possible,
        // but for now we'll implement the logic here to keep streaming.rs self-contained.
        match self.mode {
            EncodingMode::Base => self.fill_base_frame(&mut img)?,
            EncodingMode::High => self.fill_high_frame(&mut img)?,
        }

        self.frame_count += 1;

        let mut png_data = Vec::new();
        let mut cursor = std::io::Cursor::new(&mut png_data);
        img.write_to(&mut cursor, image::ImageFormat::Png)
            .map_err(|e| NexusError::Encode(e.to_string()))?;
        
        Ok(png_data)
    }

    fn fill_base_frame(&self, img: &mut ImageBuffer<Luma<u8>, Vec<u8>>) -> NexusResult<()> {
        use crate::encoder::{BASE_COLS, BASE_ROWS, BLOCK_SIZE};
        let mut bit_idx = 0usize;
        for &byte in &self.buffer {
            for bit_pos in 0..8 {
                let bit = (byte >> bit_pos) & 1;
                let col = (bit_idx % BASE_COLS as usize) as u32;
                let row = (bit_idx / BASE_COLS as usize) as u32;
                if row < BASE_ROWS {
                    let level = if bit == 1 { 255 } else { 0 };
                    self.fill_block(img, col, row, level, BLOCK_SIZE);
                }
                bit_idx += 1;
            }
        }
        Ok(())
    }

    fn fill_high_frame(&self, img: &mut ImageBuffer<Luma<u8>, Vec<u8>>) -> NexusResult<()> {
        use crate::encoder::{HIGH_COLS, HIGH_ROWS, BLOCK_SIZE};
        let mut bit_buf = 0u64;
        let mut bits_in_buf = 0usize;
        let mut sym_idx = 0usize;

        for &byte in &self.buffer {
            bit_buf |= (byte as u64) << bits_in_buf;
            bits_in_buf += 8;

            while bits_in_buf >= 3 {
                let symbol = (bit_buf & 0x7) as u8;
                let col = (sym_idx % HIGH_COLS as usize) as u32;
                let row = (sym_idx / HIGH_COLS as usize) as u32;
                if row < HIGH_ROWS {
                    let level = symbol * 36;
                    let level = if level > 240 { 255 } else { level };
                    self.fill_block(img, col, row, level, BLOCK_SIZE);
                }
                bit_buf >>= 3;
                bits_in_buf -= 3;
                sym_idx += 1;
            }
        }
        Ok(())
    }

    fn fill_block(&self, img: &mut ImageBuffer<Luma<u8>, Vec<u8>>, col: u32, row: u32, level: u8, block_size: u32) {
        let px = col * block_size;
        let py = row * block_size;
        for dy in 0..block_size {
            for dx in 0..block_size {
                img.put_pixel(px + dx, py + dy, Luma([level]));
            }
        }
    }
}

pub struct StreamingDecoder {
    mode: EncodingMode,
    buffer: Vec<u8>,
}

impl StreamingDecoder {
    pub fn new(mode: EncodingMode) -> Self {
        Self {
            mode,
            buffer: Vec::new(),
        }
    }

    pub fn push_frame(&mut self, frame_data: &[u8]) -> Result<(), NexusError> {
        let cursor = std::io::Cursor::new(frame_data);
        let img = image::load(cursor, image::ImageFormat::Png)
            .map_err(|e| NexusError::Decode(format!("Failed to load PNG frame: {}", e)))?
            .into_luma8();
        
        let mut decoded_chunk = crate::decoder::decode_frame(&img, self.mode)
            .map_err(|e| NexusError::Decode(format!("Frame decoding failed: {}", e)))?;
        
        self.buffer.append(&mut decoded_chunk);
        Ok(())
    }

    pub fn pop_data(&mut self) -> Vec<u8> {
        std::mem::take(&mut self.buffer)
    }
}

use rand::RngCore;

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_streaming_roundtrip() {
        let key = [0u8; 32];
        let mut enc = StreamingContext::new_encrypt(&key);
        let prefix = enc.nonce_prefix();

        let chunk1_plain = b"First chunk data";
        let chunk2_plain = b"Second chunk data";
        let last_plain = b"The end.";

        let c1 = enc.encrypt_chunk(chunk1_plain).unwrap();
        let c2 = enc.encrypt_chunk(chunk2_plain).unwrap();
        let c_final = enc.finalize_encrypt(last_plain).unwrap();

        let mut dec = StreamingContext::new_decrypt(&key, prefix);
        assert_eq!(dec.decrypt_chunk(&c1).unwrap(), chunk1_plain);
        assert_eq!(dec.decrypt_chunk(&c2).unwrap(), chunk2_plain);
        assert_eq!(dec.finalize_decrypt(&c_final).unwrap(), last_plain);
    }

    #[test]
    fn test_out_of_order_fails() {
        let key = [0u8; 32];
        let mut enc = StreamingContext::new_encrypt(&key);
        let prefix = enc.nonce_prefix();

        let _c1 = enc.encrypt_chunk(b"data 1").unwrap();
        let c2 = enc.encrypt_chunk(b"data 2").unwrap();

        let mut dec = StreamingContext::new_decrypt(&key, prefix);
        // Try decrypting chunk 2 before chunk 1
        assert!(dec.decrypt_chunk(&c2).is_err());
    }

    #[test]
    fn test_truncation_fails() {
        let key = [0u8; 32];
        let mut enc = StreamingContext::new_encrypt(&key);
        let prefix = enc.nonce_prefix();

        let c1 = enc.encrypt_chunk(b"chunk 1").unwrap();
        let _c_final = enc.finalize_encrypt(b"last").unwrap();

        let mut dec = StreamingContext::new_decrypt(&key, prefix);
        // Trying to finalize with a regular chunk should fail if it was encrypted as non-final
        assert!(dec.finalize_decrypt(&c1).is_err());
    }
}

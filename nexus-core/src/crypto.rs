// nexus-core/src/crypto.rs
// End-to-end encryption using XChaCha20-Poly1305 (libsodium-compatible).
// Key derivation from password uses Argon2id (memory-hard, GPU-resistant).
//
// Encrypted output layout:
//   [0..16]  — random salt (for Argon2id key derivation)
//   [16..40] — random nonce (24 bytes, XChaCha20)
//   [40..]   — ciphertext + 16-byte Poly1305 authentication tag

use chacha20poly1305::{
    aead::{Aead, KeyInit},
    XChaCha20Poly1305, XNonce,
};
use argon2::{Argon2, Params, Version};
use rand::RngCore;
use crate::types::{NexusError, NexusResult};

const SALT_LEN: usize = 16;
const NONCE_LEN: usize = 24; // XChaCha20 uses 192-bit nonces
const KEY_LEN: usize = 32;   // 256-bit key

/// Derive a 256-bit encryption key from a user password and a random salt
/// using Argon2id (strong against GPU and side-channel attacks).
fn derive_key(password: &str, salt: &[u8]) -> NexusResult<[u8; KEY_LEN]> {
    let params = Params::new(64 * 1024, 3, 1, Some(KEY_LEN))
        .map_err(|e| NexusError::Crypto(e.to_string()))?;
    let argon2 = Argon2::new(argon2::Algorithm::Argon2id, Version::V0x13, params);
    let mut key = [0u8; KEY_LEN];
    argon2
        .hash_password_into(password.as_bytes(), salt, &mut key)
        .map_err(|e| NexusError::Crypto(e.to_string()))?;
    Ok(key)
}

/// Encrypt `plaintext` with the given password.
/// Returns the full encrypted blob (salt + nonce + ciphertext).
pub fn encrypt(plaintext: &[u8], password: &str) -> NexusResult<Vec<u8>> {
    // Generate random salt and nonce
    let mut salt = [0u8; SALT_LEN];
    let mut nonce_bytes = [0u8; NONCE_LEN];
    rand::thread_rng().fill_bytes(&mut salt);
    rand::thread_rng().fill_bytes(&mut nonce_bytes);

    let key = derive_key(password, &salt)?;
    let cipher = XChaCha20Poly1305::new_from_slice(&key)
        .map_err(|e| NexusError::Crypto(e.to_string()))?;
    let nonce = XNonce::from_slice(&nonce_bytes);

    let ciphertext = cipher
        .encrypt(nonce, plaintext)
        .map_err(|e| NexusError::Crypto(e.to_string()))?;

    // Pack: salt || nonce || ciphertext
    let mut output = Vec::with_capacity(SALT_LEN + NONCE_LEN + ciphertext.len());
    output.extend_from_slice(&salt);
    output.extend_from_slice(&nonce_bytes);
    output.extend_from_slice(&ciphertext);
    Ok(output)
}

/// Decrypt a blob produced by `encrypt()` using the given password.
pub fn decrypt(blob: &[u8], password: &str) -> NexusResult<Vec<u8>> {
    if blob.len() < SALT_LEN + NONCE_LEN {
        return Err(NexusError::Crypto("blob too short".into()));
    }
    let salt = &blob[..SALT_LEN];
    let nonce_bytes = &blob[SALT_LEN..SALT_LEN + NONCE_LEN];
    let ciphertext = &blob[SALT_LEN + NONCE_LEN..];

    let key = derive_key(password, salt)?;
    let cipher = XChaCha20Poly1305::new_from_slice(&key)
        .map_err(|e| NexusError::Crypto(e.to_string()))?;
    let nonce = XNonce::from_slice(nonce_bytes);

    cipher
        .decrypt(nonce, ciphertext)
        .map_err(|_| NexusError::Crypto("decryption failed — wrong password or corrupted data".into()))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_encrypt_decrypt_roundtrip() {
        let data = b"NexusStorage secret payload";
        let password = "hunter2";
        let encrypted = encrypt(data, password).unwrap();
        let decrypted = decrypt(&encrypted, password).unwrap();
        assert_eq!(data.as_slice(), decrypted.as_slice());
    }

    #[test]
    fn test_wrong_password_fails() {
        let data = b"top secret";
        let encrypted = encrypt(data, "correct-password").unwrap();
        assert!(decrypt(&encrypted, "wrong-password").is_err());
    }

    #[test]
    fn test_tampered_ciphertext_fails() {
        let data = b"integrity test";
        let mut encrypted = encrypt(data, "pass").unwrap();
        let last = encrypted.last_mut().unwrap();
        *last ^= 0xFF; // flip bits at the end (inside the auth tag)
        assert!(decrypt(&encrypted, "pass").is_err());
    }
}

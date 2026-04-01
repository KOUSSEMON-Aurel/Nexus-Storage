// nexus-core/src/kdf.rs
// Key Derivation Function (KDF) using Argon2id for master key derivation from user password.
// Designed for V4 architecture: client-side derivation, never send raw password over network.

use argon2::{Argon2, Params, Version};
use rand::RngCore;
use crate::types::{NexusError, NexusResult};

const SALT_LEN: usize = 16;        // Argon2 salt (16 bytes)
const KEY_LEN: usize = 32;         // Master key (256-bit)
const ARGON2_TIME_COST: u32 = 3;   // iterations
const ARGON2_MEM_COST: u32 = 65536; // 64 MiB (GPU-resistant)
const ARGON2_PARALLELISM: u32 = 1; // Single-threaded (client app)

/// Generate a cryptographically random 16-byte salt for Argon2.
pub fn generate_recovery_salt() -> [u8; SALT_LEN] {
    let mut salt = [0u8; SALT_LEN];
    rand::thread_rng().fill_bytes(&mut salt);
    salt
}

/// Derive a 256-bit master key from a user password and salt using Argon2id.
/// 
/// This function is designed to be called CLIENT-SIDE ONLY (in GUI or CLI).
/// The resulting master key is kept in memory and never persisted.
/// 
/// # Parameters
/// - `password`: User's chosen password (should be strong, >= 12 chars recommended)
/// - `salt`: 16-byte salt from recovery_state (stored public in encrypted manifest)
/// 
/// # Returns
/// 32-byte master key suitable for XChaCha20-Poly1305 encryption
/// 
/// # Security Notes
/// - Argon2id is GPU/ASIC resistant
/// - 64 MiB memory cost + 3 iterations = ~100ms on modern CPU
/// - Salt prevents rainbow tables
/// - Result never hashed again; use directly as encryption key
pub fn derive_master_key(password: &str, salt: &[u8]) -> NexusResult<[u8; KEY_LEN]> {
    if salt.len() != SALT_LEN {
        return Err(NexusError::Crypto(format!(
            "Invalid salt length: expected {}, got {}",
            SALT_LEN,
            salt.len()
        )));
    }

    let params = Params::new(
        ARGON2_MEM_COST,
        ARGON2_TIME_COST,
        ARGON2_PARALLELISM,
        Some(KEY_LEN),
    )
    .map_err(|e| NexusError::Crypto(format!("Argon2 params error: {}", e)))?;

    let argon2 = Argon2::new(argon2::Algorithm::Argon2id, Version::V0x13, params);
    let mut key = [0u8; KEY_LEN];

    argon2
        .hash_password_into(password.as_bytes(), salt, &mut key)
        .map_err(|e| NexusError::Crypto(format!("Argon2 hash failed: {}", e)))?;

    Ok(key)
}

/// Verify that a password produces the expect key (for testing).
/// In production, verification is implicit: wrong password → decrypt fails.
#[cfg(test)]
pub fn verify_password(password: &str, salt: &[u8], expected_key: &[u8; KEY_LEN]) -> bool {
    match derive_master_key(password, salt) {
        Ok(key) => key == *expected_key,
        Err(_) => false,
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_deterministic_derivation() {
        let password = "my-super-secret-password";
        let salt = [42u8; SALT_LEN];

        let key1 = derive_master_key(password, &salt).unwrap();
        let key2 = derive_master_key(password, &salt).unwrap();

        assert_eq!(key1, key2);
    }

    #[test]
    fn test_different_passwords_different_keys() {
        let salt = [42u8; SALT_LEN];

        let key1 = derive_master_key("password1", &salt).unwrap();
        let key2 = derive_master_key("password2", &salt).unwrap();

        assert_ne!(key1, key2);
    }

    #[test]
    fn test_different_salts_different_keys() {
        let password = "same-password";
        let salt1 = [1u8; SALT_LEN];
        let salt2 = [2u8; SALT_LEN];

        let key1 = derive_master_key(password, &salt1).unwrap();
        let key2 = derive_master_key(password, &salt2).unwrap();

        assert_ne!(key1, key2);
    }

    #[test]
    fn test_invalid_salt_length() {
        let password = "password";
        let invalid_salt = [42u8; 8]; // Wrong length

        let result = derive_master_key(password, &invalid_salt);
        assert!(result.is_err());
    }

    #[test]
    fn test_generated_salt_is_random() {
        let salt1 = generate_recovery_salt();
        let salt2 = generate_recovery_salt();

        assert_ne!(salt1, salt2);
    }

    #[test]
    fn test_random_salt_length() {
        let salt = generate_recovery_salt();
        assert_eq!(salt.len(), SALT_LEN);
    }
}

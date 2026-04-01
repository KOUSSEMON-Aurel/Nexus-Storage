// nexus-gui/src-tauri/src/commands/crypto.rs
// Tauri IPC commands for client-side cryptography
// CRITICAL: All key derivation happens here, NO password sent to daemon

use serde::{Deserialize, Serialize};
use std::os::raw::c_uchar;

#[link(name = "nexus_core", kind = "static")]
extern "C" {
    fn nexus_generate_recovery_salt(
        out_ptr: *mut *mut c_uchar,
        out_len: *mut usize,
    ) -> i32;

    fn nexus_derive_master_key(
        password_ptr: *const libc::c_char,
        password_len: usize,
        salt_ptr: *const c_uchar,
        salt_len: usize,
        out_ptr: *mut *mut c_uchar,
        out_len: *mut usize,
    ) -> i32;

    fn nexus_free_bytes(ptr: *mut c_uchar, len: usize);
}

const NEXUS_OK: i32 = 0;

#[derive(Debug, Deserialize)]
pub struct DeriveKeyRequest {
    pub password: String,
    pub salt: String, // hex-encoded
}

#[derive(Debug, Serialize)]
pub struct DeriveKeyResponse {
    pub master_key: String, // hex-encoded 32 bytes
}

#[derive(Debug, Serialize)]
pub struct GenerateSaltResponse {
    pub salt: String, // hex-encoded 16 bytes
}

/// Generate a cryptographically random 16-byte recovery salt.
#[tauri::command]
pub fn generate_recovery_salt() -> Result<GenerateSaltResponse, String> {
    unsafe {
        let mut out_ptr: *mut c_uchar = std::ptr::null_mut();
        let mut out_len: usize = 0;

        let res = nexus_generate_recovery_salt(&mut out_ptr, &mut out_len);
        if res != NEXUS_OK {
            return Err(format!("Salt generation failed with code {}", res));
        }

        if out_len != 16 {
            nexus_free_bytes(out_ptr, out_len);
            return Err(format!("Invalid salt length: {}", out_len));
        }

        let salt_bytes = std::slice::from_raw_parts(out_ptr, out_len);
        let salt_hex = hex::encode(salt_bytes);
        nexus_free_bytes(out_ptr, out_len);

        Ok(GenerateSaltResponse { salt: salt_hex })
    }
}

/// Derive a 32-byte master key from password + salt using Argon2id.
/// 
/// CRITICAL SECURITY NOTES:
/// - Password is taken from the GUI text input (never sent over network)
/// - Derivation happens in THIS process (Tauri window)
/// - Returns only the derived masterKey (hex string)
/// - The password is NOT persisted or logged
/// - Caller must send only the masterKey to daemon via IPC
#[tauri::command]
pub fn derive_master_key(req: DeriveKeyRequest) -> Result<DeriveKeyResponse, String> {
    // Decode salt from hex
    let salt_bytes = hex::decode(&req.salt)
        .map_err(|e| format!("Invalid salt encoding: {}", e))?;

    if salt_bytes.len() != 16 {
        return Err(format!("Invalid salt length: expected 16, got {}", salt_bytes.len()));
    }

    unsafe {
        // Prepare password as C string
        let c_password = std::ffi::CString::new(req.password.as_bytes())
            .map_err(|e| format!("Password contains null byte: {}", e))?;

        let mut out_ptr: *mut c_uchar = std::ptr::null_mut();
        let mut out_len: usize = 0;

        let res = nexus_derive_master_key(
            c_password.as_ptr(),
            req.password.len(),
            salt_bytes.as_ptr() as *const c_uchar,
            salt_bytes.len(),
            &mut out_ptr,
            &mut out_len,
        );

        if res != NEXUS_OK {
            return Err(format!("Key derivation failed with code {}", res));
        }

        if out_len != 32 {
            nexus_free_bytes(out_ptr, out_len);
            return Err(format!("Invalid key length: {}", out_len));
        }

        let key_bytes = std::slice::from_raw_parts(out_ptr, out_len);
        let master_key = hex::encode(key_bytes);
        nexus_free_bytes(out_ptr, out_len);

        Ok(DeriveKeyResponse { master_key })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_generate_salt() {
        let resp = generate_recovery_salt().expect("Failed to generate salt");
        let decoded = hex::decode(&resp.salt).expect("Failed to decode salt");
        assert_eq!(decoded.len(), 16);
    }

    #[test]
    fn test_derive_key_deterministic() {
        let password = "test-password-123".to_string();
        let salt = hex::encode([42u8; 16]);

        let req1 = DeriveKeyRequest {
            password: password.clone(),
            salt: salt.clone(),
        };

        let req2 = DeriveKeyRequest {
            password: password.clone(),
            salt: salt.clone(),
        };

        let resp1 = derive_master_key(req1).expect("Derivation 1 failed");
        let resp2 = derive_master_key(req2).expect("Derivation 2 failed");

        assert_eq!(resp1.master_key, resp2.master_key);
    }

    #[test]
    fn test_different_passwords_different_keys() {
        let salt = hex::encode([42u8; 16]);

        let req1 = DeriveKeyRequest {
            password: "password1".to_string(),
            salt: salt.clone(),
        };

        let req2 = DeriveKeyRequest {
            password: "password2".to_string(),
            salt,
        };

        let resp1 = derive_master_key(req1).expect("Derivation 1 failed");
        let resp2 = derive_master_key(req2).expect("Derivation 2 failed");

        assert_ne!(resp1.master_key, resp2.master_key);
    }
}

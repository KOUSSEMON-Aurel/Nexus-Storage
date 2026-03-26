// nexus-core/src/hasher.rs
// Content-addressable fingerprinting using xxHash3 (fast) and SHA-256 (strong).
// Used for deduplication and integrity checking.

use xxhash_rust::xxh3::xxh3_128;
use sha2::{Sha256, Digest};

/// Fast 128-bit fingerprint of a byte slice using xxHash3.
/// Used for *deduplication* — before uploading, check if this hash is already
/// in the local catalogue (nexus.db). If yes → skip upload, create a symlink.
pub fn fast_fingerprint(data: &[u8]) -> u128 {
    xxh3_128(data)
}

/// Strong SHA-256 fingerprint for *integrity verification* after download.
/// Stored inside the video description metadata. Computed on decompressed,
/// decrypted payload.
pub fn strong_fingerprint(data: &[u8]) -> [u8; 32] {
    let mut hasher = Sha256::new();
    hasher.update(data);
    hasher.finalize().into()
}

/// Hex-encode a SHA-256 fingerprint for embedding in YouTube description.
pub fn fingerprint_to_hex(fp: &[u8; 32]) -> String {
    hex::encode(fp)
}

/// Verify integrity: recompute SHA-256 of `data` and compare against `expected_hex`.
/// Returns `true` if the data is intact.
pub fn verify_integrity(data: &[u8], expected_hex: &str) -> bool {
    let computed = strong_fingerprint(data);
    fingerprint_to_hex(&computed) == expected_hex
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_fast_fingerprint_deterministic() {
        let data = b"NexusStorage is awesome";
        assert_eq!(fast_fingerprint(data), fast_fingerprint(data));
    }

    #[test]
    fn test_strong_fingerprint_and_verify() {
        let data = b"NexusStorage integrity test";
        let fp = strong_fingerprint(data);
        let hex = fingerprint_to_hex(&fp);
        assert!(verify_integrity(data, &hex));
        assert!(!verify_integrity(b"tampered data", &hex));
    }
}

#ifndef NEXUS_CORE_H
#define NEXUS_CORE_H

#include <stddef.h>
#include <stdint.h>

#ifdef __cplusplus
extern "C" {
#endif

// Error codes
#define NEXUS_OK 0
#define NEXUS_ERR_NULL_PTR -1
#define NEXUS_ERR_CRYPTO -2
#define NEXUS_ERR_COMPRESS -3
#define NEXUS_ERR_ENCODE -4
#define NEXUS_ERR_DECODE -5
#define NEXUS_ERR_IO -6

/**
 * Free a byte buffer previously allocated by the nexus-core library.
 */
void nexus_free_bytes(uint8_t* ptr, size_t len);

/**
 * Encrypt bytes with a password.
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_encrypt(
    const uint8_t* in_ptr,
    size_t in_len,
    const char* password,
    uint8_t** out_ptr,
    size_t* out_len
);

/**
 * Decrypt bytes with a password.
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_decrypt(
    const uint8_t* in_ptr,
    size_t in_len,
    const char* password,
    uint8_t** out_ptr,
    size_t* out_len
);

/**
 * Compress bytes.
 * level: 0=auto, 1=lz4, 2=zstd, 3=lzma, 4=store
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_compress(
    const uint8_t* in_ptr,
    size_t in_len,
    int32_t level,
    uint8_t** out_ptr,
    size_t* out_len
);

/**
 * Decompress bytes.
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_decompress(
    const uint8_t* in_ptr,
    size_t in_len,
    uint8_t** out_ptr,
    size_t* out_len
);

/**
 * Compute SHA-256 hex fingerprint.
 * out_hex must be at least 65 bytes long.
 */
int32_t nexus_sha256_hex(
    const uint8_t* in_ptr,
    size_t in_len,
    char* out_hex
);

/**
 * Encode bytes into PNG frames in output_dir.
 * mode: 0=Tank, 1=Density
 * Returns the number of frames written (positive) or a negative error code.
 */
int32_t nexus_encode_to_frames(
    const uint8_t* in_ptr,
    size_t in_len,
    const char* output_dir,
    int32_t mode
);

/**
 * Decode a payload from PNG frames in frame_dir.
 * mode: 0=Tank, 1=Density
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 * Returns 0 on success or a negative error code.
 */
int32_t nexus_decode_from_frames(
    const char* frame_dir,
    int32_t mode,
    uint8_t** out_ptr,
    size_t* out_len
);

/**
 * Per-file raw-key encryption functions (no password derivation).
 * These enable per-file shareable encryption keys.
 */

/**
 * Generate a cryptographically random 32-byte file key.
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_generate_file_key(uint8_t** out_ptr, size_t* out_len);

/**
 * Encrypt bytes using a raw 32-byte file key (bypasses PBKDF2).
 * key_ptr must point to exactly 32 bytes.
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_encrypt_with_key(
    const uint8_t* in_ptr,
    size_t in_len,
    const uint8_t* key_ptr,
    uint8_t** out_ptr,
    size_t* out_len
);

/**
 * Decrypt a blob produced by nexus_encrypt_with_key.
 * key_ptr must point to exactly 32 bytes.
 * out_ptr will point to a buffer that must be freed with nexus_free_bytes.
 */
int32_t nexus_decrypt_with_key(
    const uint8_t* in_ptr,
    size_t in_len,
    const uint8_t* key_ptr,
    uint8_t** out_ptr,
    size_t* out_len
);

#ifdef __cplusplus
}
#endif

#endif // NEXUS_CORE_H

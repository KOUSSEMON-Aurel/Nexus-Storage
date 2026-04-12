// nexus-core/src/ffi.rs
// C-compatible FFI layer for Go (via CGO) and other native callers.
// All functions prefixed with `nexus_` and use only C-compatible types.
//
// Error codes:
//   0  = NEXUS_OK
//  -1  = NEXUS_ERR_NULL_PTR
//  -2  = NEXUS_ERR_CRYPTO
//  -3  = NEXUS_ERR_COMPRESS
//  -4  = NEXUS_ERR_ENCODE
//  -5  = NEXUS_ERR_DECODE
//  -6  = NEXUS_ERR_IO

use std::ffi::{CStr, CString};
use std::os::raw::{c_char, c_int, c_uchar};
use std::slice;

use crate::{crypto, compress, hasher, encoder};
use crate::types::{CompressionLevel, EncodingMode};
use crate::streaming::{StreamingContext, StreamingEncoder, StreamingDecoder};

const NEXUS_OK: c_int = 0;
const NEXUS_ERR_NULL_PTR: c_int = -1;
const NEXUS_ERR_CRYPTO: c_int = -2;
const NEXUS_ERR_COMPRESS: c_int = -3;
const NEXUS_ERR_ENCODE: c_int = -4;
const NEXUS_ERR_DECODE: c_int = -5;

// --------------------------
//  Helpers
// --------------------------

/// Caller must free the returned pointer with `nexus_free_bytes`.
unsafe fn alloc_bytes(data: Vec<u8>, out_ptr: *mut *mut c_uchar, out_len: *mut usize) -> c_int {
    let len = data.len();
    let boxed = data.into_boxed_slice();
    let raw = Box::into_raw(boxed) as *mut c_uchar;
    unsafe {
        *out_ptr = raw;
        *out_len = len;
    }
    NEXUS_OK
}

// --------------------------
//  Memory management
// --------------------------

/// Free a byte buffer previously allocated by the nexus-core library.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_free_bytes(ptr: *mut c_uchar, len: usize) {
    if ptr.is_null() {
        return;
    }
    unsafe {
        let _ = Box::from_raw(std::slice::from_raw_parts_mut(ptr, len));
    }
}

// --------------------------
//  Encrypt / Decrypt
// --------------------------

/// Encrypt `in_len` bytes at `in_ptr` with `password`.
/// On success: writes the output pointer to `*out_ptr`, its length to `*out_len`, returns 0.
/// On failure: returns a negative error code.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encrypt(
    in_ptr: *const c_uchar,
    in_len: usize,
    password: *const c_char,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if in_ptr.is_null() || password.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let plaintext = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    let pwd = match unsafe { CStr::from_ptr(password) }.to_str() {
        Ok(s) => s,
        Err(_) => return NEXUS_ERR_NULL_PTR,
    };
    match crypto::encrypt(plaintext, pwd) {
        Ok(blob) => unsafe { alloc_bytes(blob, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

/// Decrypt `in_len` bytes at `in_ptr` with `password`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decrypt(
    in_ptr: *const c_uchar,
    in_len: usize,
    password: *const c_char,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if in_ptr.is_null() || password.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let blob = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    let pwd = match unsafe { CStr::from_ptr(password) }.to_str() {
        Ok(s) => s,
        Err(_) => return NEXUS_ERR_NULL_PTR,
    };
    match crypto::decrypt(blob, pwd) {
        Ok(plain) => unsafe { alloc_bytes(plain, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

// --------------------------
//  Compress / Decompress
// --------------------------

/// Compress `in_len` bytes. Pass `level = 0` for auto-detection.
///   0 = auto, 1 = lz4, 2 = zstd, 3 = lzma, 4 = store
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_compress(
    in_ptr: *const c_uchar,
    in_len: usize,
    level: c_int,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if in_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    let algo = match level {
        2 => Some(CompressionLevel::Zstd),
        4 => Some(CompressionLevel::Store),
        _ => None, // auto
    };
    match compress::compress(data, algo) {
        Ok(c) => unsafe { alloc_bytes(c, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_COMPRESS,
    }
}

/// Decompress bytes previously compressed by `nexus_compress`.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decompress(
    in_ptr: *const c_uchar,
    in_len: usize,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if in_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    match compress::decompress(data) {
        Ok(d) => unsafe { alloc_bytes(d, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_COMPRESS,
    }
}

// --------------------------
//  Hashing
// --------------------------

/// Compute the SHA-256 fingerprint of `in_len` bytes and write it as a
/// null-terminated hex string to `out_hex` (must be at least 65 bytes).
/// Returns 0 on success.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_sha256_hex(
    in_ptr: *const c_uchar,
    in_len: usize,
    out_hex: *mut c_char,
) -> c_int {
    if in_ptr.is_null() || out_hex.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    let fp = hasher::strong_fingerprint(data);
    let hex = hasher::fingerprint_to_hex(&fp);
    let c_str = CString::new(hex).unwrap();
    unsafe {
        std::ptr::copy_nonoverlapping(c_str.as_ptr(), out_hex, 65);
    }
    NEXUS_OK
}

// --------------------------
//  Encoding
// --------------------------

/// Encode `in_len` bytes into PNG frames in `output_dir`.
/// mode: 0=Tank, 1=Density
/// Returns the number of frames written (positive) or a negative error code.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encode_to_frames(
    in_ptr: *const c_uchar,
    in_len: usize,
    output_dir: *const c_char,
    mode: c_int,
) -> c_int {
    if in_ptr.is_null() || output_dir.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { slice::from_raw_parts(in_ptr, in_len) }.to_vec();
    let path_str = match unsafe { CStr::from_ptr(output_dir) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return NEXUS_ERR_NULL_PTR,
    };
    let encoding_mode = match mode {
        1 => EncodingMode::High,
        _ => EncodingMode::Base,
    };

    // catch_unwind prevents a Rust panic from crossing the FFI boundary and
    // crashing the Go daemon with a SIGSEGV.
    let result = std::panic::catch_unwind(|| {
        let path = std::path::Path::new(&path_str);
        encoder::encode_to_frames(&data, path, encoding_mode)
    });

    match result {
        Ok(Ok(n)) => n as c_int,
        Ok(Err(_)) => -4, // NEXUS_ERR_ENCODE
        Err(_) => {
            eprintln!("[nexus-core] PANIC caught in nexus_encode_to_frames — returning error code");
            -4 // NEXUS_ERR_ENCODE
        }
    }
}

/// Decode a payload from a folder of PNG frames.
/// On success: writes the output pointer to `*out_ptr`, its length to `*out_len`, returns 0.
/// On failure: returns a negative error code.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decode_from_frames(
    frame_dir: *const c_char,
    mode: c_int,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if frame_dir.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let path_str = match unsafe { CStr::from_ptr(frame_dir) }.to_str() {
        Ok(s) => s.to_string(),
        Err(_) => return NEXUS_ERR_NULL_PTR,
    };
    let encoding_mode = match mode {
        1 => EncodingMode::High,
        _ => EncodingMode::Base,
    };

    // catch_unwind prevents a Rust panic (e.g. image resolution mismatch, OOB pixel
    // access) from crossing the FFI boundary and killing the Go daemon with SIGSEGV.
    let result = std::panic::catch_unwind(|| {
        let path = std::path::Path::new(&path_str);
        crate::decoder::decode_from_frames(path, encoding_mode)
    });

    match result {
        Ok(Ok(data)) => unsafe { alloc_bytes(data, out_ptr, out_len) },
        Ok(Err(_)) => -5, // NEXUS_ERR_DECODE
        Err(_) => {
            eprintln!("[nexus-core] PANIC caught in nexus_decode_from_frames — returning error code");
            -5 // NEXUS_ERR_DECODE
        }
    }
}

// --------------------------
//  Per-File Raw-Key Functions
// --------------------------

/// Generate a cryptographically random 32-byte file key.
/// Caller must free the returned bytes with nexus_free_bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_generate_file_key(
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let key = crate::crypto::generate_file_key();
    unsafe { alloc_bytes(key.to_vec(), out_ptr, out_len) }
}

/// Encrypt `in_len` bytes using a raw 32-byte file key (no password derivation).
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encrypt_with_key(
    in_ptr: *const c_uchar,
    in_len: usize,
    key_ptr: *const c_uchar,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if in_ptr.is_null() || key_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { std::slice::from_raw_parts(in_ptr, in_len) };
    let key_slice = unsafe { std::slice::from_raw_parts(key_ptr, 32) };
    let key_array: &[u8; 32] = match key_slice.try_into() {
        Ok(k) => k,
        Err(_) => return NEXUS_ERR_CRYPTO,
    };
    match crate::crypto::encrypt_with_key(data, key_array) {
        Ok(blob) => unsafe { alloc_bytes(blob, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

/// Decrypt a blob produced by nexus_encrypt_with_key.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decrypt_with_key(
    in_ptr: *const c_uchar,
    in_len: usize,
    key_ptr: *const c_uchar,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if in_ptr.is_null() || key_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { std::slice::from_raw_parts(in_ptr, in_len) };
    let key_slice = unsafe { std::slice::from_raw_parts(key_ptr, 32) };
    let key_array: &[u8; 32] = match key_slice.try_into() {
        Ok(k) => k,
        Err(_) => return NEXUS_ERR_CRYPTO,
    };
    match crate::crypto::decrypt_with_key(data, key_array) {
        Ok(blob) => unsafe { alloc_bytes(blob, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

// --------------------------
//  Key Derivation (Argon2)
// --------------------------

/// Generate a cryptographically random 16-byte recovery salt.
/// Caller must free the result with nexus_free_bytes.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_generate_recovery_salt(
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let salt = crate::kdf::generate_recovery_salt();
    unsafe { alloc_bytes(salt.to_vec(), out_ptr, out_len) }
}

/// Derive a 32-byte master key from password + salt using Argon2id.
/// 
/// # Parameters
/// - password_ptr: null-terminated C string
/// - password_len: byte length (not including null terminator, for validation)
/// - salt_ptr: 16-byte salt
/// - salt_len: must be 16
/// - out_ptr: pointer to store result
/// - out_len: pointer to store result length (32)
/// 
/// # Returns
/// 0 = NEXUS_OK, otherwise error code
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_derive_master_key(
    password_ptr: *const c_char,
    _password_len: usize,
    salt_ptr: *const c_uchar,
    salt_len: usize,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if password_ptr.is_null() || salt_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }

    // Read password as C string
    let password = match unsafe { CStr::from_ptr(password_ptr) }.to_str() {
        Ok(s) => s,
        Err(_) => return NEXUS_ERR_CRYPTO,
    };

    // Read salt bytes
    let salt = unsafe { slice::from_raw_parts(salt_ptr, salt_len) };

    // Derive key
    match crate::kdf::derive_master_key(password, salt) {
        Ok(key) => unsafe { alloc_bytes(key.to_vec(), out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

// --------------------------
//  Streaming (Hardened)
// --------------------------

/// Initialize an encryption stream context.
/// Returns a pointer to the context or null on failure.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encrypt_stream_init(
    key_ptr: *const c_uchar,
    out_nonce_prefix: *mut c_uchar,
) -> *mut StreamingContext {
    if key_ptr.is_null() || out_nonce_prefix.is_null() {
        return std::ptr::null_mut();
    }
    let key_slice = unsafe { slice::from_raw_parts(key_ptr, 32) };
    let key: &[u8; 32] = match key_slice.try_into() {
        Ok(k) => k,
        Err(_) => return std::ptr::null_mut(),
    };

    let ctx = StreamingContext::new_encrypt(key);
    unsafe {
        std::ptr::copy_nonoverlapping(ctx.nonce_prefix().as_ptr(), out_nonce_prefix, 16);
    }
    Box::into_raw(Box::new(ctx))
}

/// Initialize a decryption stream context.
#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decrypt_stream_init(
    key_ptr: *const c_uchar,
    nonce_prefix_ptr: *const c_uchar,
) -> *mut StreamingContext {
    if key_ptr.is_null() || nonce_prefix_ptr.is_null() {
        return std::ptr::null_mut();
    }
    let key_slice = unsafe { slice::from_raw_parts(key_ptr, 32) };
    let key: &[u8; 32] = match key_slice.try_into() {
        Ok(k) => k,
        Err(_) => return std::ptr::null_mut(),
    };
    let prefix_slice = unsafe { slice::from_raw_parts(nonce_prefix_ptr, 16) };
    let prefix: [u8; 16] = match prefix_slice.try_into() {
        Ok(p) => p,
        Err(_) => return std::ptr::null_mut(),
    };

    let ctx = StreamingContext::new_decrypt(key, prefix);
    Box::into_raw(Box::new(ctx))
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encrypt_stream_update(
    ctx_ptr: *mut StreamingContext,
    in_ptr: *const c_uchar,
    in_len: usize,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if ctx_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    if in_ptr.is_null() && in_len > 0 {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    let data = if in_ptr.is_null() { &[] } else { unsafe { slice::from_raw_parts(in_ptr, in_len) } };
    
    match ctx.encrypt_update(data) {
        Ok(res) => unsafe { alloc_bytes(res, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decrypt_stream_update(
    ctx_ptr: *mut StreamingContext,
    in_ptr: *const c_uchar,
    in_len: usize,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if ctx_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    if in_ptr.is_null() && in_len > 0 {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    let data = if in_ptr.is_null() { &[] } else { unsafe { slice::from_raw_parts(in_ptr, in_len) } };
    
    match ctx.decrypt_update(data) {
        Ok(res) => unsafe { alloc_bytes(res, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encrypt_stream_finalize(
    ctx_ptr: *mut StreamingContext,
    in_ptr: *const c_uchar,
    in_len: usize,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if ctx_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    if in_ptr.is_null() && in_len > 0 {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    let data = if in_ptr.is_null() { &[] } else { unsafe { slice::from_raw_parts(in_ptr, in_len) } };
    
    match ctx.finalize_encrypt(data) {
        Ok(res) => unsafe { alloc_bytes(res, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_decrypt_stream_finalize(
    ctx_ptr: *mut StreamingContext,
    in_ptr: *const c_uchar,
    in_len: usize,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if ctx_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    if in_ptr.is_null() && in_len > 0 {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    let data = if in_ptr.is_null() { &[] } else { unsafe { slice::from_raw_parts(in_ptr, in_len) } };
    
    match ctx.finalize_decrypt(data) {
        Ok(res) => unsafe { alloc_bytes(res, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_CRYPTO,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_crypto_stream_drop(ctx_ptr: *mut StreamingContext) {
    if !ctx_ptr.is_null() {
        unsafe {
            let _ = Box::from_raw(ctx_ptr);
        }
    }
}

// --- Encoder Streaming FFI ---

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encode_stream_init(mode: c_int) -> *mut StreamingEncoder {
    let encoding_mode = match mode {
        1 => EncodingMode::High,
        _ => EncodingMode::Base,
    };
    Box::into_raw(Box::new(StreamingEncoder::new(encoding_mode)))
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encode_stream_push(
    ctx_ptr: *mut StreamingEncoder,
    in_ptr: *const c_uchar,
    in_len: usize,
) -> c_int {
    if ctx_ptr.is_null() || in_ptr.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    let data = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    
    match ctx.push_data(data) {
        Ok(n) => n as c_int,
        Err(_) => NEXUS_ERR_ENCODE,
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encode_stream_pop_frame(
    ctx_ptr: *mut StreamingEncoder,
    out_ptr: *mut *mut c_uchar,
    out_len: *mut usize,
) -> c_int {
    if ctx_ptr.is_null() || out_ptr.is_null() || out_len.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    
    match ctx.pop_frame() {
        Some(frame) => unsafe { alloc_bytes(frame, out_ptr, out_len) },
        None => 1, // Special code for "No more frames currently"
    }
}

#[unsafe(no_mangle)]
pub unsafe extern "C" fn nexus_encode_stream_finalize(ctx_ptr: *mut StreamingEncoder) -> c_int {
    if ctx_ptr.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let ctx = unsafe { &mut *ctx_ptr };
    
    match ctx.finalize() {
        Ok(_) => NEXUS_OK,
        Err(_) => NEXUS_ERR_ENCODE,
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn nexus_encoder_stream_drop(ctx_ptr: *mut StreamingEncoder) {
    if !ctx_ptr.is_null() {
        unsafe {
            let _ = Box::from_raw(ctx_ptr);
        }
    }
}

// --- Decoder Streaming FFI ---

#[unsafe(no_mangle)]
pub extern "C" fn nexus_decode_stream_init(mode: i32) -> *mut StreamingDecoder {
    let mode = match mode {
        1 => EncodingMode::High,
        _ => EncodingMode::Base,
    };
    Box::into_raw(Box::new(StreamingDecoder::new(mode)))
}

#[unsafe(no_mangle)]
pub extern "C" fn nexus_decode_stream_push(
    ctx_ptr: *mut StreamingDecoder,
    in_ptr: *const u8,
    in_len: usize,
) -> i32 {
    let ctx = unsafe {
        if ctx_ptr.is_null() { return NEXUS_ERR_NULL_PTR; }
        &mut *ctx_ptr
    };
    if in_ptr.is_null() { return NEXUS_ERR_NULL_PTR; }
    let data = unsafe { std::slice::from_raw_parts(in_ptr, in_len) };

    match ctx.push_frame(data) {
        Ok(_) => NEXUS_OK,
        Err(_) => NEXUS_ERR_DECODE,
    }
}

#[unsafe(no_mangle)]
pub extern "C" fn nexus_decode_stream_pop(
    ctx_ptr: *mut StreamingDecoder,
    out_ptr: *mut *mut u8,
    out_len: *mut usize,
) -> i32 {
    let ctx = unsafe {
        if ctx_ptr.is_null() { return NEXUS_ERR_NULL_PTR; }
        &mut *ctx_ptr
    };
    if out_ptr.is_null() || out_len.is_null() { return NEXUS_ERR_NULL_PTR; }

    let data = ctx.pop_data();
    if data.is_empty() {
        return 1; // Not an error, just no data available
    }

    let len = data.len();
    let ptr = Box::into_raw(data.into_boxed_slice()) as *mut u8;

    unsafe {
        *out_ptr = ptr;
        *out_len = len;
    }
    NEXUS_OK
}

#[unsafe(no_mangle)]
pub extern "C" fn nexus_decoder_stream_drop(ctx_ptr: *mut StreamingDecoder) {
    if !ctx_ptr.is_null() {
        unsafe {
            let _ = Box::from_raw(ctx_ptr);
        }
    }
}

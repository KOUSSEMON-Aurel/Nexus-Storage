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
use std::path::Path;
use crate::{crypto, compress, hasher, encoder};
use crate::types::{CompressionLevel, EncodingMode};

const NEXUS_OK: c_int = 0;
const NEXUS_ERR_NULL_PTR: c_int = -1;
const NEXUS_ERR_CRYPTO: c_int = -2;
const NEXUS_ERR_COMPRESS: c_int = -3;
const NEXUS_ERR_IO: c_int = -6;

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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
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
        1 => Some(CompressionLevel::Lz4),
        2 => Some(CompressionLevel::Zstd),
        3 => Some(CompressionLevel::Lzma),
        4 => Some(CompressionLevel::Store),
        _ => None, // auto
    };
    match compress::compress(data, algo) {
        Ok(c) => unsafe { alloc_bytes(c, out_ptr, out_len) },
        Err(_) => NEXUS_ERR_COMPRESS,
    }
}

/// Decompress bytes previously compressed by `nexus_compress`.
#[no_mangle]
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
#[no_mangle]
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
#[no_mangle]
pub unsafe extern "C" fn nexus_encode_to_frames(
    in_ptr: *const c_uchar,
    in_len: usize,
    output_dir: *const c_char,
    mode: c_int,
) -> c_int {
    if in_ptr.is_null() || output_dir.is_null() {
        return NEXUS_ERR_NULL_PTR;
    }
    let data = unsafe { slice::from_raw_parts(in_ptr, in_len) };
    let path_str = match unsafe { CStr::from_ptr(output_dir) }.to_str() {
        Ok(s) => s,
        Err(_) => return NEXUS_ERR_NULL_PTR,
    };
    let path = Path::new(path_str);
    let encoding_mode = match mode {
        0 => EncodingMode::Tank,
        1 => EncodingMode::Density,
        _ => EncodingMode::Tank,
    };

    match encoder::encode_to_frames(data, path, encoding_mode) {
        Ok(n) => n as c_int,
        Err(_) => -4, // NEXUS_ERR_ENCODE
    }
}

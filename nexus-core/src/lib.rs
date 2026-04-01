// nexus-core/src/lib.rs
// Root library exposing all NexusStorage core modules.

pub mod compress;
pub mod crypto;
pub mod encoder;
pub mod decoder;
pub mod hasher;
pub mod types;
pub mod kdf;  // V4: Key derivation function (Argon2)

// C-compatible FFI for Go/Tauri interop
pub mod ffi;

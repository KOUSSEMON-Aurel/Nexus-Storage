// nexus-core/src/lib.rs
// Root library exposing all NexusStorage core modules.

pub mod compress;
pub mod crypto;
pub mod encoder;
pub mod decoder;
pub mod hasher;
pub mod types;

// C-compatible FFI for Go/Tauri interop
pub mod ffi;

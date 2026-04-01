use std::path::PathBuf;

fn main() {
    // Build Tauri
    tauri_build::build();
    
    // Link nexus-core static library
    let nexus_core_lib = PathBuf::from(env!("CARGO_MANIFEST_DIR"))
        .parent()
        .unwrap()
        .parent()
        .unwrap()
        .join("target/debug");
    
    println!("cargo:rustc-link-search=native={}", nexus_core_lib.display());
    println!("cargo:rustc-link-lib=static=nexus_core");
}

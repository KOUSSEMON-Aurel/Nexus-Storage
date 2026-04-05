fn main() {
    // Récupère les variables d'environnement Cargo
    let target = std::env::var("TARGET").expect("TARGET non défini");
    let profile = std::env::var("PROFILE").expect("PROFILE non défini");
    let manifest_dir = std::env::var("CARGO_MANIFEST_DIR").expect("CARGO_MANIFEST_DIR non défini");

    // Remonte jusqu'à la racine du workspace (nexus-gui/src-tauri → racine)
    let workspace_root = std::path::Path::new(&manifest_dir)
        .parent().unwrap() // nexus-gui/src-tauri → nexus-gui
        .parent().unwrap(); // nexus-gui → racine workspace

    // Cherche d'abord dans target/<target>/<profile> (cross-compilation CI)
    let cross_path = workspace_root
        .join("target")
        .join(&target)
        .join(&profile);

    // Sinon dans target/<profile> (compilation native)
    let native_path = workspace_root
        .join("target")
        .join(&profile);

    if cross_path.join("libnexus_core.a").exists() || cross_path.join("nexus_core.lib").exists() {
        println!("cargo:rustc-link-search=native={}", cross_path.display());
    } else if native_path.join("libnexus_core.a").exists() || native_path.join("nexus_core.lib").exists() {
        println!("cargo:rustc-link-search=native={}", native_path.display());
    } else {
        // Fallback : laisser Cargo chercher via le workspace
        println!("cargo:rustc-link-search=native={}", cross_path.display());
    }

    println!("cargo:rustc-link-lib=static=nexus_core");

    // Libs système selon la plateforme
    #[cfg(target_os = "linux")]
    {
        println!("cargo:rustc-link-lib=dylib=dl");
    }

    #[cfg(target_os = "windows")]
    {
        println!("cargo:rustc-link-lib=dylib=ws2_32");
        println!("cargo:rustc-link-lib=dylib=userenv");
        println!("cargo:rustc-link-lib=dylib=bcrypt");
        println!("cargo:rustc-link-lib=dylib=ntdll");
    }

    tauri_build::build()
}

// Learn more about Tauri commands at https://tauri.app/develop/calling-rust/
#[tauri::command]
fn greet(name: &str) -> String {
    format!("Hello, {}! You've been greeted from Rust!", name)
}

mod commands;

use tauri::Manager;
use tauri_plugin_shell::ShellExt;

#[cfg_attr(mobile, tauri::mobile_entry_point)]
pub fn run() {
    #[cfg(target_os = "linux")]
    unsafe {
        // 1. Force X11 backend (avoids Wayland/EGL crashes)
        std::env::set_var("GDK_BACKEND", "x11");
        
        // 2. Disable Sandbox (Modern WebKitGTK requires this in many AppImage/Container setups)
        std::env::set_var("WEBKIT_DISABLE_SANDBOX_THIS_IS_DANGEROUS", "1");
        
        // 3. Force Software Rendering (Bypasses unstable GPU drivers)
        std::env::set_var("LIBGL_ALWAYS_SOFTWARE", "1");
        std::env::set_var("GALLIUM_DRIVER", "llvmpipe");
        std::env::set_var("MESA_LOADER_DRIVER_OVERRIDE", "llvmpipe");
        
        // 4. Disable advanced rendering paths that trigger EGL initialization
        std::env::set_var("WEBKIT_USE_GLX", "1");
        std::env::set_var("WEBKIT_HARDWARE_ACCELERATION_POLICY", "never");
        std::env::set_var("WEBKIT_DISABLE_COMPOSITING_MODE", "1");
        std::env::set_var("WEBKIT_DISABLE_DMABUF_RENDERER", "1");
    }

    tauri::Builder::default()
        .plugin(tauri_plugin_shell::init())
        .plugin(tauri_plugin_opener::init())
        .plugin(tauri_plugin_dialog::init())
        .setup(|app| {
            let shell = app.shell();
            match shell.sidecar("nexus-daemon") {
                Ok(sidecar) => {
                    // Get the directory where the sidecar and its assets (.so, .json) are located
                    let bin_dir = app.path().resource_dir()
                        .map(|p: std::path::PathBuf| p.join("bin"))
                        // Fallback to searching relative to the current executable in dev
                        .unwrap_or_else(|_| {
                            let mut p = std::env::current_exe().unwrap_or_default();
                            p.pop(); // Remove exe name
                            p.join("bin") // Typical for dev bundle
                        });

                    // Set LD_LIBRARY_PATH and current_dir to let the daemon find its dependencies
                    // IMPORTANT: We scrub AppImage-polluted variables so that xdg-open works
                    #[allow(unused_mut)]
                    let mut sidecar_cmd = sidecar;
                    
                    #[cfg(target_os = "linux")]
                    {
                        // Remove AppImage poisons by clearing and re-adding only clean variables
                        // (Tauri's Command doesn't have env_remove yet)
                        let poisons = ["APPDIR", "APPIMAGE", "LD_PRELOAD", "XDG_DATA_DIRS", "GDK_PIXBUF_MODULE_FILE"];
                        let clean_vars: Vec<(String, String)> = std::env::vars()
                            .filter(|(k, _)| !poisons.contains(&k.as_str()))
                            .collect();
                        
                        sidecar_cmd = sidecar_cmd.env_clear().envs(clean_vars);
                    }

                    match sidecar_cmd
                        .current_dir(bin_dir)
                        .env("LD_LIBRARY_PATH", ".")
                        .spawn() {
                        Ok((mut rx, _child)) => {
                            println!("✅ Sidecar 'nexus-daemon' spawned successfully (env scrubbed)");
                            tauri::async_runtime::spawn(async move {
                                while let Some(event) = rx.recv().await {
                                    match event {
                                        tauri_plugin_shell::process::CommandEvent::Stdout(line) => {
                                            println!("sidecar: {}", String::from_utf8_lossy(&line));
                                        }
                                        tauri_plugin_shell::process::CommandEvent::Stderr(line) => {
                                            eprintln!("sidecar err: {}", String::from_utf8_lossy(&line));
                                        }
                                        _ => {}
                                    }
                                }
                            });
                        }
                        Err(e) => eprintln!("❌ Failed to spawn sidecar: {}", e),
                    }
                }
                Err(e) => eprintln!("❌ Sidecar 'nexus-daemon' not found: {}", e),
            }
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            greet,
            commands::crypto::generate_recovery_salt,
            commands::crypto::derive_master_key,
            commands::session::tauri_session_start,
            commands::session::tauri_session_end,
            commands::session::tauri_recovery_restore,
            commands::session::tauri_recovery_backup,
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

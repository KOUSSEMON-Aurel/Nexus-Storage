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
        std::env::set_var("GDK_BACKEND", "x11");
        std::env::set_var("WEBKIT_DISABLE_DMABUF_RENDERER", "1");
        std::env::set_var("WEBKIT_DISABLE_COMPOSITING_MODE", "1");
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
                    match sidecar
                        .current_dir(bin_dir)
                        .env("LD_LIBRARY_PATH", ".")
                        .spawn() {
                        Ok((mut rx, _child)) => {
                            println!("✅ Sidecar 'nexus-daemon' spawned successfully");
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

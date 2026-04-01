// nexus-gui/src-tauri/src/commands/session.rs

use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};

#[derive(Serialize, Deserialize, Debug)]
struct SessionStartRequest {
    master_key_hex: String,
}

#[derive(Deserialize, Debug, Serialize)]
pub struct SessionStartResponse {
    pub status: String,
    pub message: String,
}

#[derive(Serialize, Deserialize, Debug)]
struct SessionEndRequest {}

#[derive(Deserialize, Debug, Serialize)]
pub struct SessionEndResponse {
    pub status: String,
    pub message: String,
}

#[derive(Serialize, Deserialize, Debug)]
struct RecoveryRestoreRequest {
    master_key_hex: String,
}

#[derive(Deserialize, Debug, Serialize)]
pub struct RecoveryRestoreResponse {
    pub status: String,
    pub file_count: i32,
    pub message: String,
}

#[derive(Debug, Serialize, Deserialize)]
pub struct RecoveryBackupResponse {
    pub status: String,
    pub message: String,
}

const DAEMON_BASE_URL: &str = "http://127.0.0.1:8080";

/// Start a session with the daemon (authenticate with master key)
#[tauri::command]
pub fn tauri_session_start(master_key_hex: String) -> Result<SessionStartResponse, String> {
    let client = Client::new();
    let req = SessionStartRequest { master_key_hex };

    let response = client
        .post(format!("{}/api/auth/session-start", DAEMON_BASE_URL))
        .json(&req)
        .send()
        .map_err(|e| format!("Network error: {}", e))?;

    if !response.status().is_success() {
        let text = response.text().unwrap_or_default();
        return Err(format!("Server error: {}", text));
    }

    response
        .json::<SessionStartResponse>()
        .map_err(|e| format!("Failed to parse response: {}", e))
}

/// End the current session (clear master key from daemon)
#[tauri::command]
pub fn tauri_session_end() -> Result<SessionEndResponse, String> {
    let client = Client::new();
    let req = SessionEndRequest {};

    let response = client
        .post(format!("{}/api/auth/session-end", DAEMON_BASE_URL))
        .json(&req)
        .send()
        .map_err(|e| format!("Network error: {}", e))?;

    if !response.status().is_success() {
        let text = response.text().unwrap_or_default();
        return Err(format!("Server error: {}", text));
    }

    response
        .json::<SessionEndResponse>()
        .map_err(|e| format!("Failed to parse response: {}", e))
}

/// Restore from backup (download manifest from Drive, decrypt, and restore to DB)
#[tauri::command]
pub fn tauri_recovery_restore(master_key_hex: String) -> Result<RecoveryRestoreResponse, String> {
    let client = Client::new();
    let req = RecoveryRestoreRequest { master_key_hex };

    let response = client
        .post(format!("{}/api/recovery/restore", DAEMON_BASE_URL))
        .json(&req)
        .send()
        .map_err(|e| format!("Network error: {}", e))?;

    if !response.status().is_success() {
        let text = response.text().unwrap_or_default();
        return Err(format!("Server error: {}", text));
    }

    response
        .json::<RecoveryRestoreResponse>()
        .map_err(|e| format!("Failed to parse response: {}", e))
}

/// Backup current database to encrypted manifest on Drive
#[tauri::command]
pub fn tauri_recovery_backup(master_key_hex: String) -> Result<RecoveryBackupResponse, String> {
    let client = Client::new();
    let req = RecoveryRestoreRequest { master_key_hex };

    let response = client
        .post(format!("{}/api/recovery/backup", DAEMON_BASE_URL))
        .json(&req)
        .send()
        .map_err(|e| format!("Network error: {}", e))?;

    if !response.status().is_success() {
        let text = response.text().unwrap_or_default();
        return Err(format!("Server error: {}", text));
    }

    response
        .json::<RecoveryBackupResponse>()
        .map_err(|e| format!("Failed to parse response: {}", e))
}

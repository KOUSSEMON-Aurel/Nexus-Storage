use serde::{Deserialize, Serialize};

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SessionStartRequest {
    pub master_key_hex: String,
}

#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct SessionStartResponse {
    pub status: String,
    pub message: String,
}

#[derive(Debug, Serialize, Deserialize, Clone, Default)]
pub struct SessionEndResponse {
    pub status: String,
    pub message: String,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize, Serialize, Default)]
pub struct AuthStatus {
    pub authenticated: bool,
    #[serde(rename = "user", default)]
    pub channel_title: String,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct FileEntry {
    #[serde(rename = "ID")]
    pub id: i64,
    #[serde(rename = "Path")]
    pub path: String,
    #[serde(rename = "VideoID")]
    pub video_id: String,
    #[serde(rename = "Size")]
    pub size: i64,
    #[serde(rename = "Hash")]
    pub hash: String,
    #[serde(rename = "LastUpdate")]
    pub last_update: String,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize, Serialize, Default)]
pub struct QuotaInfo {
    pub used: u32,
    #[serde(rename = "limit")]
    pub total: u32,
    #[serde(default)]
    pub reset_in_secs: u64,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct TaskEntry {
    #[serde(rename = "id")]
    pub id: String,
    #[serde(rename = "filePath")]
    pub file_path: String,
    #[serde(rename = "status")]
    pub status: String,
    #[serde(rename = "progress")]
    pub progress: f64,
}

#[allow(dead_code)]
#[derive(Debug, Clone, Deserialize, Serialize)]
pub struct DaemonStats {
    pub running: bool,
    pub active_tasks: i32,
    pub uptime_seconds: u64,
}

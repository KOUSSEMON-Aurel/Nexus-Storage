// nexus-tui/src/daemon/auth.rs
// TUI Authentication endpoints for V4 session management

use serde::{Deserialize, Serialize};
use reqwest::Client;

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SessionStartRequest {
    pub master_key_hex: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SessionStartResponse {
    pub status: String,
    pub message: String,
}

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct SessionEndResponse {
    pub status: String,
    pub message: String,
}

pub async fn session_start(client: &Client, base_url: &str, master_key: &str) -> anyhow::Result<SessionStartResponse> {
    let req = SessionStartRequest {
        master_key_hex: master_key.to_string(),
    };

    let response = client
        .post(format!("{}/api/auth/session-start", base_url))
        .json(&req)
        .send()
        .await?;

    Ok(response.json().await?)
}

pub async fn session_end(client: &Client, base_url: &str) -> anyhow::Result<SessionEndResponse> {
    let response = client
        .post(format!("{}/api/auth/session-end", base_url))
        .send()
        .await?;

    Ok(response.json().await?)
}

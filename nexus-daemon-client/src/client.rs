use reqwest::{Client, StatusCode};
use std::collections::HashMap;

use crate::error::NexusError;
use crate::types::*;

pub struct NexusClient {
    client: Client,
    base_url: String,
}

impl NexusClient {
    pub fn new(base_url: &str) -> Self {
        Self {
            client: Client::builder()
                .timeout(std::time::Duration::from_secs(10))
                .build()
                .unwrap(),
            base_url: base_url.trim_end_matches('/').to_string(),
        }
    }

    pub async fn get_auth_status(&self) -> Result<AuthStatus, NexusError> {
        let res = self.client.get(format!("{}/api/auth/status", self.base_url))
            .send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;
        
        if res.status() == StatusCode::OK {
            let status = res.json::<AuthStatus>().await?;
            Ok(status)
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn get_quota(&self) -> Result<QuotaInfo, NexusError> {
        let res = self.client.get(format!("{}/api/quota", self.base_url))
            .send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;

        if res.status() == StatusCode::OK {
            let quota = res.json::<QuotaInfo>().await?;
            Ok(quota)
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn get_files(&self) -> Result<Vec<FileEntry>, NexusError> {
        let res = self.client.get(format!("{}/api/files", self.base_url))
            .send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;

        if res.status() == StatusCode::OK {
            let files = res.json::<Vec<FileEntry>>().await?;
            Ok(files)
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn get_tasks(&self) -> Result<Vec<TaskEntry>, NexusError> {
        let res = self.client.get(format!("{}/api/tasks", self.base_url))
            .send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;

        if res.status() == StatusCode::OK {
            let bytes = res.bytes().await?;
            if let Ok(map) = serde_json::from_slice::<HashMap<String, TaskEntry>>(&bytes) {
                Ok(map.into_values().collect())
            } else if let Ok(arr) = serde_json::from_slice::<Vec<TaskEntry>>(&bytes) {
                Ok(arr)
            } else {
                Ok(vec![])
            }
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn search(&self, query: &str) -> Result<Vec<FileEntry>, NexusError> {
        let res = self.client.get(format!("{}/api/search?q={}", self.base_url, query))
            .send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;

        if res.status() == StatusCode::OK {
            let files = res.json::<Vec<FileEntry>>().await?;
            Ok(files)
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn delete_file(&self, id: i64) -> Result<(), NexusError> {
        // The daemon uses /api/files/{id}/permanent for permanent deletion
        let res = self.client.delete(format!("{}/api/files/{}/permanent", self.base_url, id))
            .send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;

        if res.status().is_success() {
            Ok(())
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn upload(&self, path: &str) -> Result<TaskEntry, NexusError> {
        let mut payload = HashMap::new();
        payload.insert("path", path.to_string());
        let res = self.client.post(format!("{}/api/upload", self.base_url))
            .json(&payload).send().await
            .map_err(|e| if e.is_connect() { NexusError::DaemonUnreachable { url: self.base_url.clone() } } else { NexusError::Http(e) })?;

        if res.status().is_success() {
            let task = res.json::<TaskEntry>().await?;
            Ok(task)
        } else {
            Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
        }
    }

    pub async fn execute_command(&self, cmd: &str, args: &[&str]) -> Result<String, NexusError> {
        match cmd {
            "/upload" => {
                let path = args.join(" ");
                let mut payload = HashMap::new();
                payload.insert("path", path);
                let res = self.client.post(format!("{}/api/upload", self.base_url))
                    .json(&payload).send().await?;
                if res.status().is_success() {
                    Ok("Upload started...".into())
                } else {
                    Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
                }
            }
            "/mount" => {
                let res = self.client.post(format!("{}/api/mount", self.base_url)).send().await?;
                if res.status().is_success() {
                    Ok("WebDAV Mount triggered".into())
                } else {
                    Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
                }
            }
            "/studio" => {
                let res = self.client.post(format!("{}/api/studio", self.base_url)).send().await?;
                if res.status().is_success() {
                    Ok("Studio opened in browser".into())
                } else {
                    Err(NexusError::ApiError(res.text().await.unwrap_or_default()))
                }
            }
            "/search" => {
                 let q = args.join(" ");
                 let _ = self.search(&q).await?;
                 Ok(format!("Search completed"))
            }
            _ => Err(NexusError::ApiError(format!("Unknown command: {}", cmd)))
        }
    }
}

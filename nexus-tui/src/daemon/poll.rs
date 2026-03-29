use std::time::Duration;
use tokio::time::sleep;
use reqwest::Client;
use tokio::sync::mpsc::Sender;
use std::collections::HashMap;

use super::DaemonEvent;
use nexus_daemon_client::types::*;
use sysinfo::Networks;

const BASE_URL: &str = "http://localhost:8081/api";

pub async fn poll_auth(client: Client, tx: Sender<DaemonEvent>) {
    loop {
        if let Ok(res) = client.get(format!("{}/auth/status", BASE_URL)).send().await {
            if let Ok(status) = res.json::<AuthStatus>().await {
                let _ = tx.send(DaemonEvent::AuthStatus(status)).await;
            }
        }
        sleep(Duration::from_secs(30)).await;
    }
}

pub async fn poll_quota(client: Client, tx: Sender<DaemonEvent>) {
    loop {
        if let Ok(res) = client.get(format!("{}/quota", BASE_URL)).send().await {
            if let Ok(quota) = res.json::<QuotaInfo>().await {
                let _ = tx.send(DaemonEvent::QuotaUpdated(quota)).await;
            }
        }
        sleep(Duration::from_secs(10)).await;
    }
}

pub async fn poll_files(client: Client, tx: Sender<DaemonEvent>) {
    loop {
        if let Ok(res) = client.get(format!("{}/files", BASE_URL)).send().await {
            if let Ok(files) = res.json::<Vec<FileEntry>>().await {
                let _ = tx.send(DaemonEvent::FilesUpdated(files)).await;
            }
        }
        sleep(Duration::from_secs(15)).await;
    }
}

pub async fn poll_tasks(client: Client, tx: Sender<DaemonEvent>) {
    loop {
        if let Ok(res) = client.get(format!("{}/tasks", BASE_URL)).send().await {
            if let Ok(bytes) = res.bytes().await {
                if let Ok(map) = serde_json::from_slice::<HashMap<String, TaskEntry>>(&bytes) {
                    let tasks: Vec<TaskEntry> = map.into_values().collect();
                    let _ = tx.send(DaemonEvent::TasksUpdated(tasks)).await;
                } else if let Ok(arr) = serde_json::from_slice::<Vec<TaskEntry>>(&bytes) {
                     let _ = tx.send(DaemonEvent::TasksUpdated(arr)).await;
                }
            }
        }
        sleep(Duration::from_secs(2)).await;
    }
}

pub async fn telemetry_loop(tx: Sender<DaemonEvent>) {
    let mut networks = Networks::new_with_refreshed_list();
    loop {
        networks.refresh();
        let mut rx_total = 0;
        let mut tx_total = 0;

        for (_, data) in &networks {
            rx_total += data.received();
            tx_total += data.transmitted();
        }
        // approximate kbps per 1s
        let _rx_kbps = (rx_total as f64) * 8.0 / 1000.0;
        let tx_kbps = (tx_total as f64) * 8.0 / 1000.0;
        
        // We use TX for upload telemetry
        let _ = tx.send(DaemonEvent::TelemetryPoint(tx_kbps)).await;

        sleep(Duration::from_secs(1)).await;
    }
}

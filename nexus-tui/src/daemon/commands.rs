use nexus_daemon_client::client::NexusClient;

pub async fn execute_command(cmd_line: &str) -> Result<String, String> {
    let client = NexusClient::new("http://localhost:8081");
    let parts: Vec<&str> = cmd_line.trim().split_whitespace().collect();
    if parts.is_empty() {
        return Ok(String::new());
    }

    let cmd = parts[0];
    let args = &parts[1..];

    if cmd == "mount" {
        return handle_mount_api("mount").await;
    } else if cmd == "unmount" {
        return handle_mount_api("unmount").await;
    }

    client.execute_command(cmd, args).await.map_err(|e| e.to_string())
}

async fn handle_mount_api(endpoint: &str) -> Result<String, String> {
    let url = format!("http://localhost:8081/api/{}", endpoint);
    let client = reqwest::Client::new();
    match client.get(&url).send().await {
        Ok(res) if res.status().is_success() => Ok(format!("Virtual disk {} requested", endpoint)),
        Ok(res) => Err(format!("Error: {}", res.status())),
        Err(e) => Err(format!("Network error: {}", e)),
    }
}


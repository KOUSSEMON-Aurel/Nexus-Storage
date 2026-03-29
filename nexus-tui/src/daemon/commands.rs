use nexus_daemon_client::client::NexusClient;

pub async fn execute_command(cmd_line: &str) -> Result<String, String> {
    let client = NexusClient::new("http://localhost:8081");
    let parts: Vec<&str> = cmd_line.trim().split_whitespace().collect();
    if parts.is_empty() {
        return Ok(String::new());
    }

    let cmd = parts[0];
    let args = &parts[1..];

    client.execute_command(cmd, args).await.map_err(|e| e.to_string())
}

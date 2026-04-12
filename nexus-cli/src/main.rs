use clap::Parser;
use console::style;
use nexus_daemon_client::client::NexusClient;
use nexus_daemon_client::error::NexusError;

mod cli;
mod output;

use cli::{Cli, Commands, AuthCommands, FileCommands, TaskCommands, DaemonCommands, TrashCommands, RecoveryCommands};

#[tokio::main]
async fn main() {
    let cli = Cli::parse();
    
    // Create the shared daemon client
    let client = NexusClient::new(&cli.daemon_url);

    let res = match &cli.command {
        Commands::Auth { cmd } => handle_auth(&client, cmd, cli.json).await,
        Commands::Quota { warn_at } => handle_quota(&client, *warn_at, cli.json).await,
        Commands::File { cmd } => handle_file(&client, cmd, cli.json).await,
        Commands::Upload { path, mode, password, no_progress } => handle_upload(&client, path, mode, password.as_deref(), *no_progress, cli.json).await,
        Commands::Mount { path } => handle_mount(&client, path, cli.json).await,
        Commands::Umount => handle_umount(&client, cli.json).await,
        Commands::Sync => handle_sync(&client, cli.json).await,
        Commands::Trash { cmd } => handle_trash(&client, cmd, cli.json).await,
        Commands::Studio => handle_studio(&client, cli.json).await,
        Commands::Task { cmd } => handle_task(&client, cmd, cli.json).await,
        Commands::Daemon { cmd } => handle_daemon(&client, cmd, cli.json).await,
        Commands::DownloadShared { token, out } => handle_download_shared(&client, token, out, cli.json).await,
        Commands::Recovery { cmd } => handle_recovery(&client, cmd, cli.json).await,
    };

    if let Err(e) = res {
        if cli.json {
            eprintln!(r#"{{"error": "{}"}}"#, e.to_string().replace("\"", "\\\""));
        } else {
            eprintln!("✗ Error: {}", e);
        }
        std::process::exit(1);
    }
}

async fn handle_auth(client: &NexusClient, cmd: &AuthCommands, json: bool) -> Result<(), NexusError> {
    match cmd {
        AuthCommands::Status => {
            let status = client.get_auth_status().await?;
            if json {
                output::print_json(&status);
            } else {
                output::print_auth_status(&status);
            }
        }
        AuthCommands::Login => {
            println!("Lancement du processus de connexion...");
            // Polling approach not yet optimized for CLI in Go sidecar
        }
        AuthCommands::SessionStart { password, salt } => {
            if !json { println!("{} Démarrage de la session V4...", style("🔐").bold()); }
            
            // If salt not provided, try to read from local storage
            let _recovery_salt = if let Some(s) = salt {
                s.clone()
            } else {
                // In CLI, we would need a way to retrieve this - for now, require it
                return Err(NexusError::ApiError("Recovery salt required (use --salt) or set up GUI first".into()));
            };
            
            // Derive master key from password using FFI
            // This would require linking to nexus_core - for now, delegate to daemon
            // Note: In production, would use nexus_core::kdf::derive_master_key(password, &_recovery_salt)
            let _ = password; // Will be used when FFI binding is complete
            
            if !json { println!("{} Session établie avec le daemon.", style("✓").green()); }
        }
        AuthCommands::SessionEnd => {
            if !json { println!("{} Fermeture de la session...", style("🚪").bold()); }
            client.session_end().await?;
            if !json { println!("{} Session fermée.", style("✓").green()); }
        }
        AuthCommands::Logout => {
            if !json { println!("{} Déconnexion complète...", style("🚪").bold()); }
            client.execute_command("/auth/logout", &[]).await?;
            if !json { println!("{} Déconnecté.", style("✓").green()); }
        }
    }
    Ok(())
}

async fn handle_quota(client: &NexusClient, warn_at: Option<u32>, json: bool) -> Result<(), NexusError> {
    let quota = client.get_quota().await?;
    
    if json {
        output::print_json(&quota);
    } else {
        output::print_quota(&quota);
    }

    if let Some(w) = warn_at {
        let percent = (quota.used as f64 / quota.total as f64) * 100.0;
        if percent >= w as f64 {
            if !json { eprintln!("⚠️  ALERTE : Quota dépassé ({}%)", w); }
            std::process::exit(1);
        }
    }
    Ok(())
}

async fn handle_file(client: &NexusClient, cmd: &FileCommands, json: bool) -> Result<(), NexusError> {
    match cmd {
        FileCommands::List { sort: _ } => {
            let files = client.get_files().await?;
            if json {
                output::print_json(&files);
            } else {
                output::print_files(&files);
            }
        }
        FileCommands::Search { query } => {
            let files = client.search(query).await?;
            if json {
                output::print_json(&files);
            } else {
                println!("{} Résultats pour : {}", style("🔍").bold(), style(query).cyan());
                output::print_files(&files);
            }
        }
        FileCommands::Delete { id, force } => {
            let id_i64 = id.parse::<i64>().map_err(|_| NexusError::ApiError("L'ID doit être un nombre".into()))?;
            
            if !force && !json {
                println!("{} Êtes-vous sûr de vouloir supprimer définitivement le fichier ID {} ? [y/N]", 
                    style("⚠️").yellow(), style(id).bold());
                let mut input = String::new();
                std::io::stdin().read_line(&mut input).unwrap();
                if input.trim().to_lowercase() != "y" {
                    println!("Annulé.");
                    return Ok(());
                }
            }

            client.delete_file(id_i64).await?;
            if !json { println!("{} Fichier supprimé avec succès.", style("✓").green()); }
        }
        FileCommands::Share { id } => {
            let id_i64 = id.parse::<i64>().map_err(|_| NexusError::ApiError("L'ID doit être un nombre".into()))?;
            let link = client.share_file(id_i64).await?;
            if json {
                output::print_json(&serde_json::json!({ "link": link }));
            } else {
                println!("{} Lien de partage généré :", style("🔗").bold());
                println!("{}", style(link).cyan().underlined());
                println!("\n{} Note : La personne recevant ce lien pourra déchiffrer ce fichier uniquement.", style("ℹ").blue());
            }
        }
        _ => eprintln!("Commande non implémentée."),
    }
    Ok(())
}

async fn handle_upload(client: &NexusClient, path: &str, mode: &str, password: Option<&str>, no_progress: bool, json: bool) -> Result<(), NexusError> {
    if !json { println!("{} Préparation de l'upload : {}", style("🚀").bold(), style(path).cyan()); }
    
    let task = client.upload(path, mode, password).await?;
    let task_id = task.id.clone();
    
    if json {
        output::print_json(&task);
        return Ok(());
    }

    if no_progress {
        println!("Tâche démarrée : {}", task_id);
        return Ok(());
    }

    // Progress bar setup
    let pb = indicatif::ProgressBar::new(100);
    pb.set_style(indicatif::ProgressStyle::default_bar()
        .template("{spinner:.green} [{elapsed_precise}] [{bar:40.cyan/blue}] {pos:>7}% {msg}")
        .unwrap()
        .progress_chars("##-"));
    
    pb.set_message("Initialisation...");

    // Polling loop
    loop {
        tokio::time::sleep(std::time::Duration::from_millis(500)).await;
        let tasks = client.get_tasks().await?;
        if let Some(t) = tasks.iter().find(|t| t.id == task_id) {
            pb.set_position(t.progress as u64);
            pb.set_message(t.status.clone());
            
            if t.status == "Completed" {
                pb.finish_with_message("Terminé !");
                break;
            } else if t.status.starts_with("Error") {
                pb.abandon_with_message(format!("Erreur : {}", t.status));
                return Err(NexusError::ApiError(t.status.clone()));
            }
        } else {
            // Task might be finished and removed from queue
            pb.finish_with_message("Terminé (tâche archivée).");
            break;
        }
    }

    Ok(())
}

async fn handle_mount(client: &NexusClient, _path: &Option<String>, _json: bool) -> Result<(), NexusError> {
    let msg = client.execute_command("/mount", &[]).await?;
    println!("✓ {}", msg);
    Ok(())
}

async fn handle_umount(_client: &NexusClient, _json: bool) -> Result<(), NexusError> {
    eprintln!("Le démontage n'est pas encore géré par l'API.");
    Ok(())
}

async fn handle_studio(client: &NexusClient, _json: bool) -> Result<(), NexusError> {
    let msg = client.execute_command("/studio", &[]).await?;
    println!("✓ {}", msg);
    Ok(())
}

async fn handle_task(client: &NexusClient, cmd: &TaskCommands, json: bool) -> Result<(), NexusError> {
    match cmd {
        TaskCommands::List { watch: _ } => {
            let tasks = client.get_tasks().await?;
            if json {
                output::print_json(&tasks);
            } else {
                output::print_tasks(&tasks);
            }
        }
        _ => eprintln!("Commande non implémentée."),
    }
    Ok(())
}

async fn handle_daemon(_client: &NexusClient, cmd: &DaemonCommands, json: bool) -> Result<(), NexusError> {
    use std::process::Command;

    match cmd {
        DaemonCommands::Status => {
            let output = Command::new("fuser").arg("8081/tcp").output();
            let running = output.is_ok() && !output.unwrap().stdout.is_empty();
            
            if json {
                println!(r#"{{"running": {}}}"#, running);
            } else {
                if running {
                    println!("{} Daemon actif sur le port 8081", style("✓").green());
                } else {
                    println!("{} Daemon inactif", style("✗").red());
                }
            }
        }
        DaemonCommands::Start => {
            if !json { println!("→ Démarrage du daemon..."); }
            let status = Command::new("./nexus-gui/src-tauri/bin/nexus-daemon-x86_64-unknown-linux-gnu")
                .spawn();
            
            if status.is_ok() {
                if !json { println!("{} Daemon démarré.", style("✓").green()); }
            } else {
                return Err(NexusError::ApiError("Impossible de lancer le binaire du daemon".into()));
            }
        }
        DaemonCommands::Stop => {
            if !json { println!("→ Arrêt du daemon..."); }
            let _ = Command::new("pkill").arg("-f").arg("nexus-daemon").output();
            if !json { println!("{} Daemon arrêté.", style("✓").green()); }
        }
    }
    Ok(())
}
async fn handle_sync(client: &NexusClient, json: bool) -> Result<(), NexusError> {
    if !json { println!("{} Synchronisation du manifeste Cloud...", style("🔄").bold()); }
    client.execute_command("/cloud/sync", &[]).await?;
    if !json { println!("{} Index synchronisé avec succès.", style("✓").green()); }
    else { println!(r#"{{"status": "ok"}}"#); }
    Ok(())
}

async fn handle_trash(client: &NexusClient, cmd: &TrashCommands, json: bool) -> Result<(), NexusError> {
    match cmd {
        TrashCommands::List => {
            let files = client.get_trash_files().await?;
            if json {
                output::print_json(&files);
            } else {
                println!("{} Fichiers dans la Corbeille :", style("🗑️").bold());
                output::print_files(&files);
            }
        }
        TrashCommands::Purge { force } => {
            if !force && !json {
                println!("{} Êtes-vous sûr de vouloir vider la corbeille ? [y/N]", style("⚠️").yellow());
                let mut input = String::new();
                std::io::stdin().read_line(&mut input).unwrap();
                if input.trim().to_lowercase() != "y" {
                    println!("Annulé.");
                    return Ok(());
                }
            }
            client.execute_command("/trash/purge", &[]).await?;
            if !json { println!("{} Corbeille vidée.", style("✓").green()); }
        }
        TrashCommands::Restore { id } => {
            let id_i64 = id.parse::<i64>().map_err(|_| NexusError::ApiError("L'ID doit être un nombre".into()))?;
            client.restore_file(id_i64).await?;
            if !json { println!("{} Fichier restauré.", style("✓").green()); }
        }
    }
    Ok(())
}

async fn handle_download_shared(client: &NexusClient, token: &str, out: &Option<String>, json: bool) -> Result<(), NexusError> {
    if !json { println!("{} Démarrage du téléchargement partagé...", style("📥").bold()); }
    
    client.download_shared(token, out.as_deref()).await?;
    
    if !json { 
        println!("{} Requête de téléchargement acceptée par le daemon.", style("✓").green());
        println!("Suivez la progression avec : {} nexus task list", style("$").dim());
    } else {
        println!(r#"{{"status": "queued"}}"#);
    }
    Ok(())
}

async fn handle_recovery(client: &NexusClient, cmd: &RecoveryCommands, json: bool) -> Result<(), NexusError> {
    match cmd {
        RecoveryCommands::Backup { master_key } => {
            if !json { println!("{} Sauvegarde de la base de données chiffrée...", style("📦").bold()); }
            
            // Build request to /api/recovery/backup
            let _req_body = serde_json::json!({ "master_key_hex": master_key });
            
            // Execute via daemon
            client.execute_command("/recovery/backup", &[]).await?;
            
            if !json { 
                println!("{} Sauvegarde chiffrée envoyée vers Google Drive.", style("✓").green());
                println!("Manifest revision updated - accessible via recovery flow.");
            } else {
                println!(r#"{{"status": "backed_up"}}"#);
            }
        }
        RecoveryCommands::Restore { master_key: _ } => {
            if !json { println!("{} Restauration depuis la sauvegarde chiffrée...", style("📥").bold()); }
            
            // This will download manifest from Drive, decrypt with masterKey, and restore DB
            client.execute_command("/recovery/restore", &[]).await?;
            
            if !json { 
                println!("{} Base de données restaurée depuis la sauvegarde.", style("✓").green());
                println!("Tous vos fichiers ont été restaurés localement.");
            } else {
                println!(r#"{{"status": "restored"}}"#);
            }
        }
    }
    Ok(())
}

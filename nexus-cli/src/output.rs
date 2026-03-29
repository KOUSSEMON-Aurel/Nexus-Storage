use console::{style, Emoji};
use nexus_daemon_client::types::*;
use serde::Serialize;

static LOOKING_GLASS: Emoji<'_, '_> = Emoji("🔍 ", "");
static CHECK: Emoji<'_, '_> = Emoji("✓ ", "");
#[allow(dead_code)]
static CROSS: Emoji<'_, '_> = Emoji("✗ ", "");
#[allow(dead_code)]
static CLOUD: Emoji<'_, '_> = Emoji("☁️  ", "");

pub fn print_json<T: Serialize>(data: &T) {
    println!("{}", serde_json::to_string_pretty(data).unwrap());
}

pub fn format_size(bytes: i64) -> String {
    if bytes <= 0 { return "0 B".to_string(); }
    let units = ["B", "KB", "MB", "GB", "TB"];
    let mut size = bytes as f64;
    let mut unit = 0;
    while size >= 1024.0 && unit < units.len() - 1 {
        size /= 1024.0;
        unit += 1;
    }
    format!("{:.1} {}", size, units[unit])
}

pub fn print_auth_status(status: &AuthStatus) {
    if status.authenticated {
        println!("{} {}", style("Statut    :").bold(), style("✓ Connecté").green());
        println!("{} @{}", style("Canal     :").bold(), style(&status.channel_title).cyan());
    } else {
        println!("{} {}", style("Statut    :").bold(), style("✗ Déconnecté").red());
    }
}

pub fn print_quota(quota: &QuotaInfo) {
    let mut percent = 0.0;
    if quota.total > 0 {
        percent = (quota.used as f64 / quota.total as f64) * 100.0;
    }

    println!("{}", style("Quota journalier").bold().underlined());
    println!("  Utilisé  : {} / {} unités ({:.1}%)", 
        style(quota.used).yellow(), 
        style(quota.total).dim(),
        percent
    );

    // Simple progress bar
    let width = 30;
    let filled = ((percent / 100.0) * width as f64) as usize;
    let bar: String = (0..width).map(|i| if i < filled { '█' } else { '░' }).collect();
    
    let color_style = if percent < 70.0 { style(bar).green() } 
                     else if percent < 90.0 { style(bar).yellow() } 
                     else { style(bar).red() };
    
    println!("  [{}]", color_style);
}

pub fn print_files(files: &[FileEntry]) {
    if files.is_empty() {
        println!("{} Aucun fichier trouvé.", LOOKING_GLASS);
        return;
    }

    println!("{:<30} {:<12} {:<15} {}", 
        style("NOM").bold(), 
        style("TAILLE").bold(), 
        style("DATE").bold(), 
        style("ID SHARD").bold()
    );
    println!("{}", style("─".repeat(75)).dim());

    for f in files {
        let name = f.path.split('/').last().unwrap_or(&f.path);
        let name_trimmed = if name.len() > 28 { format!("{}...", &name[..25]) } else { name.to_string() };
        
        println!("{:<30} {:<12} {:<15} {}", 
            name_trimmed,
            format_size(f.size),
            if f.last_update.len() > 10 { &f.last_update[..10] } else { &f.last_update },
            style(&f.video_id).dim()
        );
    }
}

pub fn print_tasks(tasks: &[TaskEntry]) {
    if tasks.is_empty() {
        println!("{} Aucune tâche en cours.", CHECK);
        return;
    }

    println!("{:<12} {:<30} {:<12} {}", 
        style("ID").bold(), 
        style("NOM").bold(), 
        style("STATUT").bold(), 
        style("PROGRESSION").bold()
    );
    println!("{}", style("─".repeat(70)).dim());

    for t in tasks {
        let name = t.file_path.split('/').last().unwrap_or(&t.file_path);
        let name_trimmed = if name.len() > 28 { format!("{}...", &name[..25]) } else { name.to_string() };
        
        let status_style = match t.status.as_str() {
            "Completed" => style(&t.status).green(),
            s if s.starts_with("Error") => style(&t.status).red(),
            _ => style(&t.status).cyan(),
        };

        println!("{:<12} {:<30} {:<12} {:.1}%", 
            style(&t.id).dim(),
            name_trimmed,
            status_style,
            t.progress
        );
    }
}

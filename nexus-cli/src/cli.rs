use clap::{Parser, Subcommand};

#[derive(Parser)]
#[command(name = "nexus", author, version, about, long_about = None)]
#[command(propagate_version = true)]
pub struct Cli {
    #[arg(long, global = true, help = "Output machine-readable JSON")]
    pub json: bool,

    #[arg(short, long, global = true, help = "Suppress informative messages")]
    pub quiet: bool,

    #[arg(long, global = true, default_value = "http://localhost:8081", help = "Daemon URL override")]
    pub daemon_url: String,

    #[arg(long, global = true, help = "Disable ANSI colors")]
    pub no_color: bool,

    #[command(subcommand)]
    pub command: Commands,
}

#[derive(Subcommand)]
pub enum Commands {
    /// Authentication management
    Auth {
        #[command(subcommand)]
        cmd: AuthCommands,
    },
    /// File and index management
    File {
        #[command(subcommand)]
        cmd: FileCommands,
    },
    /// Direct shortcut to upload a file (same as `nexus file upload`)
    Upload {
        path: String,
        #[arg(long, help = "Disable progress bar")]
        no_progress: bool,
    },
    /// View daily YouTube API quota
    Quota {
        #[arg(long, help = "Set a percentage warning threshold (ex: 80)")]
        warn_at: Option<u32>,
    },
    /// Mount the virtual WebDAV filesystem
    Mount {
        #[arg(long, help = "Custom mount path override")]
        path: Option<String>,
    },
    /// Unmount the virtual WebDAV filesystem
    Umount,
    /// Open YouTube Studio in your default browser
    Studio,
    /// View active background process queue
    Task {
        #[command(subcommand)]
        cmd: TaskCommands,
    },
    /// Manage the local Nexus background daemon
    Daemon {
        #[command(subcommand)]
        cmd: DaemonCommands,
    },
}

#[derive(Subcommand)]
pub enum AuthCommands {
    /// Launches the browser to login to Google OAuth
    Login,
    /// Displays currently linked YouTube channel status
    Status,
}

#[derive(Subcommand)]
pub enum FileCommands {
    /// Lists all files in the decentralized index
    List {
        #[arg(long, default_value = "date", help = "Sort by: name, size, date")]
        sort: String,
    },
    /// Search via offline FTS5 engine
    Search {
        query: String,
    },
    /// Display full metadata properties for a file
    Info {
        id: String,
    },
    /// Delete a file from the index and soft-delete YouTube tracking
    Delete {
        id: String,
        #[arg(long, help = "Skip deletion confirmation prompt")]
        force: bool,
    },
}

#[derive(Subcommand)]
pub enum TaskCommands {
    /// Lists active and completed daemon syncs
    List {
        #[arg(long, help = "Auto refresh (watch mode)")]
        watch: bool,
    },
    /// Cancels a running task ID
    Cancel {
        id: String,
    },
}

#[derive(Subcommand)]
pub enum DaemonCommands {
    /// Check if daemon is active
    Status,
    /// Start daemon process in background
    Start,
    /// Terminate daemon safely
    Stop,
}

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
        #[arg(long, default_value = "base", help = "Encoding mode (base or high)")]
        mode: String,
        #[arg(long, help = "Optional encryption password")]
        password: Option<String>,
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
    /// Download a file shared by someone else via a token
    DownloadShared {
        token: String,
        #[arg(short, long, help = "Local output path")]
        out: Option<String>,
    },
    /// Explicitly synchronize local index with YouTube metadata
    Sync,
    /// Manage deleted files and trash retention
    Trash {
        #[command(subcommand)]
        cmd: TrashCommands,
    },
    /// Data recovery and backup management (V4)
    Recovery {
        #[command(subcommand)]
        cmd: RecoveryCommands,
    },
}

#[derive(Subcommand)]
pub enum AuthCommands {
    /// Launches the browser to login to Google OAuth
    Login,
    /// Displays currently linked YouTube channel status
    Status,
    /// Start V4 session with password (derives master key)
    SessionStart {
        #[arg(long, help = "User password")]
        password: String,
        #[arg(long, help = "Recovery salt (hex) - stored locally during setup")]
        salt: Option<String>,
    },
    /// End current V4 session (clear server-side master key)
    SessionEnd,
    /// Logout completely
    Logout,
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
    /// Generate a per-file shareable encryption link (V3)
    Share {
        id: String,
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

#[derive(Subcommand)]
pub enum TrashCommands {
    /// Lists all files in the trash
    List,
    /// Permanently purge items from the trash
    Purge {
        #[arg(long, help = "Skip confirmation")]
        force: bool,
    },
    /// Restore a file from trash back to My Drive
    Restore {
        id: String,
    },
}

#[derive(Subcommand)]
pub enum RecoveryCommands {
    /// Backup encrypted database to Google Drive
    Backup {
        #[arg(long, help = "Master key (hex format) - from session")]
        master_key: String,
    },
    /// Restore encrypted database from Google Drive
    Restore {
        #[arg(long, help = "Master key (hex format) - from session")]
        master_key: String,
    },
}

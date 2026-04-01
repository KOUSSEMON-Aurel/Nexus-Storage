pub mod poll;
pub mod commands;
pub mod auth;

pub use nexus_daemon_client::types::*;
pub use poll::*;
pub use commands::*;
pub use auth::*;


#[derive(Debug)]
pub enum DaemonEvent {
    AuthStatus(AuthStatus),
    QuotaUpdated(QuotaInfo),
    FilesUpdated(Vec<FileEntry>),
    TasksUpdated(Vec<TaskEntry>),
    MountStatus(bool),
    TelemetryPoint(f64),
    CommandResult(String),
    Error(String),
}

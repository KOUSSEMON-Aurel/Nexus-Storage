pub mod poll;
pub mod commands;

pub use nexus_daemon_client::types::*;
pub use poll::*;
pub use commands::*;


#[derive(Debug)]
pub enum DaemonEvent {
    AuthStatus(AuthStatus),
    QuotaUpdated(QuotaInfo),
    FilesUpdated(Vec<FileEntry>),
    TasksUpdated(Vec<TaskEntry>),
    TelemetryPoint(f64),
    CommandResult(String),
    Error(String),
}

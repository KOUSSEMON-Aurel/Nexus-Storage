use std::collections::VecDeque;
use std::time::Instant;

use crate::daemon::{AuthStatus, FileEntry, QuotaInfo, TaskEntry};

#[derive(Debug, PartialEq)]
pub enum AppMode {
    Normal,
    Loading,
    CommandInput,
    SearchFilter,
    #[allow(dead_code)]
    Confirm(String),
    Help,
    Authentication,      // New: Authentication screen (password input for V4)
    RecoveryMode,         // New: Recovery/restore workflow
}

#[allow(dead_code)]
pub enum NotifLevel {
    Info,
    Success,
    Warning,
    Error,
}

pub struct Notification {
    pub message: String,
    pub level: NotifLevel,
    pub expires_at: Instant,
}

#[allow(dead_code)]
pub enum SortMode {
    Name,
    Size,
    Date,
}

pub struct AppState {
    pub auth: AuthStatus,
    #[allow(dead_code)]
    pub user_channel: String,
    pub is_mounted: bool,

    pub files: Vec<FileEntry>,
    pub filtered_files: Vec<FileEntry>,
    pub selected_idx: usize,
    #[allow(dead_code)]
    pub sort: SortMode,

    pub quota: QuotaInfo,

    pub upload_history: VecDeque<f64>,
    pub current_kbps: f64,

    pub tasks: Vec<TaskEntry>,

    pub mode: AppMode,
    pub command_input: String,
    pub command_history: Vec<String>,
    #[allow(dead_code)]
    pub cmd_history_idx: Option<usize>,

    pub notification: Option<Notification>,
    pub should_quit: bool,
}

impl AppState {
    pub fn new() -> Self {
        AppState {
            auth: AuthStatus { authenticated: false, channel_title: String::new() },
            user_channel: String::new(),
            is_mounted: false,
            files: vec![],
            filtered_files: vec![],
            selected_idx: 0,
            sort: SortMode::Date,
            quota: QuotaInfo { used: 0, total: 10000, reset_in_secs: 0 },
            upload_history: {
                let mut v = VecDeque::with_capacity(60);
                for _ in 0..60 { v.push_back(0.0) }
                v
            },
            current_kbps: 0.0,
            tasks: vec![],
            mode: AppMode::Loading,
            command_input: String::new(),
            command_history: vec![],
            cmd_history_idx: None,
            notification: None,
            should_quit: false,
        }
    }

    pub fn is_ready(&self) -> bool {
        self.mode != AppMode::Loading
    }

    pub fn apply_event(&mut self, event: crate::daemon::DaemonEvent) {
        match event {
            crate::daemon::DaemonEvent::AuthStatus(status) => {
                self.auth = status;
            }
            crate::daemon::DaemonEvent::QuotaUpdated(quota) => {
                self.quota = quota;
            }
            crate::daemon::DaemonEvent::MountStatus(mounted) => {
                self.is_mounted = mounted;
            }
            crate::daemon::DaemonEvent::FilesUpdated(files) => {
                self.files = files.clone();
                // simple refresh logic
                if self.mode != AppMode::SearchFilter {
                    self.filtered_files = files;
                }
            }
            crate::daemon::DaemonEvent::TasksUpdated(tasks) => {
                self.tasks = tasks;
            }
            crate::daemon::DaemonEvent::TelemetryPoint(kbps) => {
                self.current_kbps = kbps;
                self.upload_history.push_back(kbps);
                if self.upload_history.len() > 60 {
                    self.upload_history.pop_front();
                }
            }
            crate::daemon::DaemonEvent::CommandResult(res) => {
                self.show_notification(res, NotifLevel::Success, 4);
            }
            crate::daemon::DaemonEvent::Error(err) => {
                self.show_notification(err, NotifLevel::Error, 6);
            }
        }
    }

    pub fn handle_key(&mut self, key: crossterm::event::KeyEvent) {
        use crossterm::event::{KeyCode, KeyModifiers};
        match self.mode {
            AppMode::Normal => match key.code {
                KeyCode::Char('q') | KeyCode::Esc => self.should_quit = true,
                KeyCode::Char('j') | KeyCode::Down => {
                    if !self.filtered_files.is_empty() {
                        self.selected_idx = (self.selected_idx + 1) % self.filtered_files.len();
                    }
                }
                KeyCode::Char('k') | KeyCode::Up => {
                    if !self.filtered_files.is_empty() {
                        self.selected_idx = self.selected_idx.checked_sub(1).unwrap_or(self.filtered_files.len() - 1);
                    }
                }
                KeyCode::Char(':') => {
                    self.mode = AppMode::CommandInput;
                    self.command_input.clear();
                    self.command_input.push('/');
                }
                KeyCode::Char('?') => {
                    self.mode = AppMode::Help;
                }
                _ => {}
            },
            AppMode::CommandInput => match key.code {
                KeyCode::Esc => self.mode = AppMode::Normal,
                KeyCode::Char('c') if key.modifiers.contains(KeyModifiers::CONTROL) => self.mode = AppMode::Normal,
                KeyCode::Char(c) => self.command_input.push(c),
                KeyCode::Backspace => {
                    self.command_input.pop();
                    if self.command_input.is_empty() {
                        self.mode = AppMode::Normal;
                    }
                }
                KeyCode::Enter => {
                    let cmd = self.command_input.clone();
                    self.command_history.push(cmd.clone());
                    self.mode = AppMode::Normal;
                    
                    if cmd.starts_with("/upload ") {
                        let _path = &cmd[8..];
                        // Trigger upload via internal channel or daemon client
                    } else if cmd.starts_with("/password ") {
                        // Set global session password
                    }
                }
                _ => {}
            },
            AppMode::Help => match key.code {
                KeyCode::Esc | KeyCode::Char('q') | KeyCode::Char('?') => self.mode = AppMode::Normal,
                _ => {}
            },
            _ => {}
        }
    }

    pub fn show_notification(&mut self, msg: String, level: NotifLevel, ttl_secs: u64) {
        self.notification = Some(Notification {
            message: msg,
            level,
            expires_at: Instant::now() + std::time::Duration::from_secs(ttl_secs),
        });
    }

    pub fn clean_notifications(&mut self) {
        if let Some(n) = &self.notification {
            if Instant::now() > n.expires_at {
                self.notification = None;
            }
        }
    }
}

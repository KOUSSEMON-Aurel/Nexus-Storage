use anyhow::Result;
use crossterm::{
    event::{self, DisableMouseCapture, EnableMouseCapture, Event, KeyEventKind},
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use ratatui::{backend::CrosstermBackend, Terminal};
use reqwest::Client;
use std::{io, sync::{Arc, Mutex}, time::Duration};
use tokio::sync::mpsc;

mod app;
mod daemon;
mod theme;
mod ui;

use app::{AppMode, AppState};
use daemon::DaemonEvent;

#[tokio::main]
async fn main() -> Result<()> {
    enable_raw_mode()?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen, EnableMouseCapture)?;
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let app = Arc::new(Mutex::new(AppState::new()));
    let (tx, mut rx) = mpsc::channel::<DaemonEvent>(32);

    let client = Client::builder().timeout(Duration::from_secs(2)).build()?;

    // Spawn async daemon fetchers
    let tx_auth = tx.clone();
    let c_auth = client.clone();
    tokio::spawn(async move { daemon::poll_auth(c_auth, tx_auth).await; });

    let tx_quota = tx.clone();
    let c_quota = client.clone();
    tokio::spawn(async move { daemon::poll_quota(c_quota, tx_quota).await; });

    let tx_files = tx.clone();
    let c_files = client.clone();
    tokio::spawn(async move { daemon::poll_files(c_files, tx_files).await; });

    let tx_tasks = tx.clone();
    let c_tasks = client.clone();
    tokio::spawn(async move { daemon::poll_tasks(c_tasks, tx_tasks).await; });

    let tx_telem = tx.clone();
    tokio::spawn(async move { daemon::telemetry_loop(tx_telem).await; });

    loop {
        // Drain incoming daemon events
        while let Ok(event) = rx.try_recv() {
            let mut state = app.lock().unwrap();
            state.apply_event(event);
        }

        // Draw UI
        {
            let mut state = app.lock().unwrap();
            state.clean_notifications();
        }
        
        // Let's hold the lock just inside drawing
        terminal.draw(|f| {
            let mut state = app.lock().unwrap();
            ui::render(f, &mut state);
            if state.mode == AppMode::Help {
                ui::helpers::draw_help_popup(f, f.area());
            }
        })?;

        // Handle Keys
        if event::poll(Duration::from_millis(50))? {
            if let Event::Key(key) = event::read()? {
                if key.kind == KeyEventKind::Press {
                    let mut state = app.lock().unwrap();
                    let was_cmd_mode = state.mode == AppMode::CommandInput;
                    
                    state.handle_key(key);
                    
                    // Detect if Enter was pressed in CommandInput mode
                    if was_cmd_mode && state.mode == AppMode::Normal {
                        // check if command history was appended
                        if let Some(cmd) = state.command_history.last().cloned() {
                            let tx_cmd = tx.clone();
                            tokio::spawn(async move {
                                match daemon::execute_command(&cmd).await {
                                    Ok(res) => {
                                        let _ = tx_cmd.send(DaemonEvent::CommandResult(res)).await;
                                    }
                                    Err(err) => {
                                        let _ = tx_cmd.send(DaemonEvent::Error(err)).await;
                                    }
                                }
                            });
                        }
                    }

                    // For search filter inline parsing
                    if state.mode == AppMode::CommandInput && state.command_input.starts_with("/search ") {
                        let query = state.command_input.replace("/search ", "");
                        let filtered = state.files.iter().filter(|f| {
                            f.path.to_lowercase().contains(&query.to_lowercase())
                        }).cloned().collect::<Vec<_>>();
                        state.filtered_files = filtered;
                        state.selected_idx = 0;
                    } else if state.mode == AppMode::Normal {
                        // Reset filter
                        let all_files = state.files.clone();
                        state.filtered_files = all_files;
                    }
                }
            }
        }

        if app.lock().unwrap().should_quit {
            break;
        }
    }

    disable_raw_mode()?;
    execute!(terminal.backend_mut(), LeaveAlternateScreen, DisableMouseCapture)?;
    terminal.show_cursor()?;

    Ok(())
}

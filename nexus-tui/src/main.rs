use anyhow::Result;
use crossterm::{
    event::{self, DisableMouseCapture, EnableMouseCapture, Event, KeyCode},
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use ratatui::{
    backend::{Backend, CrosstermBackend},
    layout::{Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, List, ListItem, ListState, Paragraph, Gauge},
    Frame, Terminal,
};
use std::{io, time::Duration};

struct App {
    items: Vec<String>,
    state: ListState,
    progress: u16,
}

impl App {
    fn new() -> App {
        let mut state = ListState::default();
        state.select(Some(0));
        App {
            items: vec![
                "nexus_v1_backup.rar (450 MB)".to_string(),
                "Photos_Work_2024 (12 GB)".to_string(),
                "private_keys.enc (2 KB)".to_string(),
                "Project_Aurora_Source.zip (1.2 GB)".to_string(),
                "Family_Holidays_4K.mp4 (8.5 GB)".to_string(),
                "Secrets_Vault.nexus (12 KB)".to_string(),
            ],
            state,
            progress: 65,
        }
    }

    fn next(&mut self) {
        let i = match self.state.selected() {
            Some(i) => {
                if i >= self.items.len() - 1 {
                    0
                } else {
                    i + 1
                }
            }
            None => 0,
        };
        self.state.select(Some(i));
    }

    fn previous(&mut self) {
        let i = match self.state.selected() {
            Some(i) => {
                if i == 0 {
                    self.items.len() - 1
                } else {
                    i - 1
                }
            }
            None => 0,
        };
        self.state.select(Some(i));
    }
}

fn main() -> Result<()> {
    // Setup terminal
    enable_raw_mode()?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen, EnableMouseCapture)?;
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    // Create app and run it
    let app = App::new();
    let res = run_app(&mut terminal, app);

    // Restore terminal
    disable_raw_mode()?;
    execute!(
        terminal.backend_mut(),
        LeaveAlternateScreen,
        DisableMouseCapture
    )?;
    terminal.show_cursor()?;

    if let Err(err) = res {
        println!("{:?}", err)
    }

    Ok(())
}

fn run_app<B: Backend>(terminal: &mut Terminal<B>, mut app: App) -> io::Result<()> {
    loop {
        terminal.draw(|f| ui(f, &mut app))?;

        if event::poll(Duration::from_millis(100))? {
            if let Event::Key(key) = event::read()? {
                match key.code {
                    KeyCode::Char('q') => return Ok(()),
                    KeyCode::Down => app.next(),
                    KeyCode::Up => app.previous(),
                    _ => {}
                }
            }
        }
    }
}

fn ui(f: &mut Frame, app: &mut App) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3),
            Constraint::Min(0),
            Constraint::Length(3),
        ])
        .split(f.size());

    // Title
    let title = Paragraph::new(Line::from(vec![
        Span::styled(" NexusStorage ", Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD)),
        Span::raw(" | Decentralized YouTube Drive"),
    ]))
    .block(Block::default().borders(Borders::ALL).border_style(Style::default().fg(Color::DarkGray)));
    f.render_widget(title, chunks[0]);

    // Body
    let body_chunks = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(40), Constraint::Percentage(60)])
        .split(chunks[1]);

    // File List
    let items: Vec<ListItem> = app
        .items
        .iter()
        .map(|i| {
            ListItem::new(Line::from(vec![
                Span::styled(" 󰈔 ", Style::default().fg(Color::Blue)),
                Span::raw(i),
            ]))
        })
        .collect();
    let list = List::new(items)
        .block(Block::default().title(" Files ").borders(Borders::ALL))
        .highlight_style(
            Style::default()
                .bg(Color::Rgb(30, 30, 60))
                .add_modifier(Modifier::BOLD),
        )
        .highlight_symbol(">> ");
    f.render_stateful_widget(list, body_chunks[0], &mut app.state);

    // Details Panel
    let selected_index = app.state.selected().unwrap_or(0);
    let selected_name = &app.items[selected_index];
    let details = Paragraph::new(format!(
        "\n  File: {}\n  Status: Stored on YouTube\n  YouTube ID: h6Xw9Gk2\n  Encryption: XChaCha20-Poly1305\n\n  Press [Enter] to download\n  Press [d] to delete\n  Press [q] to quit",
        selected_name
    ))
    .block(Block::default().title(" Details ").borders(Borders::ALL));
    f.render_widget(details, body_chunks[1]);

    // Progress Bar
    let gauge = Gauge::default()
        .block(Block::default().title(" Background Tasks (Uploading...) ").borders(Borders::ALL))
        .gauge_style(Style::default().fg(Color::Cyan).bg(Color::DarkGray))
        .percent(app.progress);
    f.render_widget(gauge, chunks[2]);
}

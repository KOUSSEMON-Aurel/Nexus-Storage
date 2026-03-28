use anyhow::Result;
use crossterm::{
    event::{self, DisableMouseCapture, EnableMouseCapture, Event, KeyCode, KeyEventKind},
    execute,
    terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen},
};
use ratatui::{
    backend::{Backend, CrosstermBackend},
    layout::{Alignment, Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    symbols::{Marker},
    text::{Line, Span},
    widgets::{
        Block, BorderType, Borders, Chart, Clear, Dataset, Gauge, GraphType, List, ListItem, 
        Padding, Paragraph, Row, Table, TableState, Wrap,
    },
    Frame, Terminal,
};
use reqwest::Client;
use serde::Deserialize;
use std::{
    io,
    sync::{Arc, Mutex},
    time::{Duration, Instant},
};
use sysinfo::{Networks, System};
use tokio::time::sleep;

// ─── API Types ────────────────────────────────────────────────────────────────

#[derive(Debug, Clone, Deserialize)]
struct BackendFile {
    #[serde(rename = "ID")]
    id: i64,
    #[serde(rename = "Path")]
    path: String,
    #[serde(rename = "VideoID")]
    video_id: String,
    #[serde(rename = "Size")]
    size: i64,
    #[serde(rename = "Hash")]
    hash: String,
    #[serde(rename = "LastUpdate")]
    last_update: String,
}

#[derive(Debug, Clone, Deserialize, Default)]
struct Stats {
    file_count: i64,
    total_size: i64,
}

#[derive(Debug, Clone, Deserialize)]
struct BackendTask {
    #[serde(rename = "ID")]
    id: String,
    #[serde(rename = "FilePath")]
    file_path: String,
    #[serde(rename = "Status")]
    status: String,
    #[serde(rename = "Progress")]
    progress: f64,
}

// ─── App State ────────────────────────────────────────────────────────────────

#[derive(Debug)]
struct AppState {
    files: Vec<BackendFile>,
    stats: Stats,
    tasks: Vec<BackendTask>,
    daemon_online: bool,
    last_refresh: Instant,

    // Telemetry
    cpu_history: Vec<(f64, f64)>,
    rx_history: Vec<(f64, f64)>,
    tx_history: Vec<(f64, f64)>,
    max_rx: f64,
    max_tx: f64,
    tick_count: f64,

    // System monitor instance
    sys: System,
    networks: Networks,
    last_net_time: Instant,
}

impl AppState {
    fn new() -> Self {
        let mut sys = System::new();
        let networks = Networks::new_with_refreshed_list();

        // Pre-fill history to have a straight chart initially
        let mut initial_hist = vec![];
        for i in 0..100 {
            initial_hist.push((i as f64, 0.0));
        }

        AppState {
            files: vec![],
            stats: Stats::default(),
            tasks: vec![],
            daemon_online: false,
            last_refresh: Instant::now(),
            cpu_history: initial_hist.clone(),
            rx_history: initial_hist.clone(),
            tx_history: initial_hist,
            max_rx: 10.0,
            max_tx: 10.0,
            tick_count: 100.0,
            sys,
            networks,
            last_net_time: Instant::now(),
        }
    }

    fn update_telemetry(&mut self) {
        self.tick_count += 1.0;
        let t = self.tick_count;

        // CPU Usage
        self.sys.refresh_cpu_usage();
        let cpu_usage = self.sys.global_cpu_info().cpu_usage() as f64;
        self.cpu_history.push((t, cpu_usage));
        if self.cpu_history.len() > 100 {
            self.cpu_history.remove(0);
        }

        // Network
        self.networks.refresh();
        let elapsed = self.last_net_time.elapsed().as_secs_f64().max(0.001);
        self.last_net_time = Instant::now();

        let mut rx_total = 0;
        let mut tx_total = 0;

        for (_, data) in &self.networks {
            rx_total += data.received();
            tx_total += data.transmitted();
        }

        let rx_kbps = (rx_total as f64 / elapsed) * 8.0 / 1000.0;
        let tx_kbps = (tx_total as f64 / elapsed) * 8.0 / 1000.0;

        if rx_kbps > self.max_rx { self.max_rx = rx_kbps; }
        if tx_kbps > self.max_tx { self.max_tx = tx_kbps; }

        // Decay max limits slowly (like btop)
        self.max_rx = (self.max_rx * 0.99).max(10.0);
        self.max_tx = (self.max_tx * 0.99).max(10.0);

        self.rx_history.push((t, rx_kbps));
        self.tx_history.push((t, tx_kbps));

        if self.rx_history.len() > 100 {
            self.rx_history.remove(0);
        }
        if self.tx_history.len() > 100 {
            self.tx_history.remove(0);
        }
    }
}

#[derive(Debug, PartialEq)]
enum InputMode {
    Normal,
    Uploading,
}

struct App {
    state: Arc<Mutex<AppState>>,
    table_state: TableState,
    show_help: bool,
    input_mode: InputMode,
    input: String,
}

impl App {
    fn new(state: Arc<Mutex<AppState>>) -> Self {
        let mut table_state = TableState::default();
        table_state.select(Some(0));
        App {
            state,
            table_state,
            show_help: false,
            input_mode: InputMode::Normal,
            input: String::new(),
        }
    }

    fn next(&mut self) {
        let len = self.state.lock().unwrap().files.len();
        if len == 0 {
            return;
        }
        let i = self.table_state.selected().map_or(0, |i| (i + 1) % len);
        self.table_state.select(Some(i));
    }

    fn previous(&mut self) {
        let len = self.state.lock().unwrap().files.len();
        if len == 0 {
            return;
        }
        let i = self.table_state.selected().map_or(0, |i| {
            if i == 0 { len - 1 } else { i - 1 }
        });
        self.table_state.select(Some(i));
    }
}

// ─── Async Polling ───────────────────────────────────────────────────────────

async fn poll_daemon(state: Arc<Mutex<AppState>>, client: Client) {
    loop {
        let base = "http://localhost:8081/api";
        let results = tokio::join!(
            client.get(format!("{}/files", base)).send(),
            client.get(format!("{}/stats", base)).send(),
            client.get(format!("{}/tasks", base)).send(),
        );

        let mut files_opt = None;
        let mut stats_opt = None;
        let mut tasks_opt = None;
        let mut is_online = false;

        if let Ok(res) = results.0 {
            if let Ok(files) = res.json::<Vec<BackendFile>>().await {
                files_opt = Some(files);
                is_online = true;
            }
        }

        if let Ok(res) = results.1 {
            if let Ok(stats) = res.json::<Stats>().await {
                stats_opt = Some(stats);
            }
        }

        if let Ok(res) = results.2 {
            if let Ok(task_map) = res.json::<std::collections::HashMap<String, BackendTask>>().await {
                tasks_opt = Some(task_map.into_values().collect());
            }
        }

        {
            let mut app = state.lock().unwrap();
            app.daemon_online = is_online;
            if let Some(files) = files_opt {
                app.files = files;
            }
            if let Some(stats) = stats_opt {
                app.stats = stats;
            }
            if let Some(tasks) = tasks_opt {
                app.tasks = tasks;
            }
            app.last_refresh = Instant::now();
        }

        sleep(Duration::from_secs(3)).await;
    }
}

// ─── Entry Point ─────────────────────────────────────────────────────────────

#[tokio::main]
async fn main() -> Result<()> {
    let shared = Arc::new(Mutex::new(AppState::new()));
    let client = Client::builder().timeout(Duration::from_secs(2)).build()?;

    let shared_clone = shared.clone();
    tokio::spawn(async move {
        poll_daemon(shared_clone, client).await;
    });

    let shared_telem = shared.clone();
    tokio::spawn(async move {
        loop {
            {
                let mut state = shared_telem.lock().unwrap();
                state.update_telemetry();
            }
            sleep(Duration::from_millis(250)).await;
        }
    });

    enable_raw_mode()?;
    let mut stdout = io::stdout();
    execute!(stdout, EnterAlternateScreen, EnableMouseCapture)?;
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let mut app = App::new(shared);
    let res = run_app(&mut terminal, &mut app).await;

    disable_raw_mode()?;
    execute!(terminal.backend_mut(), LeaveAlternateScreen, DisableMouseCapture)?;
    terminal.show_cursor()?;

    if let Err(err) = res {
        eprintln!("{:?}", err);
    }

    Ok(())
}

async fn run_app<B: Backend>(terminal: &mut Terminal<B>, app: &mut App) -> io::Result<()> {
    loop {
        terminal.draw(|f| ui(f, app))?;

        if event::poll(Duration::from_millis(50))? {
            if let Event::Key(key) = event::read()? {
                if key.kind != KeyEventKind::Press {
                    continue;
                }

                match app.input_mode {
                    InputMode::Normal => match key.code {
                        KeyCode::Char('q') | KeyCode::Esc => return Ok(()),
                        KeyCode::Char('?') | KeyCode::Char('h') => {
                            app.show_help = !app.show_help;
                        }
                        KeyCode::Down | KeyCode::Char('j') => {
                            app.show_help = false;
                            app.next();
                        }
                        KeyCode::Up | KeyCode::Char('k') => {
                            app.show_help = false;
                            app.previous();
                        }
                        KeyCode::Char('u') => {
                            app.input_mode = InputMode::Uploading;
                            app.input.clear();
                        }
                        KeyCode::Enter => {
                            let state = app.state.lock().unwrap();
                            if let Some(idx) = app.table_state.selected() {
                                if let Some(file) = state.files.get(idx) {
                                    let video_id = file.video_id.clone();
                                    let path = file.path.clone();
                                    let client = Client::new();
                                    tokio::spawn(async move {
                                        let _ = client.post("http://localhost:8081/api/download")
                                            .json(&serde_json::json!({ "video_id": video_id, "path": path }))
                                            .send()
                                            .await;
                                    });
                                }
                            }
                        }
                        _ => {}
                    },
                    InputMode::Uploading => match key.code {
                        KeyCode::Enter => {
                            let path = app.input.clone();
                            let client = Client::new();
                            tokio::spawn(async move {
                                let _ = client.post("http://localhost:8081/api/upload")
                                    .json(&serde_json::json!({ "path": path }))
                                    .send()
                                    .await;
                            });
                            app.input_mode = InputMode::Normal;
                        }
                        KeyCode::Char(c) => {
                            app.input.push(c);
                        }
                        KeyCode::Backspace => {
                            app.input.pop();
                        }
                        KeyCode::Esc => {
                            app.input_mode = InputMode::Normal;
                        }
                        _ => {}
                    },
                }
            }
        }
    }
}

// ─── Theme ───────────────────────────────────────────────────────────────────

const BG_COLOR: Color = Color::Reset; // Typically btop uses transparent/black
const BORDER_COLOR: Color = Color::Rgb(100, 100, 100);
const TITLE_COLOR: Color = Color::Rgb(240, 240, 240);
const HL_BG: Color = Color::Rgb(40, 40, 40);

const CHART_CPU: Color = Color::Rgb(0, 255, 120);
const CHART_TX: Color = Color::Rgb(200, 50, 200); // Magenta-ish
const CHART_RX: Color = Color::Rgb(0, 200, 255);  // Cyan-ish

const ICON_DAEMON_ON: Color = Color::Rgb(0, 255, 50);
const ICON_DAEMON_OFF: Color = Color::Rgb(255, 0, 50);

// ─── UI ───────────────────────────────────────────────────────────────────────

fn ui(f: &mut Frame, app: &mut App) {
    let state = app.state.lock().unwrap();
    let area = f.area();

    // The btop authentic grid (2x2 layout basically)
    let rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Percentage(45), Constraint::Percentage(55)])
        .split(area);

    let top_cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(rows[0]);

    let bot_cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(60), Constraint::Percentage(40)])
        .split(rows[1]);

    // Box 1: CPU / System (Top Left)
    render_cpu_panel(f, top_cols[0], &state);

    // Box 2: Network / IO (Top Right)
    render_network_panel(f, top_cols[1], &state);

    // Box 3: Vault / Files (Bottom Left)
    render_vault_panel(f, bot_cols[0], &state, &mut app.table_state);

    // Box 4: Background Tasks & Cryptography info (Bottom Right)
    render_tasks_panel(f, bot_cols[1], &state);

    if app.show_help {
        render_help_popup(f, area);
    }

    if app.input_mode == InputMode::Uploading {
        render_input_popup(f, area, &app.input);
    }
}

// ─── Btop Box Helper ──────────────────────────────────────────────────────────

fn btop_box(title: &str) -> Block {
    Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(BORDER_COLOR))
        .title(Span::styled(
            format!(" {} ", title),
            Style::default().fg(TITLE_COLOR).add_modifier(Modifier::BOLD),
        ))
}

// ─── Render Panels ────────────────────────────────────────────────────────────

fn render_cpu_panel(f: &mut Frame, area: Rect, state: &AppState) {
    let block = btop_box("cpu");
    let inner = block.inner(area);
    f.render_widget(block, area);

    let layout = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(25), Constraint::Percentage(75)])
        .split(inner);

    // Sidebar: Logo and Nexus info
    let logo = " NEXUS\n STORAGE\n\n 󰒋 Local\n 󰓅 YouTube\n 󰌋 XChaCha20";
    let is_on = state.daemon_online;
    
    let info_str = format!(
        "{}\n\n Daemon: {}\n Uptime: {} \n Ping: {}ms",
        logo,
        if is_on { "ON" } else { "OFF" },
        state.last_refresh.elapsed().as_secs(),
        if is_on { "12" } else { "---" }
    );
    let p = Paragraph::new(info_str).style(Style::default().fg(if is_on { ICON_DAEMON_ON } else { ICON_DAEMON_OFF }));
    f.render_widget(p, layout[0]);

    // Chart: CPU Usage
    let v_max = 100.0;
    let min_t = state.tick_count - 100.0;
    
    let datasets = vec![
        Dataset::default()
            .name("cpu load")
            .marker(Marker::Braille)
            .graph_type(GraphType::Line)
            .style(Style::default().fg(CHART_CPU))
            .data(&state.cpu_history),
    ];

    let current_cpu = *state.cpu_history.last().unwrap_or(&(0.0, 0.0));
    let chart = Chart::new(datasets)
        .x_axis(ratatui::widgets::Axis::default().bounds([min_t, state.tick_count]))
        .y_axis(ratatui::widgets::Axis::default().bounds([0.0, v_max]));
    
    f.render_widget(chart, layout[1]);
}

fn render_network_panel(f: &mut Frame, area: Rect, state: &AppState) {
    let block = btop_box("net");
    let inner = block.inner(area);
    f.render_widget(block, area);

    let layout = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(inner);

    let min_t = state.tick_count - 100.0;

    let rx_datasets = vec![
        Dataset::default()
            .name(format!("RX: {:.1} kbps", state.rx_history.last().unwrap_or(&(0.0, 0.0)).1))
            .marker(Marker::Braille)
            .graph_type(GraphType::Line)
            .style(Style::default().fg(CHART_RX))
            .data(&state.rx_history),
    ];
    let rx_chart = Chart::new(rx_datasets)
        .x_axis(ratatui::widgets::Axis::default().bounds([min_t, state.tick_count]))
        .y_axis(ratatui::widgets::Axis::default().bounds([0.0, state.max_rx]));
    f.render_widget(rx_chart, layout[0]);

    let tx_datasets = vec![
        Dataset::default()
            .name(format!("TX: {:.1} kbps", state.tx_history.last().unwrap_or(&(0.0, 0.0)).1))
            .marker(Marker::Braille)
            .graph_type(GraphType::Line)
            .style(Style::default().fg(CHART_TX))
            .data(&state.tx_history),
    ];
    let tx_chart = Chart::new(tx_datasets)
        .x_axis(ratatui::widgets::Axis::default().bounds([min_t, state.tick_count]))
        .y_axis(ratatui::widgets::Axis::default().bounds([0.0, state.max_tx]));
    f.render_widget(tx_chart, layout[1]);
}

fn render_vault_panel(f: &mut Frame, area: Rect, state: &AppState, table_state: &mut TableState) {
    let block = btop_box("vault (processes)");
    
    let header = Row::new(vec![
        "File Name",
        "Hash.Id",
        "Video Shard",
        "Size",
    ])
    .style(Style::default().fg(TITLE_COLOR).add_modifier(Modifier::BOLD))
    .bottom_margin(1);

    let mut rows = vec![];
    if state.files.is_empty() {
        rows.push(Row::new(vec!["No files found.", "", "", ""]));
    } else {
        for file in &state.files {
            let name = file.path.split('/').last().unwrap_or(&file.path);
            let size = format_size(file.size);
            let hash = if file.hash.len() > 8 { &file.hash[..8] } else { &file.hash };
            let video = if file.video_id.len() > 11 { &file.video_id[..11] } else { &file.video_id };
            
            let is_active = state.tasks.iter().any(|t| t.file_path == file.path);
            let color = if is_active { CHART_TX } else { Color::Rgb(200, 200, 200) };

            rows.push(
                Row::new(vec![name.to_string(), hash.to_string(), video.to_string(), size])
                    .style(Style::default().fg(color)),
            );
        }
    }

    let widths = [
        Constraint::Percentage(40),
        Constraint::Percentage(20),
        Constraint::Percentage(25),
        Constraint::Percentage(15),
    ];

    let table = Table::new(rows, widths)
        .header(header)
        .block(block)
        .row_highlight_style(Style::default().bg(HL_BG).add_modifier(Modifier::BOLD))
        .highlight_symbol(">> ");

    f.render_stateful_widget(table, area, table_state);
}

fn render_tasks_panel(f: &mut Frame, area: Rect, state: &AppState) {
    let block = btop_box("tasks & io");
    let inner = block.inner(area);
    f.render_widget(block, area);

    let active_tasks: Vec<&BackendTask> = state.tasks.iter().filter(|t| t.status != "Completed").collect();

    let layout = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Percentage(30), Constraint::Percentage(70)])
        .split(inner);

    // Storage capacity faux-gauge 
    let cap = state.stats.total_size as f64 / (15.0 * 1024.0 * 1024.0 * 1024.0); // 15GB fake
    let mut safe_cap = cap;
    if safe_cap.is_nan() || safe_cap < 0.0 { safe_cap = 0.0; }
    if safe_cap > 1.0 { safe_cap = 1.0; }

    let disk_gauge = Gauge::default()
        .block(Block::default().padding(Padding::vertical(1)))
        .gauge_style(Style::default().fg(CHART_RX))
        .use_unicode(true)
        .label(format!("Drive Usage: {:.2}% of 15GB", safe_cap * 100.0))
        .ratio(safe_cap);
    
    f.render_widget(disk_gauge, layout[0]);

    if active_tasks.is_empty() {
        f.render_widget(Paragraph::new("No active background transfers.").style(Style::default().fg(BORDER_COLOR)), layout[1]);
    } else {
        let task_items: Vec<ListItem> = active_tasks
            .iter()
            .map(|t| {
                let fname = t.file_path.split('/').last().unwrap_or(&t.file_path);
                let mut ratio = t.progress / 100.0;
                if ratio.is_nan() || ratio < 0.0 { ratio = 0.0; }
                if ratio > 1.0 { ratio = 1.0; }
                
                let bars = (ratio * 20.0) as usize;
                let braille_str: String = (0..20).map(|i| if i < bars { '⣿' } else { '⣀' }).collect();
                
                ListItem::new(Line::from(vec![
                    Span::styled(format!("{} ", fname), Style::default().fg(TITLE_COLOR)),
                    Span::styled(braille_str, Style::default().fg(CHART_TX)),
                    Span::styled(format!(" {:.1}%", t.progress), Style::default().fg(CHART_RX)),
                ]))
            })
            .collect();
            
        f.render_widget(List::new(task_items), layout[1]);
    }
}

// ─── Help ────────────────────────────────────────────────────────────────────

fn render_help_popup(f: &mut Frame, area: Rect) {
    let popup_width = 46u16;
    let popup_height = 8u16;
    let x = area.width.saturating_sub(popup_width) / 2;
    let y = area.height.saturating_sub(popup_height) / 2;
    let popup_area = Rect::new(x, y, popup_width, popup_height);

    f.render_widget(Clear, popup_area);

    let lines = vec![
        Line::from(Span::styled("  j / ↓    Next file", Style::default().fg(TITLE_COLOR))),
        Line::from(Span::styled("  k / ↑    Previous file", Style::default().fg(TITLE_COLOR))),
        Line::from(Span::styled("  u        Upload File (prompt path)", Style::default().fg(TITLE_COLOR))),
        Line::from(Span::styled("  Enter    Download/Extract Selected", Style::default().fg(TITLE_COLOR))),
        Line::from(Span::styled("  Esc / q  Quit", Style::default().fg(TITLE_COLOR))),
    ];

    let help = Paragraph::new(lines)
        .block(btop_box("help"))
        .wrap(Wrap { trim: false });

    f.render_widget(help, popup_area);
}

fn render_input_popup(f: &mut Frame, area: Rect, input: &str) {
    let popup_width = 60u16;
    let popup_height = 3u16;
    let x = area.width.saturating_sub(popup_width) / 2;
    let y = area.height.saturating_sub(popup_height) / 2;
    let popup_area = Rect::new(x, y, popup_width, popup_height);

    f.render_widget(Clear, popup_area);

    let input_field = Paragraph::new(format!(" > {}", input))
        .block(btop_box("upload path (full path)"));

    f.render_widget(input_field, popup_area);
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

fn format_size(bytes: i64) -> String {
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

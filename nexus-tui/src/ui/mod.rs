use ratatui::{
    layout::{Constraint, Direction, Layout},
    Frame,
};
use crate::app::{AppMode, AppState};

pub mod header;
pub mod file_table;
pub mod quota;
pub mod chart;
pub mod tasks;
pub mod statusbar;
pub mod command_bar;
pub mod helpers;

pub fn render(f: &mut Frame, app: &mut AppState) {
    let area = f.area();

    if app.mode == AppMode::Loading {
        draw_loading_screen(f, area, app);
        return;
    }

    // The Layout Global
    let cmd_len = if app.mode == AppMode::CommandInput { 2 } else { 0 };
    
    // Header (3), Body (flex), StatusBar (1), CmdBar (cmd_len)
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(3),
            Constraint::Min(10),
            Constraint::Length(1),
            Constraint::Length(cmd_len),
        ])
        .split(area);

    let header_chunk = chunks[0];
    let body_chunk = chunks[1];
    let status_chunk = chunks[2];
    let cmd_chunk = chunks[3];

    // Body split 60/40 horizontal
    let body_cols = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(60), Constraint::Percentage(40)])
        .split(body_chunk);

    let left_col = body_cols[0]; // File Table
    let right_col = body_cols[1]; // details

    // Right Column Split: Quota (6), Chart (8), Tasks (rest)
    let right_rows = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(6),
            Constraint::Length(8),
            Constraint::Min(4),
        ])
        .split(right_col);

    let quota_chunk = right_rows[0];
    let chart_chunk = right_rows[1];
    let tasks_chunk = right_rows[2];

    header::draw(f, header_chunk, app);
    file_table::draw(f, left_col, app);
    quota::draw(f, quota_chunk, app);
    chart::draw(f, chart_chunk, app);
    tasks::draw(f, tasks_chunk, app);
    statusbar::draw(f, status_chunk, app);

    if app.mode == AppMode::CommandInput {
        command_bar::draw(f, cmd_chunk, app);
    }
}

fn draw_loading_screen(f: &mut Frame, area: ratatui::layout::Rect, _app: &AppState) {
    use ratatui::widgets::{Paragraph, Gauge};
    use ratatui::layout::{Alignment};
    use ratatui::style::{Style, Color, Modifier};

    let center_y = area.height / 2;
    let center_x = area.width / 2;
    
    let popup_area = Layout::default()
        .direction(Direction::Vertical)
        .constraints([
            Constraint::Length(center_y.saturating_sub(2)),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Length(1),
            Constraint::Min(0),
        ])
        .split(area);

    let title = Paragraph::new("Nexus Storage")
        .style(Style::default().fg(Color::Blue).add_modifier(Modifier::BOLD))
        .alignment(Alignment::Center);
    f.render_widget(title, popup_area[1]);

    let loading_msg = Paragraph::new("SECURE INITIALIZATION...")
        .style(Style::default().fg(Color::DarkGray))
        .alignment(Alignment::Center);
    f.render_widget(loading_msg, popup_area[2]);

    let gauge = Gauge::default()
        .gauge_style(Style::default().fg(Color::Blue))
        .percent(65);
    
    let gauge_area = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([
            Constraint::Length(center_x.saturating_sub(15)),
            Constraint::Length(30),
            Constraint::Min(0),
        ])
        .split(popup_area[3]);

    f.render_widget(gauge, gauge_area[1]);
}

use ratatui::{
    layout::{Constraint, Rect},
    style::{Modifier, Style},
    widgets::{Block, Borders, BorderType, Row, Table, TableState},
    Frame,
};
use crate::app::AppState;
use crate::theme::*;

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

pub fn draw(f: &mut Frame, area: Rect, app: &mut AppState) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(MUTED))
        .title(ratatui::text::Span::styled(" FILES ", Style::default().fg(CYAN).add_modifier(Modifier::BOLD)));

    let header = Row::new(vec!["Nom", "Taille", "Date", "ID Shard"])
        .style(Style::default().fg(CYAN).add_modifier(Modifier::BOLD))
        .bottom_margin(1);

    let mut rows = vec![];
    
    // Sort logic placeholder (use default for now)
    
    if app.filtered_files.is_empty() {
        rows.push(Row::new(vec!["Aucun fichier", "", "", ""]).style(Style::default().fg(MUTED)));
    } else {
        for file in &app.filtered_files {
            let name = file.path.split('/').last().unwrap_or(&file.path);
            let size = format_size(file.size);
            let date = if file.last_update.len() > 10 { &file.last_update[..10] } else { &file.last_update };
            let shard = if file.video_id.len() > 11 { &file.video_id[..11] } else { &file.video_id };

            let mut style = Style::default().fg(ratatui::style::Color::White);
            let mut prefix = "";

            // Check if it's currently uploading/processing
            if app.tasks.iter().any(|t| t.file_path == file.path) {
                style = style.fg(CYAN);
                prefix = "▶ ";
            }

            rows.push(Row::new(vec![
                format!("{}{}", prefix, name),
                size,
                date.to_string(),
                shard.to_string(),
            ]).style(style));
        }
    }

    let widths = [
        Constraint::Percentage(45),
        Constraint::Percentage(15),
        Constraint::Percentage(20),
        Constraint::Percentage(20),
    ];

    let mut t_state = TableState::default();
    t_state.select(Some(app.selected_idx));

    let table = Table::new(rows, widths)
        .header(header)
        .block(block)
        .row_highlight_style(Style::default().bg(INDIGO_DARK).fg(ratatui::style::Color::White).add_modifier(Modifier::BOLD));

    f.render_stateful_widget(table, area, &mut t_state);
}

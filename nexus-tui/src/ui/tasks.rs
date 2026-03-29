use ratatui::{
    layout::Rect,
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, BorderType, List, ListItem},
    Frame,
};
use crate::app::AppState;
use crate::theme::*;

pub fn draw(f: &mut Frame, area: Rect, app: &AppState) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(MUTED))
        .title(ratatui::text::Span::styled(" TASK QUEUE ", Style::default().fg(INDIGO).add_modifier(Modifier::BOLD)));

    let active_tasks: Vec<_> = app.tasks.iter().filter(|t| t.status != "Completed").collect();

    if active_tasks.is_empty() {
        let p = ratatui::widgets::Paragraph::new("Aucun transfert en cours.").style(Style::default().fg(MUTED)).block(block);
        f.render_widget(p, area);
        return;
    }

    let items: Vec<ListItem> = active_tasks.iter().map(|t| {
        let fname = t.file_path.split('/').last().unwrap_or(&t.file_path);
        let mut ratio = t.progress / 100.0;
        if ratio.is_nan() || ratio < 0.0 { ratio = 0.0; }
        if ratio > 1.0 { ratio = 1.0; }

        let bars = (ratio * 12.0) as usize;
        let braille: String = (0..12).map(|i| if i < bars { '█' } else { '░' }).collect();

        // ⏸ ▶ ✓ ✗
        let icon = match t.status.as_str() {
            "Queued" | "Pending" => Span::styled("[⏸] ", Style::default().fg(AMBER)),
            "Error" => Span::styled("[✗] ", Style::default().fg(ROSE)),
            _ => Span::styled("[▶] ", Style::default().fg(CYAN)),
        };

        Line::from(vec![
            icon,
            Span::styled(format!("{:<20} ", fname), Style::default().fg(ratatui::style::Color::White)),
            Span::styled(braille, Style::default().fg(INDIGO)),
            Span::styled(format!(" {:>5.1}%", t.progress), Style::default().fg(CYAN)),
        ])
    }).map(ListItem::new).collect();

    let list = List::new(items).block(block);
    f.render_widget(list, area);
}

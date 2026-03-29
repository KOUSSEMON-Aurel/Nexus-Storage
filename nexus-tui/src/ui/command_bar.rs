use ratatui::{
    layout::Rect,
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph},
    Frame,
};
use crate::app::AppState;
use crate::theme::*;

pub fn draw(f: &mut Frame, area: Rect, app: &AppState) {
    let input = app.command_input.clone();

    // Adding a fake blinking cursor effect would require a tick tracker,
    // For now we just append a block depending on length
    let cursor = Span::styled("█", Style::default().fg(CYAN).add_modifier(Modifier::RAPID_BLINK));

    let p = Paragraph::new(Line::from(vec![
        Span::styled(" CMD ▶ ", Style::default().fg(INDIGO).add_modifier(Modifier::BOLD)),
        Span::raw(input),
        cursor,
    ]))
    .block(Block::default().borders(Borders::NONE));

    f.render_widget(p, area);
}

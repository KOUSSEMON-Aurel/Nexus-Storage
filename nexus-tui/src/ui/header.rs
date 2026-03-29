use ratatui::{
    layout::Rect,
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph},
    Frame,
};
use crate::app::{AppState, NotifLevel};
use crate::theme::*;

pub fn draw(f: &mut Frame, area: Rect, app: &AppState) {
    let auth_str = if app.auth.authenticated {
        Span::styled("[✓ Connecté]", Style::default().fg(EMERALD))
    } else {
        Span::styled("[✗ Déconnecté]", Style::default().fg(ROSE))
    };

    let logo = Span::styled(" NEXUS STORAGE  v0.3.0 ", Style::default().fg(CYAN).add_modifier(Modifier::BOLD));
    let channel = Span::styled(format!(" Canal : @{}", app.auth.channel_title), Style::default().fg(MUTED));

    let top_line = Line::from(vec![logo]);
    let mid_line = Line::from(vec![channel]);
    
    // Handle transient notifications or show auth status
    let bot_line = if let Some(notif) = &app.notification {
        let n_color = match notif.level {
            NotifLevel::Info => CYAN,
            NotifLevel::Success => EMERALD,
            NotifLevel::Warning => AMBER,
            NotifLevel::Error => ROSE,
        };
        Line::from(vec![Span::styled(format!(" 🔔 {}", notif.message), Style::default().fg(n_color))])
    } else {
        Line::from(vec![Span::raw(" "), auth_str])
    };

    let p = Paragraph::new(vec![top_line, mid_line, bot_line])
        .block(Block::default().borders(Borders::BOTTOM).border_style(Style::default().fg(SURFACE)));
    
    f.render_widget(p, area);
}

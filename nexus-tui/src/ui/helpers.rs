use ratatui::{
    layout::Rect,
    style::{Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, BorderType, Clear, Paragraph},
    Frame,
};
use crate::theme::*;

pub fn draw_help_popup(f: &mut Frame, area: Rect) {
    let popup_width = 50;
    let popup_height = 10;
    let x = area.width.saturating_sub(popup_width) / 2;
    let y = area.height.saturating_sub(popup_height) / 2;
    let popup_area = Rect::new(x, y, popup_width, popup_height);

    f.render_widget(Clear, popup_area);

    let block = Block::default()
        .title(" HELP (Vim-Bindings) ")
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(CYAN));

    let lines = vec![
        Line::from(vec![Span::raw(" [j/k] ↓/↑ "), Span::styled("Naviguer fichiers", Style::default().fg(MUTED))]),
        Line::from(vec![Span::raw(" [Enter]   "), Span::styled("Télécharger/Ouvrir", Style::default().fg(MUTED))]),
        Line::from(vec![Span::raw(" [:]       "), Span::styled("Barre de commande Agent", Style::default().fg(MUTED))]),
        Line::from(vec![Span::raw(" [/]       "), Span::styled("Recherche rapide (Filtre)", Style::default().fg(MUTED))]),
        Line::from(vec![Span::raw(" [q/Esc]   "), Span::styled("Quitter", Style::default().fg(MUTED))]),
        Line::from(""),
        Line::from(vec![Span::styled(" Commandes Agent:", Style::default().fg(INDIGO).add_modifier(Modifier::BOLD))]),
        Line::from(vec![Span::raw(" /upload ./file, /search <q>, /mount, /studio")]),
    ];

    let p = Paragraph::new(lines).block(block);
    f.render_widget(p, popup_area);
}

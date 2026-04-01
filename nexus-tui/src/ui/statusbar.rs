use ratatui::{
    layout::Rect,
    style::Style,
    text::Span,
    widgets::{Block, Borders, Paragraph},
    Frame,
};
use crate::app::{AppMode, AppState};
use crate::theme::*;

pub fn draw(f: &mut Frame, area: Rect, app: &AppState) {
    let mode_str = match app.mode {
        AppMode::Normal => "  [j/k] nav  [Enter] tléc.  [/] search  [d] suppr  [r] refresh  [?] aide  [q] quit  ",
        AppMode::CommandInput => "  [Enter] exec  [Tab] complétion  [↑↓] historique  [Esc] annuler  ",
        AppMode::SearchFilter => "  [Esc] annuler  [↑↓] nav  ",
        AppMode::Confirm(_) => "  [y] confirmer  [n] annuler  ",
        AppMode::Help => "  [Esc] fermer  ",
        AppMode::Loading => "  Initialisation sécurisée...  ",
        AppMode::Authentication => "  [↑↓] nav  [Enter] login/recover  [Esc] quit  ",
        AppMode::RecoveryMode => "  [↑↓] nav  [Enter] restore  [Esc] quit  ",
    };

    let p = Paragraph::new(Span::styled(mode_str, Style::default().fg(SURFACE).bg(MUTED)))
        .block(Block::default().borders(Borders::NONE));

    f.render_widget(p, area);
}

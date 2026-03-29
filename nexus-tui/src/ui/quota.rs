use ratatui::{
    layout::Rect,
    style::{Modifier, Style},
    widgets::{Block, Borders, BorderType, Gauge},
    Frame,
};
use crate::app::AppState;
use crate::theme::*;

pub fn draw(f: &mut Frame, area: Rect, app: &AppState) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(MUTED))
        .title(ratatui::text::Span::styled(" QUOTA YOUTUBE ", Style::default().fg(AMBER).add_modifier(Modifier::BOLD)));

    let used = app.quota.used;
    let total = app.quota.total;
    let mut ratio = used as f64 / total as f64;
    if ratio.is_nan() || ratio < 0.0 { ratio = 0.0; }
    if ratio > 1.0 { ratio = 1.0; }

    let color = if ratio < 0.70 {
        EMERALD
    } else if ratio < 0.90 {
        AMBER
    } else {
        ROSE
    };

    let label = format!("{} / {} unités", used, total);

    let gauge = Gauge::default()
        .block(block)
        .gauge_style(Style::default().fg(color).bg(SURFACE))
        .ratio(ratio)
        .label(label)
        .use_unicode(true);

    f.render_widget(gauge, area);
}

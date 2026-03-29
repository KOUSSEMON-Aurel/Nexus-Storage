use ratatui::{
    layout::Rect,
    style::{Modifier, Style},
    symbols::Marker,
    widgets::{Axis, Block, Borders, BorderType, Chart, Dataset, GraphType},
    Frame,
};
use crate::app::AppState;
use crate::theme::*;

pub fn draw(f: &mut Frame, area: Rect, app: &AppState) {
    let block = Block::default()
        .borders(Borders::ALL)
        .border_type(BorderType::Rounded)
        .border_style(Style::default().fg(MUTED))
        .title(ratatui::text::Span::styled(
            format!(" UPLOAD ({:.1} KB/s) ", app.current_kbps),
            Style::default().fg(CYAN).add_modifier(Modifier::BOLD)
        ));

    let data: Vec<(f64, f64)> = app.upload_history.iter().enumerate().map(|(i, &v)| (i as f64, v)).collect();

    let max_y = data.iter().map(|(_, y)| y).fold(10.0f64, |a, &b| a.max(b)) * 1.2;

    let datasets = vec![
        Dataset::default()
            .marker(Marker::Braille)
            .graph_type(GraphType::Line)
            .style(Style::default().fg(CYAN))
            .data(&data),
    ];

    let chart = Chart::new(datasets)
        .block(block)
        .x_axis(Axis::default().bounds([0.0, 60.0]))
        .y_axis(Axis::default().bounds([0.0, max_y]));

    f.render_widget(chart, area);
}

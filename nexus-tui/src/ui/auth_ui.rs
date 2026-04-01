// nexus-tui/src/ui/auth_ui.rs
// TUI Authentication screen for V4 password-based login

use ratatui::{
    backend::Backend,
    layout::{Alignment, Constraint, Direction, Layout, Rect},
    style::{Color, Modifier, Style},
    text::{Line, Span},
    widgets::{Block, Borders, Paragraph, Wrap},
    Frame,
};

pub struct AuthScreen {
    pub password: String,
    pub focused_field: AuthField,
    pub recovering: bool,
}

#[derive(Debug, Clone, Copy, PartialEq)]
pub enum AuthField {
    PasswordInput,
    RecoverButton,
    LoginButton,
}

impl AuthScreen {
    pub fn new() -> Self {
        Self {
            password: String::new(),
            focused_field: AuthField::PasswordInput,
            recovering: false,
        }
    }

    pub fn handle_char(&mut self, c: char) {
        if self.focused_field == AuthField::PasswordInput {
            self.password.push(c);
        }
    }

    pub fn handle_backspace(&mut self) {
        if self.focused_field == AuthField::PasswordInput {
            self.password.pop();
        }
    }

    pub fn next_field(&mut self) {
        self.focused_field = match self.focused_field {
            AuthField::PasswordInput => AuthField::RecoverButton,
            AuthField::RecoverButton => AuthField::LoginButton,
            AuthField::LoginButton => AuthField::PasswordInput,
        };
    }

    pub fn prev_field(&mut self) {
        self.focused_field = match self.focused_field {
            AuthField::PasswordInput => AuthField::LoginButton,
            AuthField::LoginButton => AuthField::RecoverButton,
            AuthField::RecoverButton => AuthField::PasswordInput,
        };
    }
}

pub fn draw_auth_screen(f: &mut Frame, auth: &AuthScreen, _daemon_url: &str) {
    let size = f.size();

    let vertical = Layout::default()
        .direction(Direction::Vertical)
        .constraints(
            [
                Constraint::Length(3),
                Constraint::Min(10),
                Constraint::Length(8),
            ]
            .as_ref(),
        )
        .split(size);

    // Header
    let header = Paragraph::new("🔐 Nexus Storage - V4 Authentication")
        .style(Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD))
        .alignment(Alignment::Center);
    f.render_widget(header, vertical[0]);

    // Main content
    let main_area = vertical[1];
    let horizontal = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(10), Constraint::Percentage(80), Constraint::Percentage(10)])
        .split(main_area);

    let content_area = horizontal[1];
    let vertical_content = Layout::default()
        .direction(Direction::Vertical)
        .constraints(
            [
                Constraint::Length(3),
                Constraint::Length(4),
                Constraint::Length(2),
                Constraint::Min(5),
            ]
            .as_ref(),
        )
        .split(content_area);

    // Info block
    let info = Paragraph::new(
        "Enter your password to decrypt your local database.\n\
         Password must match the one used during initial setup.\n\
         Your password is never transmitted to the server."
    )
    .block(Block::default().borders(Borders::ALL).title("ℹ️ Instructions"))
    .wrap(Wrap { trim: true });
    f.render_widget(info, vertical_content[0]);

    // Password input
    let password_style = if auth.focused_field == AuthField::PasswordInput {
        Style::default().bg(Color::Blue).fg(Color::White)
    } else {
        Style::default()
    };

    let password_block = Block::default()
        .title("Password")
        .borders(Borders::ALL)
        .style(password_style);

    let masked_password = "•".repeat(auth.password.len());
    let password_input = Paragraph::new(masked_password).block(password_block);
    f.render_widget(password_input, vertical_content[1]);

    // Buttons
    let button_layout = Layout::default()
        .direction(Direction::Horizontal)
        .constraints([Constraint::Percentage(50), Constraint::Percentage(50)])
        .split(vertical_content[2]);

    let recover_style = if auth.focused_field == AuthField::RecoverButton {
        Style::default().bg(Color::Yellow).fg(Color::Black)
    } else {
        Style::default().fg(Color::Yellow)
    };

    let login_style = if auth.focused_field == AuthField::LoginButton {
        Style::default().bg(Color::Green).fg(Color::Black)
    } else {
        Style::default().fg(Color::Green)
    };

    let recover_btn = Paragraph::new("[ Recover ]")
        .style(recover_style)
        .alignment(Alignment::Center);
    let login_btn = Paragraph::new("[ Login ]")
        .style(login_style)
        .alignment(Alignment::Center);

    f.render_widget(recover_btn, button_layout[0]);
    f.render_widget(login_btn, button_layout[1]);

    // Help text
    let help_area = vertical[2];
    let help_lines = vec![
        Line::from(vec![
            Span::styled("↑↓", Style::default().fg(Color::Cyan)),
            Span::raw(" Navigate  "),
            Span::styled("Enter", Style::default().fg(Color::Cyan)),
            Span::raw(" Select  "),
            Span::styled("Ctrl+U", Style::default().fg(Color::Cyan)),
            Span::raw(" Clear  "),
            Span::styled("Esc", Style::default().fg(Color::Cyan)),
            Span::raw(" Quit"),
        ]),
        Line::from(""),
        if auth.recovering {
            Line::from(vec![
                Span::styled("⏳ ", Style::default().fg(Color::Yellow)),
                Span::raw("Attempting recovery from Google Drive..."),
            ])
        } else {
            Line::from("")
        },
    ];
    let help = Paragraph::new(help_lines)
        .block(Block::default().borders(Borders::TOP))
        .alignment(Alignment::Center);
    f.render_widget(help, help_area);
}

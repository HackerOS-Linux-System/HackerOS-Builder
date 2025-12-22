use std::io::{self, Write};
use std::process::{Command, Stdio};
use std::path::Path;
use std::fs::{self, File};
use std::env;

use anyhow::{Context, Result};
use crossterm::event::{self, Event, KeyCode, KeyEventKind};
use crossterm::terminal::{disable_raw_mode, enable_raw_mode, EnterAlternateScreen, LeaveAlternateScreen};
use crossterm::{execute, ExecutableCommand};
use git2::Repository;
use indicatif::{ProgressBar, ProgressStyle};
use ratatui::backend::CrosstermBackend;
use ratatui::layout::{Alignment, Constraint, Direction, Layout, Rect};
use ratatui::style::{Color, Modifier, Style};
use ratatui::text::{Line, Span, Text};
use ratatui::widgets::{Block, Borders, List, ListItem, ListState, Paragraph, Wrap};
use ratatui::Terminal;
use reqwest::Client;
use tokio::task;

#[derive(Debug, Clone, PartialEq)]
enum Edition {
    Official,
    Gnome,
    Xfce,
    Blue,
    Hydra,
    Cybersecurity,
    Wayfire,
    Atomic,
}

#[derive(Debug, Clone, PartialEq)]
enum DebianBranch {
    Stable,    // trixie
    Testing,   // forky
    Unstable,  // sid
}

#[derive(Debug, Clone, PartialEq)]
enum Filesystem {
    Btrfs,
    Ext4,
    Zfs,
}

#[derive(Debug, Clone)]
struct InstallerState {
    current_step: usize,
    username: String,
    password: String,
    hostname: String,
    edition: Option<Edition>,
    branch: Option<DebianBranch>,
    filesystem: Option<Filesystem>,
    manual_partition: bool,
    disk: String,
    preview_image: bool,
    quit: bool,
}

impl Default for InstallerState {
    fn default() -> Self {
        InstallerState {
            current_step: 0,
            username: String::new(),
            password: String::new(),
            hostname: "hackeros".to_string(),
            edition: None,
            branch: None,
            filesystem: None,
            manual_partition: false,
            disk: String::new(),
            preview_image: false,
            quit: false,
        }
    }
}

#[tokio::main]
async fn main() -> Result<()> {
    let mut state = InstallerState::default();
    setup_terminal()?;
    let res = run_app(&mut state).await;
    teardown_terminal()?;
    res
}

fn setup_terminal() -> Result<()> {
    enable_raw_mode()?;
    execute!(io::stdout(), EnterAlternateScreen)?;
    Ok(())
}

fn teardown_terminal() -> Result<()> {
    disable_raw_mode()?;
    execute!(io::stdout(), LeaveAlternateScreen)?;
    Ok(())
}

async fn run_app(state: &mut InstallerState) -> Result<()> {
    let stdout = io::stdout();
    let backend = CrosstermBackend::new(stdout);
    let mut terminal = Terminal::new(backend)?;

    let mut list_state = ListState::default();

    loop {
        terminal.draw(|f| draw_ui(f, state, &mut list_state))?;

        if let Event::Key(key) = event::read()? {
            if key.kind == KeyEventKind::Press {
                match key.code {
                    KeyCode::Char('q') => {
                        state.quit = true;
                    }
                    KeyCode::Enter => handle_enter(state, &mut list_state).await?,
                    KeyCode::Up => {
                        if let Some(selected) = list_state.selected() {
                            if selected > 0 {
                                list_state.select(Some(selected - 1));
                            }
                        }
                    }
                    KeyCode::Down => {
                        if let Some(selected) = list_state.selected() {
                            list_state.select(Some(selected + 1));
                        }
                    }
                    KeyCode::Char(c) => handle_char_input(state, c),
                    _ => {}
                }
            }
        }

        if state.quit {
            break;
        }

        if state.current_step >= 10 { // Assume 10 steps for completion
            perform_installation(state).await?;
            break;
        }
    }

    Ok(())
}

fn draw_ui(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, state: &InstallerState, list_state: &mut ListState) {
    let chunks = Layout::default()
        .direction(Direction::Vertical)
        .constraints([Constraint::Length(3), Constraint::Min(0)])
        .split(f.area());

    let header = Paragraph::new("HackerOS Installer - Inspired by Arch")
        .style(Style::default().fg(Color::Cyan).add_modifier(Modifier::BOLD))
        .alignment(Alignment::Center);
    f.render_widget(header, chunks[0]);

    let body_chunk = chunks[1];

    match state.current_step {
        0 => draw_welcome(f, body_chunk),
        1 => draw_username_input(f, body_chunk, &state.username),
        2 => draw_password_input(f, body_chunk, &state.password),
        3 => draw_hostname_input(f, body_chunk, &state.hostname),
        4 => draw_edition_selection(f, body_chunk, list_state, state.edition.as_ref()),
        5 => draw_branch_selection(f, body_chunk, list_state, state.branch.as_ref()),
        6 => draw_filesystem_selection(f, body_chunk, list_state, state.filesystem.as_ref()),
        7 => draw_partition_mode(f, body_chunk, list_state, state.manual_partition),
        8 => draw_disk_selection(f, body_chunk, &state.disk),
        9 => draw_summary(f, body_chunk, state),
        _ => {}
    }

    if state.preview_image {
        draw_image_preview(f, body_chunk, state.edition.as_ref());
        state.preview_image = false;
    }
}

fn draw_welcome(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect) {
    let text = Text::from("Welcome to HackerOS Installer!\nPress Enter to start.");
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("Welcome").borders(Borders::ALL))
        .style(Style::default().fg(Color::Green))
        .alignment(Alignment::Center)
        .wrap(Wrap::default());
    f.render_widget(paragraph, area);
}

fn draw_username_input(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, input: &str) {
    let text = format!("Enter username: {}", input);
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("User Creation").borders(Borders::ALL))
        .style(Style::default().fg(Color::Yellow));
    f.render_widget(paragraph, area);
}

fn draw_password_input(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, input: &str) {
    let text = format!("Enter password: {}", "*".repeat(input.len()));
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("Password").borders(Borders::ALL))
        .style(Style::default().fg(Color::Yellow));
    f.render_widget(paragraph, area);
}

fn draw_hostname_input(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, input: &str) {
    let text = format!("Enter hostname (default: hackeros): {}", input);
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("Hostname").borders(Borders::ALL))
        .style(Style::default().fg(Color::Yellow));
    f.render_widget(paragraph, area);
}

fn draw_edition_selection(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, list_state: &mut ListState, selected: Option<&Edition>) {
    let items = vec![
        ListItem::new("Official (KDE Plasma + SDDM)"),
        ListItem::new("Gnome (GNOME + GDM3)"),
        ListItem::new("XFCE (XFCE + LightDM)"),
        ListItem::new("Blue (Custom Environment)"),
        ListItem::new("Hydra (Custom Look)"),
        ListItem::new("Cybersecurity (With Tools)"),
        ListItem::new("Wayfire (Wayfire + SDDM)"),
        ListItem::new("Atomic (With Hammer)"),
    ];
    if list_state.selected().is_none() {
        list_state.select(Some(0));
    }
    let list = List::new(items)
        .block(Block::default().title("Select Edition").borders(Borders::ALL))
        .style(Style::default().fg(Color::White))
        .highlight_style(Style::default().add_modifier(Modifier::ITALIC).fg(Color::Green))
        .highlight_symbol(">>");
    f.render_stateful_widget(list, area, list_state);
}

fn draw_branch_selection(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, list_state: &mut ListState, selected: Option<&DebianBranch>) {
    let items = vec![
        ListItem::new("Stable (trixie)"),
        ListItem::new("Testing (forky)"),
        ListItem::new("Unstable (sid)"),
    ];
    if list_state.selected().is_none() {
        list_state.select(Some(0));
    }
    let list = List::new(items)
        .block(Block::default().title("Select Debian Branch").borders(Borders::ALL))
        .style(Style::default().fg(Color::White))
        .highlight_style(Style::default().add_modifier(Modifier::ITALIC).fg(Color::Green))
        .highlight_symbol(">>");
    f.render_stateful_widget(list, area, list_state);
}

fn draw_filesystem_selection(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, list_state: &mut ListState, selected: Option<&Filesystem>) {
    let items = vec![
        ListItem::new("Btrfs"),
        ListItem::new("Ext4"),
        ListItem::new("Zfs"),
    ];
    if list_state.selected().is_none() {
        list_state.select(Some(0));
    }
    let list = List::new(items)
        .block(Block::default().title("Select Filesystem").borders(Borders::ALL))
        .style(Style::default().fg(Color::White))
        .highlight_style(Style::default().add_modifier(Modifier::ITALIC).fg(Color::Green))
        .highlight_symbol(">>");
    f.render_stateful_widget(list, area, list_state);
}

fn draw_partition_mode(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, list_state: &mut ListState, manual: bool) {
    let items = vec![
        ListItem::new("Automatic Partitioning"),
        ListItem::new("Manual Partitioning"),
    ];
    if list_state.selected().is_none() {
        list_state.select(Some(0));
    }
    let list = List::new(items)
        .block(Block::default().title("Partitioning Mode").borders(Borders::ALL))
        .style(Style::default().fg(Color::White))
        .highlight_style(Style::default().add_modifier(Modifier::ITALIC).fg(Color::Green))
        .highlight_symbol(">>");
    f.render_stateful_widget(list, area, list_state);
}

fn draw_disk_selection(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, input: &str) {
    let text = format!("Enter disk (e.g., /dev/sda): {}", input);
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("Disk Selection").borders(Borders::ALL))
        .style(Style::default().fg(Color::Yellow));
    f.render_widget(paragraph, area);
}

fn draw_summary(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, state: &InstallerState) {
    let mut lines = vec![
        Line::from(format!("Username: {}", state.username)),
        Line::from(format!("Hostname: {}", state.hostname)),
        Line::from(format!("Edition: {:?}", state.edition)),
        Line::from(format!("Branch: {:?}", state.branch)),
        Line::from(format!("Filesystem: {:?}", state.filesystem)),
        Line::from(format!("Manual Partition: {}", state.manual_partition)),
        Line::from(format!("Disk: {}", state.disk)),
        Line::from("\nPress Enter to install."),
    ];
    let text = Text::from(lines);
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("Summary").borders(Borders::ALL))
        .style(Style::default().fg(Color::Magenta));
    f.render_widget(paragraph, area);
}

fn draw_image_preview(f: &mut ratatui::Frame<CrosstermBackend<io::Stdout>>, area: Rect, edition: Option<&Edition>) {
    let image_name = match edition {
        Some(Edition::Official) => "plasma.png",
        Some(Edition::Gnome) => "gnome.png",
        Some(Edition::Xfce) => "xfce.png",
        Some(Edition::Blue) => "blue.png",
        Some(Edition::Hydra) => "hydra.png",
        Some(Edition::Cybersecurity) => "cybersecurity.png",
        Some(Edition::Wayfire) => "wayfire.png",
        Some(Edition::Atomic) => "atomic.png", // Assume exists
        None => return,
    };
    let path = format!("/usr/share/HackerOS-Installer/images/{}", image_name);
    // Note: In real TUI, displaying image is complex; assume text placeholder
    let text = format!("Previewing image: {}", path);
    let paragraph = Paragraph::new(text)
        .block(Block::default().title("Edition Preview").borders(Borders::ALL))
        .style(Style::default().fg(Color::Blue));
    f.render_widget(paragraph, area);
}

async fn handle_enter(state: &mut InstallerState, list_state: &mut ListState) -> Result<()> {
    match state.current_step {
        0 => state.current_step += 1,
        1 => if !state.username.is_empty() { state.current_step += 1 },
        2 => if !state.password.is_empty() { state.current_step += 1 },
        3 => {
            if state.hostname.is_empty() {
                state.hostname = "hackeros".to_string();
            }
            state.current_step += 1;
        }
        4 => {
            if let Some(selected) = list_state.selected() {
                state.edition = Some(match selected {
                    0 => Edition::Official,
                    1 => Edition::Gnome,
                    2 => Edition::Xfce,
                    3 => Edition::Blue,
                    4 => Edition::Hydra,
                    5 => Edition::Cybersecurity,
                    6 => Edition::Wayfire,
                    7 => Edition::Atomic,
                    _ => return Ok(()),
                });
                // Preview option - for simplicity, toggle preview
                state.preview_image = true;
                state.current_step += 1;
            }
        }
        5 => {
            if let Some(selected) = list_state.selected() {
                state.branch = Some(match selected {
                    0 => DebianBranch::Stable,
                    1 => DebianBranch::Testing,
                    2 => DebianBranch::Unstable,
                    _ => return Ok(()),
                });
                state.current_step += 1;
            }
        }
        6 => {
            if let Some(selected) = list_state.selected() {
                state.filesystem = Some(match selected {
                    0 => Filesystem::Btrfs,
                    1 => Filesystem::Ext4,
                    2 => Filesystem::Zfs,
                    _ => return Ok(()),
                });
                state.current_step += 1;
            }
        }
        7 => {
            if let Some(selected) = list_state.selected() {
                state.manual_partition = selected == 1;
                state.current_step += 1;
            }
        }
        8 => if !state.disk.is_empty() { state.current_step += 1 },
        9 => state.current_step += 1, // Proceed to install
        _ => {}
    }
    list_state.select(None); // Reset selection
    Ok(())
}

fn handle_char_input(state: &mut InstallerState, c: char) {
    match state.current_step {
        1 => state.username.push(c),
        2 => state.password.push(c),
        3 => state.hostname.push(c),
        8 => state.disk.push(c),
        _ => {}
    }
}

async fn perform_installation(state: &InstallerState) -> Result<()> {
    // Update sources.list based on branch
    let branch_str = match state.branch.as_ref().unwrap() {
        DebianBranch::Stable => "trixie",
        DebianBranch::Testing => "forky",
        DebianBranch::Unstable => "sid",
    };
    fs::write("/etc/apt/sources.list", format!("deb http://deb.debian.org/debian {} main", branch_str))?;

    Command::new("apt")
        .args(&["update"])
        .status()?;

    // Partition disk
    if state.manual_partition {
        // Launch cfdisk or something
        Command::new("cfdisk").arg(&state.disk).status()?;
    } else {
        // Automatic partitioning - simple example
        let pb = ProgressBar::new(100);
        pb.set_style(ProgressStyle::default_bar().template("{msg} {bar:40.cyan/blue} {percent}%"));
        pb.set_message("Partitioning disk...");
        // Assume /dev/sda1 for boot, /dev/sda2 for root
        Command::new("sfdisk").arg(&state.disk).stdin(Stdio::piped()).status()?;
        // Write partition table (simplified)
        pb.finish_with_message("Partitioned.");
    }

    // Format filesystem
    let fs_cmd = match state.filesystem.as_ref().unwrap() {
        Filesystem::Btrfs => "mkfs.btrfs",
        Filesystem::Ext4 => "mkfs.ext4",
        Filesystem::Zfs => "zpool create", // Simplified
    };
    Command::new(fs_cmd).arg("/dev/sda2").status()?; // Assume root partition

    // Mount
    fs::create_dir_all("/mnt")?;
    Command::new("mount").arg("/dev/sda2").arg("/mnt").status()?;
    fs::create_dir_all("/mnt/boot")?;
    Command::new("mount").arg("/dev/sda1").arg("/mnt/boot").status()?;

    // Install base system - debootstrap
    Command::new("debootstrap").args(&[branch_str, "/mnt"]).status()?;

    // Chroot and setup
    // Bind mounts
    for dir in &["/dev", "/proc", "/sys", "/run"] {
        fs::create_dir_all(format!("/mnt{}", dir))?;
        Command::new("mount").args(&["--bind", dir, &format!("/mnt{}", dir)]).status()?;
    }

    // Chroot commands
    let chroot_cmd = |cmd: &str| {
        Command::new("chroot")
            .arg("/mnt")
            .arg("/bin/bash")
            .arg("-c")
            .arg(cmd)
            .status()
    };

    chroot_cmd("apt update")?;
    chroot_cmd("apt install -y linux-image-amd64 grub-efi-amd64")?; // Base

    // Create user
    chroot_cmd(&format!("useradd -m -G sudo {}", state.username))?;
    chroot_cmd(&format!("echo '{}:{}' | chpasswd", state.username, state.password))?;
    chroot_cmd(&format!("echo '{} ALL=(ALL) ALL' >> /etc/sudoers", state.username))?;

    // Hostname
    fs::write("/mnt/etc/hostname", &state.hostname)?;

    // Install edition-specific
    install_edition(state.edition.as_ref().unwrap(), state).await?;

    // Grub
    chroot_cmd("grub-install /dev/sda")?;
    chroot_cmd("update-grub")?;

    // Cleanup
    for dir in &["/dev", "/proc", "/sys", "/run"] {
        Command::new("umount").arg(format!("/mnt{}", dir)).status()?;
    }
    Command::new("umount").arg("/mnt/boot").status()?;
    Command::new("umount").arg("/mnt").status()?;

    // Post-install cleanup
    fs::remove_dir_all("/usr/share/HackerOS-Installer")?;
    fs::remove_file("/usr/bin/HackerOS-Installer")?;
    fs::remove_file("/etc/profile.d/HackerOS-Installer.sh")?;

    // Reboot
    Command::new("reboot").status()?;

    Ok(())
}

async fn install_edition(edition: &Edition, state: &InstallerState) -> Result<()> {
    let chroot_cmd = |cmd: &str| {
        Command::new("chroot")
            .arg("/mnt")
            .arg("/bin/bash")
            .arg("-c")
            .arg(cmd)
            .status()
    };

    // Common copy from /usr/share/HackerOS-Installer/official/ to /
    copy_dir("/usr/share/HackerOS-Installer/official/", "/mnt/")?;

    match edition {
        Edition::Official => {
            chroot_cmd("apt install -y kde-plasma-desktop sddm")?;
        }
        Edition::Gnome => {
            chroot_cmd("apt install -y gnome gdm3")?;
        }
        Edition::Xfce => {
            chroot_cmd("apt install -y xfce4 lightdm")?;
        }
        Edition::Blue => {
            // Download binaries
            let client = Client::new();
            let home = format!("/mnt/home/{}/.hackeros/Blue-Environment/", state.username);
            fs::create_dir_all(&home)?;
            let components = vec![
                ("wm", "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/wm"),
                ("shell", "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/shell"),
                ("launcher", "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/launcher"),
                ("Desktop", "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/Desktop"),
                ("decorations", "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/decorations"),
                ("core", "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/core"),
            ];
            for (name, url) in components {
                download_file(&client, url, &format!("{}/{}", home, name)).await?;
            }
            download_file(&client, "https://github.com/HackerOS-Linux-System/Blue-Environment/releases/download/v0.1/Blue-Environment", "/mnt/usr/bin/Blue-Environment").await?;
            download_file(&client, "https://raw.githubusercontent.com/HackerOS-Linux-System/Blue-Environment/main/Blue-Environment.desktop", "/mnt/usr/share/wayland-sessions/Blue-Environment.desktop").await?;
            chroot_cmd("apt install -y sddm")?; // SDDM
        }
        Edition::Hydra => {
            // Git clone
            let repo = Repository::clone("https://github.com/HackerOS-Linux-System/hydra-look-and-feel.git", "/tmp/hydra-look-and-feel")?;
            copy_dir("/tmp/hydra-look-and-feel/files/", "/mnt/")?;
        }
        Edition::Cybersecurity => {
            // Install packages in future - placeholder
            chroot_cmd("apt install -y nmap wireshark")?; // Example
        }
        Edition::Wayfire => {
            chroot_cmd("apt install -y wayfire sddm")?;
        }
        Edition::Atomic => {
            let client = Client::new();
            download_file(&client, "https://github.com/HackerOS-Linux-System/hammer/releases/download/v0.5/hammer", "/mnt/usr/bin/hammer").await?;
            let lib_dir = "/mnt/usr/lib/HackerOS/hammer/";
            fs::create_dir_all(lib_dir)?;
            let hammer_components = vec![
                "https://github.com/HackerOS-Linux-System/hammer/releases/download/v0.5/hammer-updater",
                "https://github.com/HackerOS-Linux-System/hammer/releases/download/v0.5/hammer-tui",
                "https://github.com/HackerOS-Linux-System/hammer/releases/download/v0.5/hammer-core",
                "https://github.com/HackerOS-Linux-System/hammer/releases/download/v0.5/hammer-builder",
            ];
            for url in hammer_components {
                let name = url.split('/').last().unwrap();
                download_file(&client, url, &format!("{}{}", lib_dir, name)).await?;
            }
            chroot_cmd("apt install -y kde-plasma-desktop sddm")?; // Default Plasma
            chroot_cmd("hammer setup")?;
        }
    }

    Ok(())
}

async fn download_file(client: &Client, url: &str, path: &str) -> Result<()> {
    let mut resp = client.get(url).send().await?;
    let mut file = File::create(path)?;
    while let Some(chunk) = resp.chunk().await? {
        file.write_all(&chunk)?;
    }
    // Make executable if binary
    if path.ends_with('/') == false && !path.ends_with(".desktop") {
        Command::new("chmod").args(&["+x", path]).status()?;
    }
    Ok(())
}

fn copy_dir(src: impl AsRef<Path>, dst: impl AsRef<Path>) -> io::Result<()> {
    fs::create_dir_all(&dst)?;
    for entry in fs::read_dir(src)? {
        let entry = entry?;
        let ty = entry.file_type()?;
        if ty.is_dir() {
            copy_dir(entry.path(), dst.as_ref().join(entry.file_name()))?;
        } else {
            fs::copy(entry.path(), dst.as_ref().join(entry.file_name()))?;
        }
    }
    Ok(())
}

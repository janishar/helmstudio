use super::numeric::digits;

const TLDS: [&str; 13] = ["dev", "ai", "io", "bg", "com", "org", "net", "edu", "gov", "info", "me", "app", "eu"];

fn alpha(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_alphabetic())
}

fn domain(s: &str) -> Option<String> {
    let (a, b) = s.split_once('.')?;
    (alpha(a) && TLDS.contains(&b)).then(|| format!("{a} точка {b}"))
}

fn render(s: &str, separate_transitions: bool) -> Option<String> {
    let mut out = String::new();
    let mut run = String::new();
    let mut digit = false;
    for c in s.chars().chain(std::iter::once('\0')) {
        if c.is_ascii_alphanumeric() {
            let next_digit = c.is_ascii_digit();
            if !run.is_empty() && next_digit != digit {
                if digit {
                    out.push_str(&digits(&run)?);
                } else {
                    out.push_str(&run);
                }
                if separate_transitions {
                    out.push(' ');
                }
                run.clear();
            }
            digit = next_digit;
            run.push(c);
            continue;
        }
        if !run.is_empty() {
            if digit {
                out.push_str(&digits(&run)?);
            } else {
                out.push_str(&run);
            }
            run.clear();
        }
        match c {
            '.' => out.push_str(" точка "),
            '-' => out.push_str(" тире "),
            '_' => out.push_str(" долна черта "),
            '/' => out.push_str(" наклонена черта "),
            ':' => out.push_str(" двоеточие "),
            '\0' => {}
            _ => return None,
        }
    }
    Some(out)
}

pub(crate) fn classify(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let parts = s.split('.');
    if parts.clone().count() == 4 && parts.clone().all(|x| !x.is_empty() && x.bytes().all(|c| c.is_ascii_digit())) {
        if let Some(value) = render(s, false) {
            out.push((value, 0.0));
        }
    }
    if let Some(value) = domain(s) {
        out.push((value, 0.98));
    }
    if let Some((user, host)) = s.split_once('@') {
        if let Some(host) = domain(host) {
            let valid = !user.is_empty()
                && user.split(|c| matches!(c, '.' | '-' | '_')).all(|part| {
                    !part.is_empty()
                        && part.bytes().all(|c| c.is_ascii_alphanumeric())
                        && part.as_bytes().windows(2).filter(|p| p[0].is_ascii_digit() != p[1].is_ascii_digit()).count()
                            <= 1
                });
            if valid {
                if let Some(value) = render(user, true) {
                    out.push((format!("{value} кльомба {host}"), 0.96));
                }
            }
        }
    }
    for (prefix, spoken) in [
        ("https://", "х т т п с двоеточие наклонена черта наклонена черта "),
        ("http://", "х т т п двоеточие наклонена черта наклонена черта "),
        ("www.", "в в в точка "),
    ] {
        let Some(rest) = s.strip_prefix(prefix) else { continue };
        let end = rest.find(|c| c == ':' || c == '/').unwrap_or(rest.len());
        let host = &rest[..end];
        let rest = &rest[end..];
        let Some(host_value) = domain(host).or_else(|| alpha(host).then(|| host.to_owned())) else { continue };
        let mut tail = rest;
        if let Some(port) = tail.strip_prefix(':') {
            let n = port.find('/').unwrap_or(port.len());
            if n == 0 || !port[..n].bytes().all(|b| b.is_ascii_digit()) {
                continue;
            }
            tail = &port[n..];
        }
        if !tail.is_empty()
            && (!tail.starts_with('/')
                || tail.len() == 1
                || !tail[1..].bytes().all(|c| c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_' | b'.' | b'/')))
        {
            continue;
        }
        if let Some(rest_value) = render(rest, false) {
            out.push((format!("{spoken}{host_value}{rest_value}"), 0.95));
            if host == "localhost" {
                out.push((format!("{spoken}локалхост{rest_value}"), 0.95));
            }
        }
    }
    out
}

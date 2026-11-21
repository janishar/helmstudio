//! Clock, time-zone and time-range readings.
use super::cardinal::base_num;

fn minute(s: &str) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n < 10 { Some(format!("אפס {}", base_num(&s[1..], false)?)) } else { base_num(s, false) }
}
fn hm(s: &str) -> Option<String> {
    let (h, m) = s.split_once(':')?;
    if h.is_empty() || h.len() > 2 || !h.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: u8 = h.parse().ok()?;
    if n > 24 || h.len() == 2 && h.starts_with('0') && n >= 10 {
        return None;
    }
    let hour = if n == 0 {
        "חצות".to_owned()
    } else {
        let number = if h.starts_with('0') { &h[1..] } else { h };
        base_num(number, false)?
    };
    if m == "00" { Some(hour) } else { Some(format!("{hour} {}", minute(m)?)) }
}
fn hm_all(s: &str) -> Vec<String> {
    let Some(first) = hm(s) else { return Vec::new() };
    let mut out = vec![first.clone()];
    if s.ends_with(":00") {
        out.push(format!("{first} אפס אפס"));
    }
    out
}
fn core(s: &str) -> Vec<(String, i64)> {
    if let Some((prefix, sec)) = s.rsplit_once(':') {
        if prefix.contains(':') {
            return match minute(sec) {
                Some(sec) => hm_all(prefix).into_iter().map(|v| (format!("{v} ו{sec} שניות"), 900_000)).collect(),
                None => Vec::new(),
            };
        }
    }
    hm_all(s).into_iter().map(|v| (v, 1_000_000)).collect()
}
fn hm_count(s: &str) -> usize {
    let Some((h, m)) = s.split_once(':') else { return 0 };
    if h.is_empty()
        || h.len() > 2
        || !h.bytes().all(|b| b.is_ascii_digit())
        || m.len() != 2
        || !m.bytes().all(|b| b.is_ascii_digit())
    {
        return 0;
    }
    let Ok(hour) = h.parse::<u8>() else { return 0 };
    if hour > 24 {
        return 0;
    }
    if m == "00" { 2 } else { 1 }
}

fn core_shape(s: &str) -> (usize, i64) {
    if let Some((prefix, sec)) = s.rsplit_once(':') {
        if prefix.contains(':') {
            return if sec.len() == 2 && sec.bytes().all(|b| b.is_ascii_digit()) {
                (hm_count(prefix), 900_000)
            } else {
                (0, 900_000)
            };
        }
    }
    (hm_count(s), 1_000_000)
}

/// Enumerate source costs without spelling hours, minutes or time zones.
pub(crate) fn weights(s: &str, mut push: impl FnMut(i64)) {
    let (count, weight) = core_shape(s);
    for _ in 0..count {
        push(weight + 1_890_000);
    }
    for tz in ["UTC", "GMT", "EST", "PST", "CET", "IDT", "IST"] {
        if let Some(prefix) = s.strip_suffix(tz) {
            let (count, weight) = core_shape(prefix.strip_suffix(' ').unwrap_or(prefix));
            for _ in 0..count {
                push(weight + 1_790_000);
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '־') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let rest = &s[i + c.len_utf8()..];
            let b = rest.strip_prefix(' ').unwrap_or(rest);
            for _ in 0..hm_count(a) * hm_count(b) {
                push(1_810_000);
            }
        }
    }
}

fn hm_at(s: &str, rank: usize) -> Option<String> {
    if rank >= hm_count(s) {
        return None;
    }
    let mut name = hm(s)?;
    if rank == 1 {
        name.push_str(" אפס אפס");
    }
    Some(name)
}

fn core_at(s: &str, rank: usize) -> Option<String> {
    if let Some((prefix, seconds)) = s.rsplit_once(':') {
        if prefix.contains(':') {
            let mut name = hm_at(prefix, rank)?;
            name.push_str(" ו");
            name.push_str(&minute(seconds)?);
            name.push_str(" שניות");
            return Some(name);
        }
    }
    hm_at(s, rank)
}

/// Render only the selected clock, zone or range path.
pub(crate) fn name_at(s: &str, mut rank: usize) -> Option<String> {
    let count = core_shape(s).0;
    if rank < count {
        return core_at(s, rank);
    }
    rank -= count;
    for (tz, name) in [
        ("UTC", "יוטוסי"),
        ("GMT", "ג׳יאמטי"),
        ("EST", "אי אס טי"),
        ("PST", "פי אס טי"),
        ("CET", "סי אי טי"),
        ("IDT", "שעון ישראל"),
        ("IST", "שעון ישראל"),
    ] {
        if let Some(prefix) = s.strip_suffix(tz) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            let count = core_shape(prefix).0;
            if rank < count {
                let mut out = core_at(prefix, rank)?;
                out.push(' ');
                out.push_str(name);
                return Some(out);
            }
            rank -= count;
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '־') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let rest = &s[i + c.len_utf8()..];
            let b = rest.strip_prefix(' ').unwrap_or(rest);
            let b_count = hm_count(b);
            let count = hm_count(a) * b_count;
            if rank < count {
                let mut out = hm_at(a, rank / b_count)?;
                out.push_str(" עד ");
                out.push_str(&hm_at(b, rank % b_count)?);
                return Some(out);
            }
            rank -= count;
        }
    }
    None
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (name, weight) in core(s) {
        out.push((name, weight + 1_890_000));
    }
    for (tz, name) in [
        ("UTC", "יוטוסי"),
        ("GMT", "ג׳יאמטי"),
        ("EST", "אי אס טי"),
        ("PST", "פי אס טי"),
        ("CET", "סי אי טי"),
        ("IDT", "שעון ישראל"),
        ("IST", "שעון ישראל"),
    ] {
        if let Some(prefix) = s.strip_suffix(tz) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            for (v, w) in core(prefix) {
                out.push((format!("{v} {name}"), w + 1_790_000));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–' | '—' | '־') {
            continue;
        }
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let after = &s[i + c.len_utf8()..];
        let b = after.strip_prefix(' ').unwrap_or(after);
        for a in hm_all(a) {
            for b in hm_all(b) {
                out.push((format!("{a} עד {b}"), 1_810_000));
            }
        }
    }
    out
}

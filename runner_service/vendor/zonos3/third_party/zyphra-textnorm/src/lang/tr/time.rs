//! Clock and timezone branches from `tr/taggers/time.py`.
use super::graph_utils::{append_positive, digit_sequence, positive};

fn valid_hour(s: &str) -> bool {
    if s == "00" || s == "0" {
        return true;
    }
    s.len() <= 2 && s.bytes().all(|b| b.is_ascii_digit()) && s.parse::<u8>().ok().is_some_and(|n| n > 0 && n <= 23)
}
fn valid_minute(s: &str) -> bool {
    s.len() == 2 && s.bytes().all(|b| b.is_ascii_digit()) && s.parse::<u8>().ok().is_some_and(|n| n <= 59)
}
fn valid_base(s: &str) -> bool {
    let mut fields = s.split(':');
    let Some(h) = fields.next() else { return false };
    let Some(m) = fields.next() else { return false };
    if !valid_hour(h) || !valid_minute(m) {
        return false;
    }
    match fields.next() {
        Some(sec) => valid_minute(sec) && fields.next().is_none(),
        None => true,
    }
}
const ZONES: [&str; 11] = ["UTC", "GMT", "CET", "EET", "EST", "PST", "TSİ", "e.m. est", "e.m. cet", "e.m.", "ö.s."];
fn zone_count(s: &str) -> usize {
    ZONES
        .into_iter()
        .chain(["ö.ö."])
        .filter(|&tz| s.strip_suffix(tz).and_then(|v| v.strip_suffix(' ')).is_some_and(valid_base))
        .count()
}
/// Count clock paths without constructing a spoken value.
pub fn count(s: &str) -> usize {
    let mut n = usize::from(valid_base(s)) + zone_count(s);
    for prefix in ["saat ", "Saat ", "kl. "] {
        if let Some(rest) = s.strip_prefix(prefix) {
            n += usize::from(valid_hour(rest)) + usize::from(valid_base(rest)) + zone_count(rest);
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            n += usize::from(valid_base(a) && valid_base(b));
        }
    }
    n
}

fn hour(s: &str) -> Option<String> {
    if s == "00" {
        return Some("sıfır sıfır".into());
    }
    if s == "0" {
        return Some("sıfır".into());
    }
    if s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n == 0 || n > 23 || (s.len() == 2 && s.starts_with('0') && n >= 10) {
        return None;
    }
    positive(&n.to_string())
}
fn minute(s: &str) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|b| b.is_ascii_digit()) || s.parse::<u8>().ok()? > 59 {
        return None;
    }
    if s.starts_with('0') { digit_sequence(s) } else { positive(s) }
}
fn base(s: &str) -> Option<String> {
    let mut p = s.split(':');
    let h = hour(p.next()?)?;
    let m = p.next()?;
    let sec = p.next();
    if p.next().is_some() {
        return None;
    }
    let m_word = minute(m)?;
    match sec {
        Some(sec) => Some(format!("{h} {m_word} {}", minute(sec)?)),
        None if m == "00" => Some(h),
        None => Some(format!("{h} {m_word}")),
    }
}
fn append_hour(out: &mut String, s: &str) -> Option<()> {
    if !valid_hour(s) {
        return None;
    }
    if s == "00" {
        out.push_str("sıfır\u{a0}sıfır");
        return Some(());
    }
    let s = if s.len() == 2 && s.starts_with('0') { &s[1..] } else { s };
    append_positive(out, s, '\u{a0}')
}
fn append_minute(out: &mut String, s: &str) -> Option<()> {
    if !valid_minute(s) {
        return None;
    }
    if s.starts_with('0') {
        out.push_str("sıfır\u{a0}");
        append_positive(out, &s[1..], '\u{a0}')
    } else {
        append_positive(out, s, '\u{a0}')
    }
}
fn append_base(out: &mut String, s: &str) -> Option<()> {
    if !valid_base(s) {
        return None;
    }
    let mut fields = s.split(':');
    let h = fields.next()?;
    let m = fields.next()?;
    let sec = fields.next();
    append_hour(out, h)?;
    if m != "00" || sec.is_some() {
        out.push('\u{a0}');
        append_minute(out, m)?;
    }
    if let Some(sec) = sec {
        out.push('\u{a0}');
        append_minute(out, sec)?;
    }
    Some(())
}
/// Write the first plain clock reading directly into the final tag.
pub fn simple_tag(s: &str) -> Option<String> {
    let (rest, prefixed) = if valid_base(s) {
        (s, false)
    } else {
        let rest = ["saat ", "Saat ", "kl. "].iter().find_map(|p| s.strip_prefix(*p))?;
        if !valid_hour(rest) && !valid_base(rest) {
            return None;
        }
        (rest, true)
    };
    let mut tag = String::with_capacity(s.len() * 4 + 16);
    tag.push_str("name: \"");
    if prefixed {
        tag.push_str("saat\u{a0}");
    }
    if prefixed && valid_hour(rest) {
        append_hour(&mut tag, rest)?;
    } else {
        append_base(&mut tag, rest)?;
    }
    tag.push('"');
    Some(tag)
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(v) = base(s) {
        out.push(v);
    }
    for (tz, spoken) in [
        ("UTC", "u t c"),
        ("GMT", "g m t"),
        ("CET", "c e t"),
        ("EET", "e e t"),
        ("EST", "e s t"),
        ("PST", "p s t"),
        ("TSİ", "t s i"),
        ("e.m. est", "e s t"),
        ("e.m. cet", "c e t"),
        ("e.m.", "öğleden sonra"),
        ("ö.s.", "öğleden sonra"),
        ("ö.ö.", "öğleden önce"),
    ] {
        if let Some(pre) = s.strip_suffix(tz).and_then(|v| v.strip_suffix(' ')) {
            if let Some(v) = base(pre) {
                out.push(format!("{v} {spoken}"));
            }
        }
    }
    for prefix in ["saat ", "Saat ", "kl. "] {
        if let Some(rest) = s.strip_prefix(prefix) {
            if let Some(v) = hour(rest) {
                out.push(format!("saat {v}"));
            }
            if let Some(v) = base(rest) {
                out.push(format!("saat {v}"));
            }
            for (tz, spoken) in [
                ("UTC", "u t c"),
                ("GMT", "g m t"),
                ("CET", "c e t"),
                ("EET", "e e t"),
                ("EST", "e s t"),
                ("PST", "p s t"),
                ("TSİ", "t s i"),
                ("e.m. est", "e s t"),
                ("e.m. cet", "c e t"),
                ("e.m.", "öğleden sonra"),
                ("ö.s.", "öğleden sonra"),
                ("ö.ö.", "öğleden önce"),
            ] {
                if let Some(pre) = rest.strip_suffix(tz).and_then(|v| v.strip_suffix(' ')) {
                    if let Some(v) = base(pre) {
                        out.push(format!("saat {v} {spoken}"));
                    }
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            if let (Some(a), Some(b)) = (base(a), base(b)) {
                out.push(format!("{a} ila {b}"));
            }
        }
    }
    out
}

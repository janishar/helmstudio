use super::graph_utils::{Gender, cardinal};
use super::numeric::number;
use super::ordinal::ordinal;
use std::sync::LazyLock;

const MONTHS: [&str; 12] = [
    "януари",
    "февруари",
    "март",
    "април",
    "май",
    "юни",
    "юли",
    "август",
    "септември",
    "октомври",
    "ноември",
    "декември",
];

const ERAS: &[(&str, &str)] = &[
    ("пр.н.е.", "преди новата ера"),
    ("пр. н. е.", "преди новата ера"),
    ("пр.н.е", "преди новата ера"),
    ("пр. Хр.", "преди новата ера"),
    ("пр.Хр.", "преди новата ера"),
    ("BC", "преди новата ера"),
    ("B.C.", "преди новата ера"),
    ("след н.е.", "след новата ера"),
    ("след н. е.", "след новата ера"),
    ("от н.е.", "след новата ера"),
    ("AD", "след новата ера"),
    ("A.D.", "след новата ера"),
];
static DATE_CHARS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars: Vec<char> = MONTHS
        .iter()
        .flat_map(|s| s.chars())
        .chain(ERAS.iter().flat_map(|(key, _)| key.chars()))
        .chain("годинаг.".chars())
        .collect();
    chars.sort_unstable();
    chars.dedup();
    chars
});

/// Alphabet of the source date labels, eras, month names, and numeric syntax.
pub(crate) fn possible_char(c: char) -> bool {
    c.is_ascii_digit()
        || matches!(c, ' ' | '\u{a0}' | '.' | ',' | '/' | '-' | '–' | '—')
        || DATE_CHARS.binary_search(&c).is_ok()
}
fn day(s: &str) -> Option<String> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    ordinal(&n.to_string(), Gender::Masculine)
}
fn month(s: &str) -> Option<&'static str> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    MONTHS.get(n.checked_sub(1)?).copied()
}
fn trim_label(s: &str) -> &str {
    for x in ["година", "г.", "г"] {
        if let Some(v) = s.strip_suffix(x) {
            return v.strip_suffix(' ').unwrap_or(v);
        }
    }
    s
}

/// Outputs are either a name or the source ISO date field layout.
pub(crate) fn date(s: &str) -> Vec<(String, f64, bool)> {
    let mut out = Vec::new();
    let mut iso = s.split('-');
    if let (Some(year), Some(month_part), Some(day_part), None) = (iso.next(), iso.next(), iso.next(), iso.next()) {
        if year.len() == 4 && month_part.len() == 2 && day_part.len() == 2 {
            if let (Some(y), Some(m), Some(d)) = (ordinal(year, Gender::Feminine), month(month_part), day(day_part)) {
                out.push((format!("date {{ year: \"{y} година\" month: \"{m}\" day: \"{d}\" }}"), 0.0, true));
            }
        }
    }
    let raw = trim_label(s);
    let mut numeric = raw.split(|c| c == '.' || c == '/');
    if let (Some(day_part), Some(month_part), Some(year), None) =
        (numeric.next(), numeric.next(), numeric.next(), numeric.next())
    {
        if year.len() == 2 || year.len() == 4 {
            if let (Some(d), Some(m), Some(y)) = (day(day_part), month(month_part), ordinal(year, Gender::Feminine)) {
                out.push((format!("{d} {m} {y} година"), 0.95, false));
            }
        }
    }
    let mut written = raw.split(' ');
    if let (Some(day_part), Some(month_part)) = (written.next(), written.next()) {
        if MONTHS.contains(&month_part) {
            if let Some(d) = day(day_part) {
                match (written.next(), written.next()) {
                    (None, _) if raw == s => out.push((format!("{d} {month_part}"), 0.97, false)),
                    (Some(year), None) => {
                        if let Some(y) = ordinal(year, Gender::Feminine) {
                            out.push((format!("{d} {month_part} {y} година"), 0.96, false));
                        }
                    }
                    _ => {}
                }
            }
        }
    }
    if raw != s {
        if let Some(y) = ordinal(raw, Gender::Feminine) {
            out.push((format!("{y} година"), 0.98, false));
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let r = &s[i + c.len_utf8()..];
            let b = r.strip_prefix(' ').unwrap_or(r);
            if let (Some(x), Some(y)) = (ordinal(a, Gender::Feminine), ordinal(b, Gender::Feminine)) {
                out.push((format!("{x} до {y}"), 0.99, false));
            }
        }
    }
    for &(suffix, word) in ERAS {
        if let Some(n) = s.strip_suffix(suffix) {
            if let Some(v) = number(n.strip_suffix(' ').unwrap_or(n), Gender::Masculine) {
                out.push((format!("{v} {word}"), 1.0, false));
            }
        }
    }
    for p in ["AD", "A.D."] {
        if let Some(n) = s.strip_prefix(p) {
            if let Some(v) = number(n.strip_prefix(' ').unwrap_or(n), Gender::Masculine) {
                out.push((format!("{v} след новата ера"), 1.0, false));
            }
        }
    }
    out
}
pub(crate) fn clock(s: &str) -> Option<(String, bool)> {
    let mut parts = s.split(':');
    let hours = parts.next()?;
    let minutes = parts.next()?;
    let seconds = parts.next();
    if parts.next().is_some()
        || hours.is_empty()
        || hours.len() > 2
        || minutes.len() != 2
        || !hours.bytes().all(|c| c.is_ascii_digit())
        || !minutes.bytes().all(|c| c.is_ascii_digit())
        || seconds.is_some_and(|s| !s.bytes().all(|c| c.is_ascii_digit()))
    {
        return None;
    }
    let h: usize = hours.parse().ok()?;
    let m: usize = minutes.parse().ok()?;
    if h > 23 || m > 59 {
        return None;
    }
    let hour_digits = hours.trim_start_matches('0');
    let mut hw = cardinal(if hour_digits.is_empty() { "0" } else { hour_digits }, Gender::Masculine)?;
    if let Some(seconds) = seconds {
        if seconds.len() != 2 {
            return None;
        }
        let sec: usize = seconds.parse().ok()?;
        if m == 0 || sec == 0 || sec > 59 {
            return None;
        }
        hw.push_str(" часа, ");
        hw.push_str(&cardinal(minutes.trim_start_matches('0'), Gender::Feminine)?);
        hw.push_str(" минути и ");
        hw.push_str(&cardinal(seconds.trim_start_matches('0'), Gender::Feminine)?);
        hw.push_str(" секунди");
        return Some((hw, true));
    }
    if m == 0 {
        Some((hw, false))
    } else {
        hw.push_str(" и ");
        hw.push_str(&cardinal(minutes.trim_start_matches('0'), Gender::Feminine)?);
        Some((hw, false))
    }
}
pub(crate) fn time(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some((v, hms)) = clock(s) {
        if hms {
            out.push((v, 0.96))
        } else {
            if s == "00:00" || s == "0:00" {
                out.push(("нула часа".to_owned(), 0.99));
            }
            out.push((v, if s.ends_with(":00") { 1.05 } else { 1.0 }));
        }
    }
    for (ending, word) in [
        ("AM", "сутринта"),
        ("A.M.", "сутринта"),
        ("am", "сутринта"),
        ("a.m.", "сутринта"),
        ("PM", "следобед"),
        ("P.M.", "следобед"),
        ("pm", "следобед"),
        ("p.m.", "следобед"),
    ] {
        if let Some(t) = s.strip_suffix(ending) {
            let t = t.strip_suffix(' ').unwrap_or(t);
            if let Some((v, false)) = clock(t) {
                out.push((format!("{v} {word}"), 0.97));
            }
        }
    }
    for tz in ["UTC", "GMT", "EET", "EEST", "CET", "CEST"] {
        if let Some(t) = s.strip_suffix(tz).and_then(|t| t.strip_suffix(' ')) {
            if let Some((v, false)) = clock(t) {
                out.push((format!("{v} {tz}"), 0.98));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let r = &s[i + c.len_utf8()..];
            let b = r.strip_prefix(' ').unwrap_or(r);
            if let (Some((a, false)), Some((b, false))) = (clock(a), clock(b)) {
                out.push((format!("{a} до {b}"), 0.95));
            }
        }
    }
    out
}

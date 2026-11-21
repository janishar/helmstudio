//! Written dates, ISO fields, decades, eras and year ranges.
use super::numeric::{self, Reading};

const MONTHS: [&str; 12] = [
    "januari",
    "februari",
    "maart",
    "april",
    "mei",
    "juni",
    "juli",
    "augustus",
    "september",
    "oktober",
    "november",
    "december",
];
const SHORT: [&str; 12] = ["jan", "feb", "mrt", "apr", "mei", "jun", "jul", "aug", "sep", "okt", "nov", "dec"];

pub(super) fn month_prefix(s: &str) -> bool {
    MONTHS.iter().chain(SHORT.iter()).any(|m| m.starts_with(s)) || "sept".starts_with(s)
}

/// Cheap source-shape gate before constructing any date readings.
pub(super) fn could_match(s: &str) -> bool {
    if s.chars().any(|c| matches!(c, '/' | '.' | '-' | '–' | '—' | 'C' | 'D')) || s.ends_with('s') {
        return true;
    }
    let Some((first, rest)) = s.split_once(' ') else { return false };
    if first == "jaren" {
        return true;
    }
    let written_month = rest.split_once(' ').map_or(rest, |(month, _)| month);
    !written_month.bytes().all(|b| b.is_ascii_digit()) && month(written_month).is_some()
}

fn month(s: &str) -> Option<&'static str> {
    if s.len() <= 2 && s.bytes().all(|x| x.is_ascii_digit()) {
        if s.len() == 2 && s.starts_with('0') && s == "00" {
            return None;
        }
        let n: usize = s.parse().ok()?;
        return MONTHS.get(n.checked_sub(1)?).copied();
    }
    if let Some(i) = MONTHS.iter().position(|m| *m == s) {
        return Some(MONTHS[i]);
    }
    let s = s.strip_suffix('.').unwrap_or(s);
    if s == "sept" {
        return Some("september");
    }
    for i in 0..12 {
        if i != 4 && s == SHORT[i] {
            return Some(MONTHS[i]);
        }
    }
    None
}
fn day(s: &str) -> Option<String> {
    let s = s.strip_prefix('0').unwrap_or(s);
    if s.is_empty() || s.len() > 2 || s.starts_with('0') {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n == 0 || n > 99 {
        return None;
    }
    super::cardinal::raw(s)
}
fn years(s: &str) -> Vec<Reading> {
    if s.len() < 4 || s.len() > 6 || s.starts_with('0') || !s.bytes().all(|x| x.is_ascii_digit()) {
        return vec![];
    }
    numeric::integer(s)
}

/// Returns an already serialized tag and internal weight.
pub(super) fn parse(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    {
        let v: Vec<_> = s.split(['/', '.', '-']).collect();
        if v.len() == 3 {
            if v[1].bytes().all(|b| b.is_ascii_digit()) {
                if let (Some(d), Some(m)) = (day(v[0]), month(v[1])) {
                    for (y, w) in years(v[2]) {
                        out.push((name(&format!("{d} {m} {y}")), 0.95 + w));
                    }
                }
            }
            if s.len() == 10
                && s.as_bytes()[4] == b'-'
                && s.as_bytes()[7] == b'-'
                && v[0].len() == 4
                && v[1].len() == 2
                && v[2].len() == 2
            {
                if let (Some(m), Some(d)) = (month(v[1]), day(v[2])) {
                    if v[2].parse::<u8>().ok().is_some_and(|n| (1..=31).contains(&n)) {
                        for (y, w) in years(v[0]) {
                            out.push((format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"), w));
                        }
                    }
                }
            }
        }
    }
    let parts: Vec<_> = s.split(' ').collect();
    if parts.len() == 2 || parts.len() == 3 {
        if let (Some(d), Some(m)) = (day(parts[0]), month(parts[1])) {
            if !parts[1].bytes().all(|c| c.is_ascii_digit()) {
                if parts.len() == 2 {
                    out.push((name(&format!("{d} {m}")), 0.96));
                } else {
                    for (y, w) in years(parts[2]) {
                        out.push((name(&format!("{d} {m} {y}")), 0.96 + w));
                    }
                }
            }
        }
    }
    let intl = [
        ("2020s", "twintig"),
        ("2010s", "tien"),
        ("2000s", "nul"),
        ("1990s", "negentig"),
        ("1980s", "tachtig"),
        ("1970s", "zeventig"),
        ("1960s", "zestig"),
        ("1950s", "vijftig"),
        ("1940s", "veertig"),
        ("1930s", "dertig"),
        ("1920s", "twintig"),
        ("90s", "negentig"),
        ("80s", "tachtig"),
        ("70s", "zeventig"),
        ("60s", "zestig"),
        ("50s", "vijftig"),
    ];
    for (k, v) in intl {
        if s == k {
            out.push((name(&format!("jaren {v}")), 0.97));
        }
    }
    if let Some(x) = s.strip_prefix("jaren ") {
        let x = x.strip_prefix('\'').unwrap_or(x);
        let v = match x {
            "20" | "2020" => Some("twintig"),
            "30" => Some("dertig"),
            "40" => Some("veertig"),
            "50" => Some("vijftig"),
            "60" => Some("zestig"),
            "70" => Some("zeventig"),
            "80" | "1980" => Some("tachtig"),
            "90" | "1990" => Some("negentig"),
            _ => None,
        };
        if let Some(v) = v {
            out.push((name(&format!("jaren {v}")), 0.97));
        }
    }
    for (era, word) in [
        ("v.Chr.", "voor Christus"),
        ("v. Chr.", "voor Christus"),
        ("v.C.", "voor Christus"),
        ("v. C.", "voor Christus"),
        ("BC", "voor Christus"),
        ("B.C.", "voor Christus"),
        ("n.Chr.", "na Christus"),
        ("n. Chr.", "na Christus"),
        ("n.C.", "na Christus"),
        ("n. C.", "na Christus"),
        ("AD", "na Christus"),
        ("A.D.", "na Christus"),
    ] {
        if let Some(a) = s.strip_suffix(era) {
            for (v, w) in numeric::integer(a.strip_suffix(' ').unwrap_or(a)) {
                out.push((name(&format!("{v} {word}")), 0.98 + w));
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        for (a, aw) in years(s[..i].trim_end_matches(crate::text::is_nemo_space)) {
            for (b, bw) in years(s[i + c.len_utf8()..].trim_start_matches(crate::text::is_nemo_space)) {
                out.push((name(&format!("{a} tot {b}")), 0.98 + aw + bw));
            }
        }
    }
    out
}
fn name(s: &str) -> String {
    format!("name: \"{}\"", s.replace(' ', "\u{a0}"))
}

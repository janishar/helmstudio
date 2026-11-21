// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
use super::graph_utils::{Gender, number};
const MONTHS: [&str; 12] = [
    "ianuarie",
    "februarie",
    "martie",
    "aprilie",
    "mai",
    "iunie",
    "iulie",
    "august",
    "septembrie",
    "octombrie",
    "noiembrie",
    "decembrie",
];
const ABBR: [&str; 12] =
    ["ian.", "feb.", "mar.", "apr.", "mai", "iun.", "iul.", "aug.", "sept.", "oct.", "nov.", "dec."];
fn small(s: &str, max: u8) -> Option<u8> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if n == 0 || n > max || (s.len() == 2 && s.starts_with('0') && n >= 10) { None } else { Some(n) }
}
fn day(s: &str) -> Option<String> {
    let n = small(s, 31)?;
    Some(if n == 1 { "întâi".to_owned() } else { number(&n.to_string(), Gender::Masculine)? })
}
fn month(s: &str) -> Option<&'static str> {
    Some(MONTHS[(small(s, 12)? - 1) as usize])
}
fn written(s: &str) -> Option<&'static str> {
    let lower = s.to_lowercase();
    let valid_case = s == lower
        || s.chars().next().map(|c| c.is_uppercase()).unwrap_or(false) && s.chars().skip(1).all(|c| !c.is_uppercase());
    if !valid_case {
        return None;
    }
    (0..12).find(|&i| lower == MONTHS[i] || lower == ABBR[i]).map(|i| MONTHS[i])
}
/// Results contain either an ISO date tag or the spoken name value.
pub fn date(s: &str) -> Vec<(String, bool)> {
    let mut out = Vec::new();
    for sep in ['/', '.', '-'] {
        let mut parts = s.split(sep);
        if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let Some(m) = month(b) {
                if let (Some(d), Some(y)) = (day(a), number(c, Gender::Masculine)) {
                    out.push((format!("{} {} {}", d, m, y), false));
                }
                if sep == '-' && a.len() == 4 {
                    if let (Some(y), Some(d)) = (number(a, Gender::Masculine), day(c)) {
                        out.push((format!("date {{ year: \"{}\" month: \"{}\" day: \"{}\" }}", y, m, d), true));
                    }
                }
            }
        }
    }
    let mut parts = s.split(' ');
    if let (Some(a), Some(b)) = (parts.next(), parts.next()) {
        let year = parts.next();
        if parts.next().is_none() {
            if let Some(d) = day(a) {
                if let Some(m) = written(b) {
                    if let Some(year) = year {
                        if let Some(y) = number(year, Gender::Masculine) {
                            out.push((format!("{} {} {}", d, m, y), false));
                        }
                    } else {
                        out.push((format!("{} {}", d, m), false));
                    }
                }
            }
        }
    }
    let decade = match s {
        "1990s" | "anii '90" | "anii 90" => Some("nouăzeci"),
        "anii '80" | "anii 80" => Some("optzeci"),
        "anii '70" | "anii 70" => Some("șaptezeci"),
        "anii 1990" => Some("o mie nouă sute nouăzeci"),
        "anii 2000" => Some("două mii"),
        "anii 2020" => Some("două mii douăzeci"),
        _ => None,
    };
    if let Some(v) = decade {
        out.push((format!("anii {}", v), false));
    }
    if let Some(year) = s.strip_suffix('s') {
        if ["1920", "1930", "1940", "1950", "1960", "1970", "1980", "2000", "2010", "2020", "2030"].contains(&year) {
            if let Some(v) = number(year, Gender::Masculine) {
                out.push((format!("anii {}", v), false));
            }
        }
    }
    for (suffix, phrase) in [
        ("î.Hr.", "înainte de Hristos"),
        ("î.e.n.", "înainte de Hristos"),
        ("î.Hr", "înainte de Hristos"),
        ("î.e.n", "înainte de Hristos"),
        ("BC", "înainte de Hristos"),
        ("B.C.", "înainte de Hristos"),
        ("BCE", "înainte de Hristos"),
        ("B.C.E.", "înainte de Hristos"),
        ("d.Hr.", "după Hristos"),
        ("e.n.", "după Hristos"),
        ("d.Hr", "după Hristos"),
        ("e.n", "după Hristos"),
        ("AD", "după Hristos"),
        ("A.D.", "după Hristos"),
        ("CE", "după Hristos"),
        ("C.E.", "după Hristos"),
    ] {
        if let Some(y) = s.strip_suffix(suffix) {
            if let Some(n) = number(y.strip_suffix(' ').unwrap_or(y), Gender::Masculine) {
                out.push((format!("{} {}", n, phrase), false));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '−') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = s[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&s[i + c.len_utf8()..]);
            if a.len() == 4 && b.len() == 4 {
                if let (Some(a), Some(b)) = (number(a, Gender::Masculine), number(b, Gender::Masculine)) {
                    out.push((format!("{} până la {}", a, b), false));
                }
            }
        }
    }
    out
}

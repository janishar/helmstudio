// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::{
    cardinal::{Gender, number},
    ordinal::ordinal,
};

const MONTHS: [&str; 12] = [
    "січня",
    "лютого",
    "березня",
    "квітня",
    "травня",
    "червня",
    "липня",
    "серпня",
    "вересня",
    "жовтня",
    "листопада",
    "грудня",
];
fn component(s: &str, max: usize) -> Option<usize> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if n == 0 || n > max || (s.len() == 2 && n < 10 && !s.starts_with('0')) {
        return None;
    }
    Some(n)
}
fn day(s: &str) -> Option<String> {
    ordinal(&component(s, 31)?.to_string(), 2)
}
fn month(s: &str) -> Option<&'static str> {
    Some(MONTHS[component(s, 12)? - 1])
}
fn numeric_year(s: &str) -> Option<String> {
    if s.len() != 2 && s.len() != 4 {
        return None;
    }
    if s == "2000" { Some("дві тисячі".to_owned()) } else { ordinal(s, 3) }
}

// Returns the source branch's internal weight in hundredths.
pub(super) fn date(s: &str) -> Option<(String, i32)> {
    for (suffix, phrase) in [
        ("до н. е.", "до нашої ери"),
        ("до н.е.", "до нашої ери"),
        ("до н.е", "до нашої ери"),
        ("B.C.", "до нашої ери"),
        ("BC", "до нашої ери"),
        ("н. е.", "нашої ери"),
        ("н.е.", "нашої ери"),
        ("A.D.", "нашої ери"),
        ("AD", "нашої ери"),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(w) = super::cardinal::graph(n, Gender::Masculine, false) {
                return Some((format!("{w} {phrase}"), 8));
            }
        }
    }
    for prep in ["в ", "у ", "В ", "У "] {
        if let Some(rest) = s.strip_prefix(prep) {
            if let Some((year, suffix)) = rest.split_once(' ') {
                if year.len() == 4 && ["році", "году", "р.", "р"].contains(&suffix) {
                    return Some((format!("{prep}{} році", ordinal(year, 4)?), 7));
                }
            }
        }
    }
    for dash in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(dash) {
            if a.len() == 4 && b.len() == 4 {
                if let (Some(a), Some(b)) = (number(a, Gender::Masculine, true), number(b, Gender::Masculine, true)) {
                    return Some((format!("від {a} до {b}"), 10));
                }
            }
        }
    }
    for sep in ['-', '/'] {
        let p: Vec<_> = s.split(sep).collect();
        if p.len() == 3 && p[1].len() == 2 && p[2].len() == 2 {
            if let (Some(y), Some(m), Some(d)) = (ordinal(p[0], 3), month(p[1]), day(p[2])) {
                return Some((format!("{d} {m} {y}"), -10));
            }
        }
    }
    let (base, year_suffix) = ["року", "года", "р.", "г.", "р", "г"]
        .iter()
        .find_map(|x| s.strip_suffix(*x))
        .map(|x| (x, true))
        .unwrap_or((s, false));
    for sep in ['.', '/'] {
        let p: Vec<_> = base.split(sep).collect();
        if p.len() == 3 {
            if let (Some(d), Some(m), Some(y)) = (day(p[0]), month(p[1]), numeric_year(p[2])) {
                return Some((format!("{d} {m} {y}{}", if year_suffix { " року" } else { "" }), 6));
            }
        }
    }
    for m in MONTHS {
        if let Some((d, after)) = base.split_once(m) {
            let d = d.strip_suffix(' ').unwrap_or(d);
            if let Some(d) = day(d) {
                if after.is_empty() && !year_suffix {
                    return Some((format!("{d} {m}"), 5));
                }
                let y = after.strip_prefix(' ').unwrap_or(after);
                if let Some(y) = numeric_year(y) {
                    return Some((format!("{d} {m} {y}{}", if year_suffix { " року" } else { "" }), 5));
                }
            }
        }
    }
    None
}

// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::number_names::{Case, Gender, cardinal, ordinal};
use super::numeric::grouped;
const M: Gender = Gender::Masculine;
const F: Gender = Gender::Feminine;
const N: Case = Case::Nominative;
const G: Case = Case::Genitive;
const MONTHS: [&str; 12] = [
    "stycznia",
    "lutego",
    "marca",
    "kwietnia",
    "maja",
    "czerwca",
    "lipca",
    "sierpnia",
    "września",
    "października",
    "listopada",
    "grudnia",
];
// Index the source vocabularies by their initial byte: dates beginning with
// a different letter can be rejected without scanning every month and era.
static DATE_PREFIXES: std::sync::LazyLock<[Vec<(&'static str, bool)>; 128]> = std::sync::LazyLock::new(|| {
    let mut index: [Vec<(&'static str, bool)>; 128] = std::array::from_fn(|_| Vec::new());
    for month in MONTHS {
        index[month.as_bytes()[0] as usize].push((month, true));
    }
    for word in [
        "lata 90.",
        "lata 90-te",
        "lata 90",
        "lata 1990.",
        "lata 1990-te",
        "lata 1990",
        "r",
        "r.",
        "roku",
        "p.n.e.",
        "p.n.e",
        "BC",
        "BCE",
        "B.C.",
        "B.C.E.",
        "n.e.",
        "n.e",
        "AD",
        "CE",
        "A.D.",
        "C.E.",
        "s",
    ] {
        index[word.as_bytes()[0] as usize].push((word, false));
    }
    index
});

pub fn alphabetic_prefix(tail: &str) -> bool {
    let Some(&first) = tail.as_bytes().first() else {
        return false;
    };
    let Some(words) = DATE_PREFIXES.get(first as usize) else {
        return false;
    };
    words.iter().any(|&(word, month)| {
        word.starts_with(tail) || (month && tail.strip_prefix(word).is_some_and(|rest| rest.starts_with(' ')))
    })
}
fn day(s: &str) -> Option<String> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let d: u8 = s.parse().ok()?;
    if !(1..=31).contains(&d) {
        return None;
    }
    ordinal(&d.to_string(), M, G)
}
fn month(s: &str) -> Option<&'static str> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    MONTHS.get(n.checked_sub(1)?).copied()
}
fn year(s: &str) -> Option<String> {
    if s.len() != 4 {
        return None;
    }
    Some(format!("{} roku", ordinal(s, M, G)?))
}
pub fn date(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (key, val) in [
        ("lata 90.", "lata dziewięćdziesiąte"),
        ("lata 90-te", "lata dziewięćdziesiąte"),
        ("lata 90", "lata dziewięćdziesiąte"),
        ("lata 1990.", "lata tysiąc dziewięćset dziewięćdziesiąte"),
        ("lata 1990-te", "lata tysiąc dziewięćset dziewięćdziesiąte"),
        ("lata 1990", "lata tysiąc dziewięćset dziewięćdziesiąte"),
        ("2020s", "lata dwudzieste"),
        ("1990s", "lata dziewięćdziesiąte"),
        ("1980s", "lata osiemdziesiąte"),
        ("2010s", "lata dwutysięczne dziesiąte"),
    ] {
        if s == key {
            out.push((val.into(), 0.0));
        }
    }
    for sep in ['.', '/'] {
        if let Some((d, rest)) = s.split_once(sep) {
            if let Some((m, y)) = rest.split_once(sep) {
                if let (Some(d), Some(m), Some(y)) = (day(d), month(m), year(y)) {
                    out.push((format!("{d} {m} {y}"), 0.0));
                }
            }
        }
    }
    if let Some((raw_day, rest)) = s.split_once(' ') {
        let (m, rest) = rest.split_once(' ').map_or((rest, None), |(m, r)| (m, Some(r)));
        if MONTHS.contains(&m) {
            if let Some(d) = day(raw_day.strip_suffix('.').unwrap_or(raw_day)) {
                if let Some(rest) = rest {
                    let (y, suffix) = rest.split_once(' ').map_or((rest, None), |(y, suffix)| (y, Some(suffix)));
                    if suffix.is_none() || suffix.is_some_and(|s| ["r.", "r", "roku"].contains(&s)) {
                        if let Some(y) = year(y) {
                            out.push((format!("{d} {m} {y}"), 0.0));
                        }
                    }
                } else {
                    out.push((format!("{d} {m}"), 0.0));
                }
            }
        }
    }
    for suffix in [" r.", " r", " roku"] {
        if let Some(y) = s.strip_suffix(suffix).and_then(year) {
            out.push((y, 0.0));
        }
    }
    for sep in ['-', '–', '—'] {
        if let Some((a, b)) = s.split_once(sep) {
            if a.len() == 4 && b.len() == 4 {
                if let (Some(a), Some(b)) = (grouped(a, M, N, true), grouped(b, M, N, true)) {
                    out.push((format!("{a} do {b}"), 0.0));
                }
            }
        }
    }
    for (suffix, word) in [
        ("p.n.e.", "przed naszą erą"),
        ("p.n.e", "przed naszą erą"),
        ("BC", "przed naszą erą"),
        ("BCE", "przed naszą erą"),
        ("B.C.", "przed naszą erą"),
        ("B.C.E.", "przed naszą erą"),
        ("n.e.", "naszej ery"),
        ("n.e", "naszej ery"),
        ("AD", "naszej ery"),
        ("CE", "naszej ery"),
        ("A.D.", "naszej ery"),
        ("C.E.", "naszej ery"),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(n) = grouped(n, M, N, true) {
                out.push((format!("{n} {word}"), 0.0));
            }
        }
    }
    out
}
pub fn iso(s: &str) -> Option<(String, String, String)> {
    let (y, rest) = s.split_once('-')?;
    let (m, d) = rest.split_once('-')?;
    Some((year(y)?, month(m)?.into(), day(d)?))
}
fn time_core(s: &str, genitive: bool) -> Option<String> {
    let mut parts = s.split(':');
    let h = parts.next()?;
    let minute = parts.next()?;
    let second = parts.next();
    if parts.next().is_some() {
        return None;
    }
    if h.is_empty() || h.len() > 2 || !h.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let hn: u8 = h.parse().ok()?;
    if hn > 23 || (h.len() == 2 && h.starts_with('0') && hn >= 10) {
        return None;
    }
    let mut out = if hn == 0 {
        if genitive { "zera".to_string() } else { "zero".to_string() }
    } else {
        ordinal(&hn.to_string(), F, if genitive { G } else { N })?
    };
    for part in [Some(minute), second].into_iter().flatten() {
        if part.len() != 2 || !part.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        out.push(' ');
        if part.starts_with('0') {
            out.push_str("zero ");
            out.push_str(&cardinal(&part[1..], M, N)?);
        } else {
            out.push_str(&cardinal(part, M, N)?);
        }
    }
    Some(out)
}
pub fn simple_time_words(s: &str, rank: i32) -> Option<String> {
    if s == "00:00" && rank == 0 { Some("północ".into()) } else { time_core(s, false) }
}
fn time_single(s: &str, genitive: bool) -> Vec<String> {
    let mut outputs = Vec::new();
    for i in (1..=s.len()).filter(|&i| s.is_char_boundary(i)) {
        let (core, mut rest) = s.split_at(i);
        let Some(w) = time_core(core, genitive) else {
            continue;
        };
        let mut extras = String::new();
        for (k, v) in [
            (" AM", " rano"),
            (" am", " rano"),
            (" A.M.", " rano"),
            (" a.m.", " rano"),
            (" PM", " po południu"),
            (" pm", " po południu"),
            (" P.M.", " po południu"),
            (" p.m.", " po południu"),
        ] {
            if let Some(r) = rest.strip_prefix(k) {
                rest = r;
                extras.push_str(v);
                break;
            }
        }
        if !rest.is_empty() {
            let mut found = false;
            for tz in ["UTC", "GMT", "CET", "CEST", "EET", "EEST", "PST", "PDT", "EST", "EDT"] {
                let key = format!(" {tz}");
                if let Some(r) = rest.strip_prefix(&key) {
                    let mut extra = format!(
                        " {}",
                        tz.chars().map(|c| c.to_ascii_lowercase().to_string()).collect::<Vec<_>>().join(" ")
                    );
                    if !r.is_empty() {
                        let (r, sg) = if let Some(r) = r.strip_prefix('+') {
                            (r, " plus ")
                        } else if let Some(r) = r.strip_prefix('-').or_else(|| r.strip_prefix('−')) {
                            (r, " minus ")
                        } else {
                            continue;
                        };
                        if r.is_empty() || r.len() > 2 {
                            continue;
                        }
                        let Some(n) = cardinal(r, M, N) else {
                            continue;
                        };
                        extra.push_str(sg);
                        extra.push_str(&n);
                    }
                    extras.push_str(&extra);
                    found = true;
                    break;
                }
            }
            if !found {
                continue;
            }
        }
        if !genitive && core == "00:00" {
            outputs.push(format!("północ{extras}"));
        }
        outputs.push(format!("{w}{extras}"));
    }
    outputs
}
pub fn time(s: &str) -> Vec<(String, f64)> {
    let mut out: Vec<_> = time_single(s, false).into_iter().map(|x| (x, 0.0)).collect();
    for (idx, c) in s.char_indices() {
        if !['-', '–', '—'].contains(&c) {
            continue;
        }
        let a = &s[..idx];
        let b = &s[idx + c.len_utf8()..];
        let a = a.strip_suffix(' ').unwrap_or(a);
        let b = b.strip_prefix(' ').unwrap_or(b);
        if let Some(first) = time_core(a, false) {
            if a == "00:00" {
                for last in time_single(b, true) {
                    out.push((format!("północ do {last}"), 0.0));
                }
            }
            for last in time_single(b, true) {
                out.push((format!("{first} do {last}"), 0.0));
            }
        }
    }
    out
}

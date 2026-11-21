// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
// Number names derive from Unicode CLDR release 49 (Unicode-3.0 license).

const DIGITS: [&str; 10] = ["kosong", "satu", "dua", "tiga", "empat", "lima", "enam", "tujuh", "lapan", "sembilan"];

/// The source cardinal graph accepts zero or an unpadded number of at most 15 digits.
pub(crate) fn cardinal(s: &str) -> Option<String> {
    if s == "0" {
        return Some(if super::lazy::recognizing() { String::new() } else { DIGITS[0].into() });
    }
    if s.is_empty() || s.len() > 15 || s.starts_with('0') || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if super::lazy::recognizing() {
        return Some(String::new());
    }
    let n: u64 = s.parse().ok()?;
    let mut words = String::with_capacity(s.len() * 8);
    for (scale, suffix) in
        [(1_000_000_000_000, "trilion"), (1_000_000_000, "bilion"), (1_000_000, "juta"), (1_000, "ribu"), (1, "")]
    {
        let group = (n / scale % 1000) as usize;
        if group == 0 {
            continue;
        }
        if scale == 1000 && group == 1 {
            append(&mut words, "seribu");
        } else {
            three(&mut words, group);
            if !suffix.is_empty() {
                append(&mut words, suffix);
            }
        }
    }
    Some(words)
}

fn append(out: &mut String, word: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(word);
}
fn three(out: &mut String, n: usize) {
    let hundreds = n / 100;
    if hundreds == 1 {
        append(out, "seratus");
    } else if hundreds > 1 {
        append(out, DIGITS[hundreds]);
        append(out, "ratus");
    }
    let rest = n % 100;
    match rest {
        0 => {}
        1..=9 => append(out, DIGITS[rest]),
        10 => append(out, "sepuluh"),
        11 => append(out, "sebelas"),
        12..=19 => {
            append(out, DIGITS[rest - 10]);
            append(out, "belas");
        }
        _ => {
            append(out, DIGITS[rest / 10]);
            append(out, "puluh");
            if rest % 10 != 0 {
                append(out, DIGITS[rest % 10]);
            }
        }
    }
}

/// Number graph with the source's optional thousands separators.
pub(crate) fn grouped(s: &str) -> Option<String> {
    if let Some(value) = cardinal(s) {
        return Some(value);
    }
    let mut groups = s.split(['.', ',', ' ']);
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut count = 0;
    for group in groups {
        count += 1;
        if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
    }
    if !(1..=4).contains(&count) {
        return None;
    }
    if super::lazy::recognizing() {
        return Some(String::new());
    }
    let ungrouped: String = s.chars().filter(|c| c.is_ascii_digit()).collect();
    cardinal(&ungrouped)
}

pub(crate) fn digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if super::lazy::recognizing() {
        return Some(String::new());
    }
    let mut out = String::with_capacity(s.len() * 7);
    for b in s.bytes() {
        append(&mut out, DIGITS[(b - b'0') as usize]);
    }
    Some(out)
}

// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::number_names::{Case, Gender, cardinal};
use super::numeric::digits;
const FIXED: &[(&str, &str)] = &[
    ("COVID-19", "COVID dziewiętnaście"),
    ("covid-19", "covid dziewiętnaście"),
    ("5G", "pięć G"),
    ("5g", "pięć g"),
    ("4K", "cztery K"),
    ("4k", "cztery k"),
    ("CO2", "C O dwa"),
    ("BA249", "BA dwa cztery dziewięć"),
];

// Recognize the same alternating alpha/numeric segment as `segment`, without
// spelling numbers or allocating its chunks and output strings.
fn segment_accepts(s: &str) -> bool {
    let b = s.as_bytes();
    let mut i = 0;
    let mut pieces = 0;
    while i < b.len() {
        pieces += 1;
        if pieces > 3 {
            return false;
        }
        if b[i].is_ascii_alphabetic() {
            while i < b.len() && b[i].is_ascii_alphabetic() {
                i += 1;
            }
        } else if b[i].is_ascii_digit() {
            loop {
                while i < b.len() && b[i].is_ascii_digit() {
                    i += 1;
                }
                if i + 1 < b.len() && b[i] == b'.' && b[i + 1].is_ascii_digit() {
                    i += 1;
                } else {
                    break;
                }
            }
        } else {
            return false;
        }
    }
    pieces != 0
}

/// Emit branch costs in the same order as `serial`, without rendering any
/// explored reading. The matching rank is sufficient to render a winner.
pub fn recognize(s: &str, mut emit: impl FnMut(f64)) {
    for &(key, _) in FIXED {
        if s == key {
            emit(-0.5);
        }
    }
    fn walk(s: &str, cost: f64, emit: &mut impl FnMut(f64)) {
        if segment_accepts(s) {
            emit(cost);
        }
        for (i, c) in s.char_indices() {
            if "-/:_.".contains(c) && segment_accepts(&s[..i]) {
                walk(&s[i + c.len_utf8()..], cost + if c == '.' { 0.1 } else { 0.0 }, emit);
            }
        }
    }
    walk(s, 0.0, &mut emit);
}
fn number(s: &str) -> Option<String> {
    cardinal(s, Gender::Masculine, Case::Nominative)
        .or_else(|| if (s.starts_with('0') && s.len() > 1) || s.len() >= 13 { digits(s) } else { None })
}
fn segment(s: &str) -> Option<String> {
    let mut chunks: Vec<&str> = Vec::new();
    let mut start = 0;
    let mut prev = None;
    for (i, c) in s.char_indices() {
        let kind = if c.is_ascii_alphabetic() {
            0
        } else if c.is_ascii_digit() {
            1
        } else if c == '.' {
            2
        } else {
            return None;
        };
        if let Some(p) = prev {
            if p != kind {
                chunks.push(&s[start..i]);
                start = i;
            }
        }
        prev = Some(kind);
    }
    if s.is_empty() {
        return None;
    }
    chunks.push(&s[start..]);
    let mut types = Vec::new();
    let mut words = Vec::new();
    let mut i = 0;
    while i < chunks.len() {
        let ch = chunks[i];
        if ch.as_bytes()[0].is_ascii_alphabetic() {
            types.push(0);
            words.push(ch.to_string());
            i += 1;
        } else if ch.as_bytes()[0].is_ascii_digit() {
            types.push(1);
            let mut word = number(ch)?;
            i += 1;
            while i + 1 < chunks.len() && chunks[i] == "." && chunks[i + 1].as_bytes()[0].is_ascii_digit() {
                word.push_str(" kropka ");
                word.push_str(&number(chunks[i + 1])?);
                i += 2;
            }
            words.push(word);
        } else {
            return None;
        }
    }
    if types.len() > 3 || types.windows(2).any(|p| p[0] == p[1]) {
        return None;
    }
    Some(words.join(" "))
}
pub fn serial(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for &(k, v) in FIXED {
        if s == k {
            out.push((v.into(), -0.5));
        }
    }
    // A dot can be a numeric separator or a higher-cost segment delimiter.
    fn walk(s: &str, words: &mut Vec<String>, cost: f64, out: &mut Vec<(String, f64)>) {
        if let Some(w) = segment(s) {
            let mut all = words.clone();
            all.push(w);
            out.push((all.join(" "), cost));
        }
        for (i, c) in s.char_indices() {
            if !"-/:_.".contains(c) {
                continue;
            }
            let Some(w) = segment(&s[..i]) else {
                continue;
            };
            words.push(w);
            walk(&s[i + c.len_utf8()..], words, cost + if c == '.' { 0.1 } else { 0.0 }, out);
            words.pop();
        }
    }
    walk(s, &mut Vec::new(), 0.0, &mut out);
    out
}

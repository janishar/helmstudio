// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::cardinal::{Gender, digit, number};
pub(super) fn telephone(s: &str) -> Vec<(String, i32)> {
    let mut out = Vec::new();
    if let Some(rest) = s.strip_prefix('+') {
        if rest.chars().all(|c| c.is_ascii_digit() || " -.()".contains(c)) {
            let digits: Vec<_> = rest.bytes().filter(|x| x.is_ascii_digit()).collect();
            if (7..=15).contains(&digits.len()) {
                out.push((
                    format!(
                        "плюс {}",
                        digits.iter().map(|x| digit((*x - b'0') as usize)).collect::<Vec<_>>().join(" ")
                    ),
                    50,
                ));
            }
        }
    }
    let (plus, rest) = if let Some(rest) = s.strip_prefix('+') { ("плюс ", rest) } else { ("", s) };
    if rest.chars().all(|c| c.is_ascii_digit() || " -()".contains(c)) {
        let parts: Vec<_> = rest.split(|c| " -()".contains(c)).collect();
        if (3..=7).contains(&parts.len()) && parts.iter().all(|p| (1..=3).contains(&p.len())) {
            if let Some(words) = parts.iter().map(|p| number(p, Gender::Masculine, false)).collect::<Option<Vec<_>>>() {
                out.push((format!("{plus}{}", words.join(" ")), 10));
            }
        }
        if !plus.is_empty()
            && parts.len() == 4
            && (parts[0].len() == 1 || parts[0].len() == 3)
            && parts[1].len() == (if parts[0].len() == 1 { 3 } else { 2 })
            && parts[2].len() == 3
            && parts[3].len() == 4
        {
            let p = [parts[0], parts[1], parts[2], &parts[3][..2], &parts[3][2..]];
            if let Some(words) = p.iter().map(|p| number(p, Gender::Masculine, false)).collect::<Option<Vec<_>>>() {
                out.push((format!("плюс {}", words.join(" ")), 1));
            }
        }
        if !plus.is_empty()
            && parts.len() == 5
            && (parts[0].len() == 1 || parts[0].len() == 3)
            && parts[1].len() == (if parts[0].len() == 1 { 3 } else { 2 })
            && parts[2].len() == 3
            && parts[3].len() == 2
            && parts[4].len() == 2
        {
            if let Some(words) = parts.iter().map(|p| number(p, Gender::Masculine, false)).collect::<Option<Vec<_>>>() {
                out.push((format!("плюс {}", words.join(" ")), 1));
            }
        }
    }
    out
}

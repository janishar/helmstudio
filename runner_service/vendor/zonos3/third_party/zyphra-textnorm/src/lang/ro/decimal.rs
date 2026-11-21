// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::graph_utils::{Gender, number};

const DIGITS: [&str; 10] = ["zero", "unu", "doi", "trei", "patru", "cinci", "șase", "șapte", "opt", "nouă"];

fn fractional(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if s.len() >= 4 {
        return Some(s.bytes().map(|b| DIGITS[(b - b'0') as usize]).collect::<Vec<_>>().join(" "));
    }
    let zeros = s.bytes().take_while(|&b| b == b'0').count().min(s.len() - 1);
    let mut words = vec!["zero"; zeros].join(" ");
    if zeros > 0 {
        words.push(' ');
    }
    words.push_str(&number(&s[zeros..], Gender::Masculine)?);
    Some(words)
}

/// Decimal source graph. Both gender variants are returned for percentages.
pub fn decimal(input: &str) -> Vec<String> {
    let (negative, input) = if let Some(s) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        (true, s)
    } else {
        (false, input)
    };
    let (suffix, input) = if let Some(s) = input.strip_suffix('%') {
        (Some("la sută"), s.strip_suffix(' ').unwrap_or(s))
    } else if let Some(s) = input.strip_suffix('‰') {
        (Some("la mie"), s.strip_suffix(' ').unwrap_or(s))
    } else {
        (None, input)
    };
    // The decimal graph explicitly excludes inputs that are entirely a grouped integer.
    let separators: Vec<_> = input.match_indices(|c| matches!(c, '.' | ',' | ' ' | '\u{a0}')).collect();
    if !separators.is_empty() {
        let first = separators[0].1;
        let groups: Vec<_> = input.split(first).collect();
        if groups.len() > 1
            && !groups[0].starts_with('0')
            && (1..=3).contains(&groups[0].len())
            && groups.iter().all(|g| g.bytes().all(|b| b.is_ascii_digit()))
            && groups[1..].iter().all(|g| g.len() == 3)
        {
            return Vec::new();
        }
    }
    let mut out = Vec::new();
    for (idx, c) in input.char_indices() {
        if c != '.' && c != ',' {
            continue;
        }
        let Some(frac) = fractional(&input[idx + 1..]) else {
            continue;
        };
        for gender in
            if suffix.is_some() { &[Gender::Feminine, Gender::Masculine][..] } else { &[Gender::Masculine][..] }
        {
            let Some(integer) = number(&input[..idx], *gender) else {
                continue;
            };
            let mut result = format!("{}{} virgulă {}", if negative { "minus " } else { "" }, integer, frac);
            if let Some(s) = suffix {
                result.push(' ');
                result.push_str(s);
            }
            if !out.contains(&result) {
                out.push(result);
            }
        }
    }
    out
}

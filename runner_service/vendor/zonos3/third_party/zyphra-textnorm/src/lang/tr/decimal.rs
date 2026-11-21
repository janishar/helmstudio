//! Decimal number readings from `tr/taggers/decimal.py`.
use super::graph_utils::{digit_sequence, is_positive, positive};

fn fractional_count(s: &str) -> usize {
    if s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return 0;
    }
    match s.len() {
        1 => 1,
        2 if s.starts_with('0') => 1,
        3 if s.starts_with("00") => 1,
        2 | 3 => 2,
        _ => 1,
    }
}

fn bare_count(input: &str) -> usize {
    let s = input.strip_prefix('-').or_else(|| input.strip_prefix('−')).unwrap_or(input);
    if !s.as_bytes().last().is_some_and(u8::is_ascii_digit) {
        return 0;
    }
    s.bytes()
        .enumerate()
        .filter_map(|(i, c)| {
            if matches!(c, b',' | b'.') && is_positive(&s[..i]) { Some(fractional_count(&s[i + 1..])) } else { None }
        })
        .sum()
}

/// Count the exact decimal alternatives without spelling their numbers.
pub fn count(input: &str) -> usize {
    let mut n = bare_count(input);
    if let Some((prefix, quantity)) = input.rsplit_once(' ') {
        if matches!(
            quantity,
            "milyon" | "milyonluk" | "milyar" | "milyarlık" | "trilyon" | "katrilyon" | "bin" | "bilyon" | "biljoner"
        ) {
            n += usize::from(is_positive(prefix)) + bare_count(prefix);
        }
    }
    n
}

/// Fractional readings can be ambiguous: the source accepts both cardinal and
/// digit-by-digit readings for two or more digits.
pub fn fractional(s: &str) -> Vec<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return Vec::new();
    }
    let mut out = Vec::new();
    if s.len() <= 3 {
        let zeros = s.bytes().take_while(|b| *b == b'0').count();
        if zeros == s.len() {
            if let Some(v) = digit_sequence(s) {
                out.push(v);
            }
        } else if let Some(n) = positive(&s[zeros..]) {
            let mut p = vec!["sıfır"; zeros];
            p.push(&n);
            out.push(p.join(" "));
        }
    }
    if s.len() >= 2 {
        if let Some(v) = digit_sequence(s) {
            if !out.contains(&v) {
                out.push(v);
            }
        }
    }
    out
}

pub fn readings(input: &str) -> Vec<String> {
    let mut result = Vec::new();
    let quantities = [
        ("milyon", "milyon"),
        ("milyonluk", "milyonluk"),
        ("milyar", "milyar"),
        ("milyarlık", "milyarlık"),
        ("trilyon", "trilyon"),
        ("katrilyon", "katrilyon"),
        ("bin", "bin"),
        ("bilyon", "bilyon"),
        ("biljoner", "biljon"),
    ];
    for (written, spoken) in quantities {
        if let Some(prefix) = input.strip_suffix(written).and_then(|s| s.strip_suffix(' ')) {
            if let Some(n) = positive(prefix) {
                result.push(format!("{n} {spoken}"));
            }
            for n in bare(prefix) {
                result.push(format!("{n} {spoken}"));
            }
        }
    }
    result.extend(bare(input));
    result
}

fn bare(input: &str) -> Vec<String> {
    let (sign, s) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(rest) => ("eksi ", rest),
        None => ("", input),
    };
    let mut out = Vec::new();
    for (i, c) in s.char_indices() {
        if !matches!(c, ',' | '.') {
            continue;
        }
        let Some(integer) = positive(&s[..i]) else { continue };
        for fraction in fractional(&s[i + 1..]) {
            out.push(format!("{sign}{integer} virgül {fraction}"));
        }
    }
    out
}

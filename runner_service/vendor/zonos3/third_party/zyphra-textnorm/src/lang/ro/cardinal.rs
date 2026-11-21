// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::graph_utils::{Gender, number};

const DIGITS: [&str; 10] = ["zero", "unu", "doi", "trei", "patru", "cinci", "șase", "șapte", "opt", "nouă"];

/// Source cardinal branch outputs and their internal weights in micro-units.
pub fn cardinal(input: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    let masc = |s: &str| number(s, Gender::Masculine);
    if let Some(n) = masc(input) {
        out.push((n, 1_050_000));
    }
    if input.len() > 1 && input.starts_with('0') && input.bytes().all(|b| b.is_ascii_digit()) {
        out.push((input.bytes().map(|b| DIGITS[(b - b'0') as usize]).collect::<Vec<_>>().join(" "), 1_050_000));
    }
    for (prefix, spoken, weight) in
        [("-", "minus", 980_000), ("−", "minus", 980_000), ("~", "aproximativ", 950_000), ("±", "plus-minus", 950_000)]
    {
        if let Some(n) = input.strip_prefix(prefix).and_then(masc) {
            out.push((format!("{} {}", spoken, n), weight));
        }
    }
    for (i, op) in input.char_indices() {
        let left = &input[..i];
        let right = &input[i + op.len_utf8()..];
        if op == '^' {
            let base = masc(left).or_else(|| {
                if left.chars().count() == 1
                    && left.chars().all(|c| c.is_ascii_alphabetic() || "ăâîșțĂÂÎȘȚşţŞŢ".contains(c))
                {
                    Some(normalize_alpha(left))
                } else {
                    None
                }
            });
            if let Some(base) = base {
                if right == "2" {
                    out.push((format!("{} la pătrat", base), 900_000));
                }
                if right == "3" {
                    out.push((format!("{} la cub", base), 900_000));
                }
                if let Some(exp) = number(right, Gender::Feminine) {
                    out.push((format!("{} la puterea {}", base, exp), 900_000));
                }
            }
        }
        if op == ':' {
            if let (Some(a), Some(b)) = (masc(left), masc(right)) {
                out.push((format!("{} la {}", a, b), 900_000));
            }
        }
        if matches!(op, '-' | '–' | '—' | '−') {
            if let (Some(a), Some(b)) =
                (masc(left.strip_suffix(' ').unwrap_or(left)), masc(right.strip_prefix(' ').unwrap_or(right)))
            {
                out.push((format!("{} la {}", a, b), 930_000));
            }
        }
        let spoken = match op {
            '+' => Some("plus"),
            '-' | '−' => Some("minus"),
            '×' | '*' => Some("ori"),
            '÷' | '/' => Some("împărțit la"),
            '=' => Some("egal cu"),
            _ => None,
        };
        if let Some(spoken) = spoken {
            if let (Some(a), Some(b)) =
                (masc(left.strip_suffix(' ').unwrap_or(left)), masc(right.strip_prefix(' ').unwrap_or(right)))
            {
                let weight = if op == '-' && left.ends_with(' ') && right.starts_with(' ') { 920_000 } else { 950_000 };
                out.push((format!("{} {} {}", a, spoken, b), weight));
            }
        }
    }
    let is_alpha = |c: char| c.is_ascii_alphabetic() || "ăâîșțĂÂÎȘȚşţŞŢ".contains(c);
    let alpha = |s: &str| !s.is_empty() && s.chars().all(is_alpha);
    if let Some((i, _)) = input.char_indices().find(|(_, c)| !is_alpha(*c)) {
        let (a, b) = input.split_at(i);
        if alpha(a) {
            if let Some(n) = masc(b) {
                out.push((format!("{} {}", normalize_alpha(a), n), 1_100_000));
            }
        }
    }
    if let Some((i, _)) = input.char_indices().find(|(_, c)| is_alpha(*c)) {
        let (a, b) = input.split_at(i);
        if alpha(b) {
            if let Some(n) = masc(a) {
                out.push((format!("{} {}", n, normalize_alpha(b)), 1_100_000));
            }
        }
    }
    if let Some((a, rest)) = input.split_once('-') {
        if let Some((b, c)) = rest.split_once('-') {
            if alpha(a) && alpha(c) {
                if let Some(n) = masc(b) {
                    out.push((
                        format!("{} liniuță {} liniuță {}", normalize_alpha(a), n, normalize_alpha(c)),
                        1_100_000,
                    ));
                }
            }
        }
    }
    if let Some(n) = input.strip_prefix("COVID-").and_then(masc) {
        out.push((format!("COVID {}", n), 1_100_000));
    }
    let (prefix, version) = if let Some(v) = input.strip_prefix('v') {
        ("v ", v)
    } else if let Some(v) = input.strip_prefix('V') {
        ("V ", v)
    } else {
        ("", input)
    };
    if version.contains('.') {
        let mut pieces = version.split('.');
        let mut words = String::new();
        let mut count = 0;
        let mut valid = true;
        while let Some(piece) = pieces.next() {
            if !(1..=4).contains(&piece.len()) {
                valid = false;
                break;
            }
            let Some(word) = masc(piece) else {
                valid = false;
                break;
            };
            if count != 0 {
                words.push_str(" punct ");
            }
            words.push_str(&word);
            count += 1;
        }
        if valid && (2..=5).contains(&count) {
            out.push((format!("{}{}", prefix, words), 1_100_000));
        }
    }
    out
}

fn normalize_alpha(s: &str) -> String {
    s.chars()
        .map(|c| match c {
            'ş' => 'ș',
            'ţ' => 'ț',
            'Ş' => 'Ș',
            'Ţ' => 'Ț',
            _ => c,
        })
        .collect()
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Decimal core from uk/taggers/decimal.py.

use super::cardinal::{Gender, number};

fn singular(s: &str) -> bool {
    let b = s.as_bytes();
    b.last() == Some(&b'1') && (b.len() == 1 || b[b.len() - 2] != b'1')
}

pub(super) fn core(input: &str) -> Option<String> {
    let (negative, unsigned) = if let Some(s) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        (true, s)
    } else {
        (false, input)
    };
    let (whole, frac) = unsigned.split_once([',', '.'])?;
    if frac.is_empty() || frac.len() > 6 || !frac.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let integral = number(whole, Gender::Feminine, false)?;
    let nonzero = frac.trim_start_matches('0');
    let fraction = number(if nonzero.is_empty() { "0" } else { nonzero }, Gender::Feminine, false)?;
    let denominations = [
        ("десята", "десятих"),
        ("сота", "сотих"),
        ("тисячна", "тисячних"),
        ("десятитисячна", "десятитисячних"),
        ("стотисячна", "стотисячних"),
        ("мільйонна", "мільйонних"),
    ];
    let denom = denominations[frac.len() - 1];
    Some(format!(
        "{}{} {} {} {}",
        if negative { "мінус " } else { "" },
        integral,
        if singular(whole) { "ціла" } else { "цілих" },
        fraction,
        if singular(frac) { denom.0 } else { denom.1 }
    ))
}

pub(super) fn decimal(s: &str) -> Vec<(String, i32)> {
    let mut out = Vec::new();
    if let Some(w) = core(s) {
        out.push((w, 10));
    }
    for (suffix, spoken, weight) in [
        ("%", "відсотка", 8),
        ("л", "літра", 7),
        ("л.", "літра", 7),
        ("млн", "мільйона", 6),
        ("млн.", "мільйона", 6),
        ("млрд", "мільярда", 6),
        ("млрд.", "мільярда", 6),
    ] {
        if let Some(v) = s.strip_suffix(suffix) {
            let v = v.strip_suffix(' ').unwrap_or(v);
            if let Some(w) = core(v) {
                out.push((format!("{w} {spoken}"), weight));
            }
        }
    }
    for (curr, spoken) in [("руб.", "рублів"), ("руб", "рублів"), ("грн.", "гривень"), ("грн", "гривень")]
    {
        if let Some(v) = s.strip_suffix(curr) {
            let v = v.strip_suffix(' ').unwrap_or(v);
            for (scale, word) in
                [("млн.", "мільйона"), ("млн", "мільйона"), ("млрд.", "мільярда"), ("млрд", "мільярда")]
            {
                if let Some(n) = v.strip_suffix(scale) {
                    let n = n.strip_suffix(' ').unwrap_or(n);
                    if let Some(w) = core(n) {
                        out.push((format!("{w} {word} {spoken}"), 5));
                    }
                }
            }
        }
    }
    out
}

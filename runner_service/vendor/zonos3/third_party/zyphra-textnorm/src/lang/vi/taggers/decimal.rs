// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese decimal classifier: commas, points, quantities, abbreviations, compound expansions.

use super::super::graph_utils::{
    Digits, SpokenFraction, SpokenNumber, WEIGHT_DECIMAL, cardinal_cost, digit_word, dot_cost, scan_dot_grouped_numbers,
};
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Decimal;

const QUANTITIES: [(&str, &str); 8] = [
    ("nghìn tỷ", "nghìn tỷ"),
    ("triệu tỷ", "triệu tỷ"),
    ("tỷ tỷ", "tỷ tỷ"),
    ("nghìn", "nghìn"),
    ("triệu", "triệu"),
    ("trăm", "trăm"),
    ("linh", "linh"),
    ("tỷ", "tỷ"),
];

const QUANTITY_ABBRS: [(&str, &str); 7] =
    [("tr", "triệu"), ("TR", "triệu"), ("Tr", "triệu"), ("k", "nghìn"), ("K", "nghìn"), ("t", "tỷ"), ("T", "tỷ")];

const SAFE_DECIMAL_ABBRS: [(&str, &str); 3] = [("tr", "triệu"), ("TR", "triệu"), ("Tr", "triệu")];

impl Decimal {
    pub(super) fn quantity_suffixes(text: &[char], pos: usize) -> impl Iterator<Item = (usize, &'static str)> + '_ {
        let pos = pos + usize::from(text.get(pos) == Some(&' '));
        let indices: &[usize] = match text.get(pos) {
            Some('n') => &[0, 3],
            Some('t') => &[1, 2, 4, 5, 7],
            Some('l') => &[6],
            _ => &[],
        };
        indices.iter().filter_map(move |&index| {
            let (written, spoken) = QUANTITIES[index];
            let end = pos + written.chars().count();
            text.get(pos..end).filter(|part| part.iter().copied().eq(written.chars())).map(|_| (end, spoken))
        })
    }

    /// Scan decimal patterns without negative sign, calling `f(end, tag_body, internal_cost)`.
    pub fn scan_positive_spans(text: &[char], start: usize, f: impl FnMut(usize, String, Cost)) {
        Self::scan_positive(text, start, true, f);
    }

    pub(super) fn scan_positive(text: &[char], start: usize, render: bool, f: impl FnMut(usize, String, Cost)) {
        Self::scan_selected(text, start, render, None, f);
    }

    pub(super) fn scan_selected(
        text: &[char],
        start: usize,
        render: bool,
        target: Option<(usize, Cost)>,
        mut f: impl FnMut(usize, String, Cost),
    ) {
        let found = std::cell::Cell::new(false);
        macro_rules! emit {
            ($end:expr, $body:expr, $cost:expr) => {{
                let end = $end;
                let cost = $cost;
                if !found.get() && target.is_none_or(|wanted| wanted == (end, cost)) {
                    f(end, $body, cost);
                    if target.is_some() {
                        found.set(true);
                    }
                }
            }};
        }
        macro_rules! format {
            ($($arg:tt)*) => {
                if render { std::format!($($arg)*) } else { String::new() }
            };
        }
        if start >= text.len() {
            return;
        }

        // 1. Basic comma decimal: e.g. "12,5", ",5", "12.345,67", "12,5 tỷ"
        // Can start with ',' (omitted integer_part) or with integer_part + ','
        if text[start] == ',' && start + 1 < text.len() && text[start + 1].is_ascii_digit() {
            let frac_start = start + 1;
            let mut i = start + 1;
            while i < text.len() && text[i].is_ascii_digit() {
                i += 1;
                let frac_str = SpokenFraction(&text[frac_start..i]);

                // without quantity
                emit!(i, format!("fractional_part: \"{}\"", frac_str), 0);

                // with quantity
                for (q_end, q_name) in Self::quantity_suffixes(text, i) {
                    emit!(q_end, format!("fractional_part: \"{}\" quantity: \"{}\"", frac_str, q_name), 0);
                }

                // with safe abbr (e.g. ,5tr)
                for &(abbr, q_name) in &SAFE_DECIMAL_ABBRS {
                    let end = i + abbr.len();
                    if text.get(i..end).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                        emit!(end, format!("fractional_part: \"{}\" quantity: \"{}\"", frac_str, q_name), 0);
                    }
                }
            }
        }

        // Numbers with integer part
        if text[start].is_ascii_digit() {
            scan_dot_grouped_numbers(text, start, false, |end, digits| {
                let Some(c) = dot_cost(digits) else { return };
                let integer = SpokenNumber { digits, grouped: true };
                for (q_end, quantity) in Self::quantity_suffixes(text, end) {
                    emit!(q_end, format!("integer_part: \"{}\" quantity: \"{}\"", integer, quantity), c);
                }
                let Some(&sep) = text.get(end) else { return };
                if sep != ',' && sep != '.' {
                    return;
                }
                let frac_start = end + 1;
                let mut p = end + 1;
                while text.get(p).is_some_and(char::is_ascii_digit) {
                    p += 1;
                    if sep == '.' && p - frac_start == 3 {
                        continue;
                    }
                    let fractional = SpokenFraction(&text[frac_start..p]);
                    emit!(p, format!("integer_part: \"{}\" fractional_part: \"{}\"", integer, fractional), c);
                    for (q_end, quantity) in Self::quantity_suffixes(text, p) {
                        emit!(
                            q_end,
                            format!(
                                "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                integer, fractional, quantity
                            ),
                            c
                        );
                    }
                    if sep == ',' {
                        for &(abbr, quantity) in &SAFE_DECIMAL_ABBRS {
                            let q_end = p + abbr.chars().count();
                            if text.get(p..q_end).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                                emit!(
                                    q_end,
                                    format!(
                                        "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                        integer, fractional, quantity
                                    ),
                                    c
                                );
                            }
                        }
                    }
                }
            });
            // Check dot-grouped integer first (e.g. 12.345,67)
            scan_dot_grouped_numbers(text, start, true, |dot_int_end, int_digits| {
                if let Some(extra_cost) = cardinal_cost(int_digits) {
                    let int_str = SpokenNumber { digits: int_digits, grouped: false };
                    if dot_int_end < text.len() && text[dot_int_end] == ',' {
                        let frac_start = dot_int_end + 1;
                        let mut i = dot_int_end + 1;
                        while i < text.len() && text[i].is_ascii_digit() {
                            i += 1;
                            let frac_str = SpokenFraction(&text[frac_start..i]);

                            emit!(
                                i,
                                format!("integer_part: \"{}\" fractional_part: \"{}\"", int_str, frac_str),
                                extra_cost
                            );

                            for (q_end, q_name) in Self::quantity_suffixes(text, i) {
                                emit!(
                                    q_end,
                                    format!(
                                        "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                        int_str, frac_str, q_name
                                    ),
                                    extra_cost
                                );
                            }
                        }
                    }
                }
            });

            // Plain integer part
            let mut int_digits = Digits::new();
            let mut int_end = start;
            while int_end < text.len() && text[int_end].is_ascii_digit() && int_digits.len() < 21 {
                int_digits.push(text[int_end] as u8);
                int_end += 1;
                // An integer needs a separator, quantity, or abbreviation.
                if !matches!(text.get(int_end), Some(',' | '.' | 't' | 'T' | 'k' | 'K'))
                    && Self::quantity_suffixes(text, int_end).next().is_none()
                {
                    continue;
                }

                let len = int_digits.len();
                let Some(int_cost) = cardinal_cost(&int_digits) else {
                    continue;
                };
                let int_str = SpokenNumber { digits: &int_digits, grouped: false };

                // Pattern 3: Integer with quantity word (e.g. "100 triệu", "100triệu")
                for (q_end, q_name) in Self::quantity_suffixes(text, int_end) {
                    emit!(q_end, format!("integer_part: \"{}\" quantity: \"{}\"", int_str, q_name), int_cost);
                }

                // Pattern 4: Standard abbreviations (1..3 digits + abbr: 1k, 100tr, 2t)
                if len <= 3 {
                    for &(abbr, full_name) in &QUANTITY_ABBRS {
                        let end = int_end + abbr.len();
                        if text.get(int_end..end).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                            emit!(end, format!("integer_part: \"{}\" quantity: \"{}\"", int_str, full_name), int_cost);
                        }
                    }

                    // Pattern 6: Compound abbreviations (1tr2, 2t3)
                    if int_end + 2 <= text.len() {
                        let c1 = text[int_end];
                        let c2 = text[int_end + 1];
                        if (c1 == 't' && c2 == 'r') && int_end + 2 < text.len() && text[int_end + 2].is_ascii_digit() {
                            let d = text[int_end + 2] as u8;
                            let end = int_end + 3;
                            let d_word = digit_word(d);
                            emit!(end, format!("integer_part: \"{} triệu {} trăm nghìn\"", int_str, d_word), int_cost);
                        } else if c1 == 't' && c2.is_ascii_digit() {
                            let d = c2 as u8;
                            let end = int_end + 2;
                            let d_word = digit_word(d);
                            emit!(end, format!("integer_part: \"{} tỷ {} trăm triệu\"", int_str, d_word), int_cost);
                        }
                    }
                }

                // Pattern 1: Comma decimal: int + "," + frac
                if int_end < text.len() && text[int_end] == ',' {
                    let frac_start = int_end + 1;
                    let mut i = int_end + 1;
                    while i < text.len() && text[i].is_ascii_digit() {
                        i += 1;
                        let frac_str = SpokenFraction(&text[frac_start..i]);

                        emit!(i, format!("integer_part: \"{}\" fractional_part: \"{}\"", int_str, frac_str), int_cost);

                        for (q_end, q_name) in Self::quantity_suffixes(text, i) {
                            emit!(
                                q_end,
                                format!(
                                    "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                    int_str, frac_str, q_name
                                ),
                                int_cost
                            );
                        }

                        // safe decimal abbrs (2,5tr)
                        for &(abbr, q_name) in &SAFE_DECIMAL_ABBRS {
                            let end = i + abbr.len();
                            if text.get(i..end).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                                emit!(
                                    end,
                                    format!(
                                        "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                        int_str, frac_str, q_name
                                    ),
                                    int_cost
                                );
                            }
                        }
                    }
                }

                // Pattern 1b: Point decimal: int + "." + frac (where frac is 1, 2, or 4+ digits, NOT 3)
                if int_end < text.len() && text[int_end] == '.' {
                    let frac_start = int_end + 1;
                    let mut i = int_end + 1;
                    while i < text.len() && text[i].is_ascii_digit() {
                        i += 1;
                        let frac_len = i - frac_start;
                        if frac_len == 3 {
                            continue; // exactly 3 digits is reserved for thousands group!
                        }
                        let frac_str = SpokenFraction(&text[frac_start..i]);

                        emit!(i, format!("integer_part: \"{}\" fractional_part: \"{}\"", int_str, frac_str), int_cost);

                        for (q_end, q_name) in Self::quantity_suffixes(text, i) {
                            emit!(
                                q_end,
                                format!(
                                    "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                    int_str, frac_str, q_name
                                ),
                                int_cost
                            );
                        }
                    }
                }
            }
        }
    }
}

impl Classifier for Decimal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() {
            return;
        }

        let num_start = if text[start] == '-'
            && start + 1 < text.len()
            && (text[start + 1].is_ascii_digit() || text[start + 1] == ',')
        {
            start + 1
        } else {
            start
        };
        Self::scan_positive(text, num_start, false, |end, _, internal_cost| {
            out.push(Candidate { end, cost: WEIGHT_DECIMAL + internal_cost, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let negative = text[start] == '-';
        let num_start = start + usize::from(negative);
        Self::scan_selected(
            text,
            num_start,
            true,
            Some((candidate.end, candidate.cost - WEIGHT_DECIMAL)),
            |end, body, internal_cost| {
                if candidate.tag.is_empty() && end == candidate.end && WEIGHT_DECIMAL + internal_cost == candidate.cost
                {
                    candidate.tag = if negative {
                        format!("decimal {{ negative: \"true\" {body} }}")
                    } else {
                        format!("decimal {{ {body} }}")
                    };
                }
            },
        );
    }
}

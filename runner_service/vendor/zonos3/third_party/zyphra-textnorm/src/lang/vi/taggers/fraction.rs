// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese fraction classifier: slash fractions, Unicode vulgar fractions, mixed fractions.

use super::super::graph_utils::{
    Digits, SpokenNumber, WEIGHT_FRACTION, cardinal_cost, dot_cost, scan_dot_grouped_numbers,
};
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Fraction;

pub const UNICODE_FRACTIONS: [(char, &str, &str); 18] = [
    ('½', "một", "hai"),
    ('¼', "một", "tư"),
    ('¾', "ba", "tư"),
    ('⅓', "một", "ba"),
    ('⅔', "hai", "ba"),
    ('⅕', "một", "năm"),
    ('⅖', "hai", "năm"),
    ('⅗', "ba", "năm"),
    ('⅘', "bốn", "năm"),
    ('⅙', "một", "sáu"),
    ('⅚', "năm", "sáu"),
    ('⅛', "một", "tám"),
    ('⅜', "ba", "tám"),
    ('⅝', "năm", "tám"),
    ('⅞', "bảy", "tám"),
    ('⅐', "một", "bảy"),
    ('⅑', "một", "chín"),
    ('⅒', "một", "mười"),
];

enum Denominator<'a> {
    Special,
    Number(&'a [u8]),
}
impl std::fmt::Display for Denominator<'_> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Special => f.write_str("tư"),
            Self::Number(digits) => write!(f, "{}", SpokenNumber { digits, grouped: false }),
        }
    }
}

impl Fraction {
    fn scan_denominator<'a>(digits: &'a [u8], mut f: impl FnMut(Denominator<'a>, Cost)) {
        if digits == b"4" {
            f(Denominator::Special, 0);
        }
        if let Some(c) = cardinal_cost(digits) {
            f(Denominator::Number(digits), c);
        }
    }

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
            ($($arg:tt)*) => { if render { std::format!($($arg)*) } else { String::new() } };
        }
        if start >= text.len() {
            return;
        }
        // Every digit-led path must reach a slash, dot-group, or a mixed
        // fraction continuation after at most the cardinal graph's 21 digits.
        if text[start].is_ascii_digit() {
            let mut end = start;
            while end < text.len() && end - start < 21 && text[end].is_ascii_digit() {
                end += 1;
            }
            if !text.get(end).is_some_and(|&c| {
                matches!(c, '/' | '⁄' | '.')
                    || (c == ' '
                        && text.get(end + 1).is_some_and(|&next| {
                            next.is_ascii_digit()
                                || (!next.is_ascii() && UNICODE_FRACTIONS.iter().any(|&(ch, _, _)| ch == next))
                        }))
                    || (!c.is_ascii() && UNICODE_FRACTIONS.iter().any(|&(ch, _, _)| ch == c))
            }) {
                return;
            }
        }

        // 1. Vulgar fraction alone
        if !text[start].is_ascii() {
            for &(ch, num_str, den_str) in &UNICODE_FRACTIONS {
                if text[start] == ch {
                    emit!(start + 1, format!("numerator: \"{}\" denominator: \"{}\"", num_str, den_str), 0);
                }
            }
        }

        // 2. Simple slash fraction alone: num / den
        if text[start].is_ascii_digit() {
            let mut num_digits = Digits::new();
            let mut i = start;
            while i < text.len() && text[i].is_ascii_digit() && num_digits.len() < 21 {
                num_digits.push(text[i] as u8);
                i += 1;
                if !matches!(text.get(i), Some('/' | '⁄')) {
                    continue;
                }

                let Some(num_cost) = cardinal_cost(&num_digits) else { continue };
                let num_str = SpokenNumber { digits: &num_digits, grouped: false };

                if i < text.len() && (text[i] == '/' || text[i] == '⁄') {
                    let slash_pos = i;
                    let mut den_digits = Digits::new();
                    let mut j = slash_pos + 1;
                    while j < text.len() && text[j].is_ascii_digit() && den_digits.len() < 21 {
                        den_digits.push(text[j] as u8);
                        j += 1;

                        Self::scan_denominator(&den_digits, |den_str, den_cost| {
                            emit!(
                                j,
                                format!("numerator: \"{}\" denominator: \"{}\"", num_str, den_str),
                                num_cost + den_cost
                            );
                        });
                    }
                }
            }

            scan_dot_grouped_numbers(text, start, false, |int_end, digits| {
                let has_space = text.get(int_end) == Some(&' ');
                let frac_start = int_end + usize::from(has_space);
                if !text.get(frac_start).is_some_and(|&c| {
                    UNICODE_FRACTIONS.iter().any(|&(ch, _, _)| ch == c) || (has_space && c.is_ascii_digit())
                }) {
                    return;
                }
                if let Some(int_cost) = dot_cost(digits) {
                    let integer = SpokenNumber { digits, grouped: true };
                    let has_space = text.get(int_end) == Some(&' ');
                    let frac_start = int_end + usize::from(has_space);
                    if frac_start < text.len() {
                        for &(ch, num, den) in &UNICODE_FRACTIONS {
                            if text[frac_start] == ch {
                                emit!(
                                    frac_start + 1,
                                    format!(
                                        "integer_part: \"{}\" numerator: \"{}\" denominator: \"{}\"",
                                        integer, num, den
                                    ),
                                    int_cost
                                );
                            }
                        }
                        if has_space && text[frac_start].is_ascii_digit() {
                            let mut num = Digits::new();
                            let mut p = frac_start;
                            while p < text.len() && text[p].is_ascii_digit() && num.len() < 21 {
                                num.push(text[p] as u8);
                                p += 1;
                                if let Some(num_cost) = cardinal_cost(&num) {
                                    let numerator = SpokenNumber { digits: &num, grouped: false };
                                    if matches!(text.get(p), Some(&'/') | Some(&'⁄')) {
                                        let mut den = Digits::new();
                                        let mut q = p + 1;
                                        while q < text.len() && text[q].is_ascii_digit() && den.len() < 21 {
                                            den.push(text[q] as u8);
                                            q += 1;
                                            Self::scan_denominator(&den, |denominator, den_cost| {
                                                emit!(
                                                    q,
                                                    format!(
                                                        "integer_part: \"{}\" numerator: \"{}\" denominator: \"{}\"",
                                                        integer, numerator, denominator
                                                    ),
                                                    int_cost + num_cost + den_cost
                                                );
                                            });
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            });
            // 3. Mixed fraction: integer_part + optional_space + (simple | vulgar)
            let mut int_digits = Digits::new();
            let mut int_end = start;
            while int_end < text.len() && text[int_end].is_ascii_digit() && int_digits.len() < 21 {
                int_digits.push(text[int_end] as u8);
                int_end += 1;
                let next = int_end + usize::from(text.get(int_end) == Some(&' '));
                if !text.get(next).is_some_and(|&c| {
                    UNICODE_FRACTIONS.iter().any(|&(ch, _, _)| ch == c) || (next > int_end && c.is_ascii_digit())
                }) {
                    continue;
                }

                let Some(int_cost) = cardinal_cost(&int_digits) else { continue };
                let int_str = SpokenNumber { digits: &int_digits, grouped: false };

                // Check for vulgar fraction right after or after space
                let mut frac_start = int_end;
                if frac_start < text.len() && text[frac_start] == ' ' {
                    frac_start += 1;
                }

                if frac_start < text.len() {
                    for &(ch, num_str, den_str) in &UNICODE_FRACTIONS {
                        if text[frac_start] == ch {
                            emit!(
                                frac_start + 1,
                                format!(
                                    "integer_part: \"{}\" numerator: \"{}\" denominator: \"{}\"",
                                    int_str, num_str, den_str
                                ),
                                int_cost
                            );
                        }
                    }

                    // Check for slash fraction after space (e.g. "23 1/5")
                    // Note: source mixed fraction requires space for slash fractions
                    if frac_start > int_end && text[frac_start].is_ascii_digit() {
                        let mut num_digits = Digits::new();
                        let mut k = frac_start;
                        while k < text.len() && text[k].is_ascii_digit() && num_digits.len() < 21 {
                            num_digits.push(text[k] as u8);
                            k += 1;

                            let Some(num_cost) = cardinal_cost(&num_digits) else { continue };
                            let num_str = SpokenNumber { digits: &num_digits, grouped: false };

                            if k < text.len() && (text[k] == '/' || text[k] == '⁄') {
                                let mut den_digits = Digits::new();
                                let mut m = k + 1;
                                while m < text.len() && text[m].is_ascii_digit() && den_digits.len() < 21 {
                                    den_digits.push(text[m] as u8);
                                    m += 1;

                                    Self::scan_denominator(&den_digits, |den_str, den_cost| {
                                        emit!(
                                            m,
                                            format!(
                                                "integer_part: \"{}\" numerator: \"{}\" denominator: \"{}\"",
                                                int_str, num_str, den_str
                                            ),
                                            int_cost + num_cost + den_cost
                                        );
                                    });
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

impl Classifier for Fraction {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() {
            return;
        }

        let num_start = if text[start] == '-'
            && start + 1 < text.len()
            && (text[start + 1].is_ascii_digit() || UNICODE_FRACTIONS.iter().any(|&(ch, _, _)| text[start + 1] == ch))
        {
            start + 1
        } else {
            start
        };
        Self::scan_positive(text, num_start, false, |end, _, internal_cost| {
            out.push(Candidate { end, cost: WEIGHT_FRACTION + internal_cost, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let negative = text[start] == '-';
        let num_start = start + usize::from(negative);
        Self::scan_selected(
            text,
            num_start,
            true,
            Some((candidate.end, candidate.cost - WEIGHT_FRACTION)),
            |end, body, internal_cost| {
                if candidate.tag.is_empty() && end == candidate.end && WEIGHT_FRACTION + internal_cost == candidate.cost
                {
                    candidate.tag = if negative {
                        format!("fraction {{ negative: \"true\" {body} }}")
                    } else {
                        format!("fraction {{ {body} }}")
                    };
                }
            },
        );
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese telephone classifier.

use super::super::graph_utils::{WEIGHT_TELEPHONE, digit_word};
use crate::lattice::{Candidate, Classifier};

pub struct Telephone;
crate::labels!(COUNTRY_CODES_LABELS, "vi/telephone/country_code.tsv");

fn separator(c: char) -> bool {
    matches!(c, ' ' | '-' | '.')
}

fn spell(digits: &[char]) -> String {
    let mut out = String::new();
    for &d in digits {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(digit_word(d as u8));
    }
    out
}

fn grouped_plain(digits: &[char], a: usize, b: usize) -> String {
    format!("{}, {}, {}", spell(&digits[..a]), spell(&digits[a..a + b]), spell(&digits[a + b..]))
}

fn scan_groups(
    text: &[char],
    pos: usize,
    min_first: usize,
    max_first: usize,
    min_rest: usize,
    max_rest: usize,
    min_groups: usize,
    max_groups: usize,
    min_digits: usize,
    max_digits: usize,
    mut f: impl FnMut(usize, String),
) {
    fn visit(
        text: &[char],
        pos: usize,
        min: usize,
        max: usize,
        rest_min: usize,
        rest_max: usize,
        groups: usize,
        min_groups: usize,
        max_groups: usize,
        total: usize,
        min_digits: usize,
        max_digits: usize,
        before: &str,
        f: &mut dyn FnMut(usize, String),
    ) {
        let mut end = pos;
        for n in 1..=max {
            if !text.get(end).is_some_and(char::is_ascii_digit) {
                break;
            }
            end += 1;
            if n < min || total + n > max_digits {
                continue;
            }
            let mut value = String::new();
            value.push_str(before);
            if groups != 0 {
                value.push_str(", ");
            }
            value.push_str(&spell(&text[pos..end]));
            let count = groups + 1;
            if count >= min_groups && total + n >= min_digits {
                f(end, value.clone());
            }
            if count < max_groups && text.get(end).is_some_and(|&c| separator(c)) {
                visit(
                    text,
                    end + 1,
                    rest_min,
                    rest_max,
                    rest_min,
                    rest_max,
                    count,
                    min_groups,
                    max_groups,
                    total + n,
                    min_digits,
                    max_digits,
                    &value,
                    f,
                );
            }
        }
    }
    visit(
        text, pos, min_first, max_first, min_rest, max_rest, 0, min_groups, max_groups, 0, min_digits, max_digits, "",
        &mut f,
    );
}

impl Telephone {
    pub fn scan_spans(text: &[char], start: usize, mut f: impl FnMut(usize, String)) {
        if start >= text.len() {
            return;
        }
        if text[start] == '0' {
            scan_groups(text, start, 2, 4, 2, 4, 2, 5, 9, 11, |end, value| {
                f(end, format!("number_part: \"{}\"", value));
            });
            for (length, a, b) in [(10, 4, 3), (11, 3, 4)] {
                let end = start + length;
                if text.get(start..end).is_some_and(|s| s.iter().all(char::is_ascii_digit)) {
                    f(end, format!("number_part: \"{}\"", grouped_plain(&text[start..end], a, b)));
                }
            }
        }
        if text[start] == '(' {
            let area_start = start + 1;
            for area_len in 2..=4 {
                let close = area_start + area_len;
                if !text.get(area_start..close).is_some_and(|s| s.iter().all(char::is_ascii_digit))
                    || text.get(close) != Some(&')')
                {
                    continue;
                }
                let mut first = close + 1;
                if text.get(first).is_some_and(|&c| separator(c)) {
                    first += 1;
                }
                for first_len in 3..=4 {
                    let sep = first + first_len;
                    if !text.get(first..sep).is_some_and(|s| s.iter().all(char::is_ascii_digit))
                        || !text.get(sep).is_some_and(|&c| separator(c))
                    {
                        continue;
                    }
                    for second_len in 3..=4 {
                        let end = sep + 1 + second_len;
                        if text.get(sep + 1..end).is_some_and(|s| s.iter().all(char::is_ascii_digit)) {
                            f(
                                end,
                                format!(
                                    "number_part: \"{}, {}, {}\"",
                                    spell(&text[area_start..close]),
                                    spell(&text[first..sep]),
                                    spell(&text[sep + 1..end])
                                ),
                            );
                        }
                    }
                }
            }
        }
        if text[start] != '+' {
            return;
        }
        let code_start = start + 1;
        for code_len in 1..=3 {
            let code_end = code_start + code_len;
            let Some(code_chars) = text.get(code_start..code_end) else { break };
            if !code_chars.iter().all(char::is_ascii_digit) {
                break;
            }
            let code: String = code_chars.iter().collect();
            let known = COUNTRY_CODES_LABELS
                .iter()
                .find_map(|row| (row.first().is_some_and(|s| s == &code)).then(|| row.get(1)).flatten());
            let code_spoken = known.cloned().unwrap_or_else(|| spell(code_chars));
            if text.get(code_end).is_some_and(|&c| separator(c)) {
                let rest = code_end + 1;
                scan_groups(text, rest, 1, 4, 1, 4, 2, 6, 6, 12, |end, value| {
                    f(end, format!("country_code: \"cộng {}\" number_part: \"{}\"", code_spoken, value));
                });
                let mut end = rest;
                for length in 1..=12 {
                    if !text.get(end).is_some_and(char::is_ascii_digit) {
                        break;
                    }
                    end += 1;
                    if length >= 6 {
                        f(
                            end,
                            format!(
                                "country_code: \"cộng {}\" number_part: \"{}\"",
                                code_spoken,
                                spell(&text[rest..end])
                            ),
                        );
                    }
                }
            }
            if known.is_some() {
                for (length, a, b) in [(9, 3, 3), (10, 2, 4)] {
                    let end = code_end + length;
                    if text.get(code_end..end).is_some_and(|s| s.iter().all(char::is_ascii_digit)) {
                        f(
                            end,
                            format!(
                                "country_code: \"cộng {}\" number_part: \"{}\"",
                                code_spoken,
                                grouped_plain(&text[code_end..end], a, b)
                            ),
                        );
                    }
                }
            }
        }
    }
}

impl Classifier for Telephone {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan_spans(text, start, |end, body| {
            out.push(Candidate { end, cost: WEIGHT_TELEPHONE, rank: 0, tag: format!("telephone {{ {} }}", body) });
        });
    }
}

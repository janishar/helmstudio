// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese measure classifier: numbers + metric/base/special/compound units.

use super::super::graph_utils::{SpokenNumber, WEIGHT_MEASURE, cardinal_cost};
use super::decimal::Decimal;
use super::fraction::Fraction;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost};

pub struct Measure;

crate::labels!(PREFIXES_LABELS, "vi/measure/prefixes.tsv");
crate::labels!(BASE_UNITS_LABELS, "vi/measure/base_units.tsv");
crate::labels!(SPECIAL_UNITS_LABELS, "vi/measure/measurements_minimal.tsv");

struct UnitIndex {
    ascii: [Vec<&'static Vec<String>>; 128],
    other: std::collections::HashMap<char, Vec<&'static Vec<String>>>,
}
fn index_rows(rows: &'static [Vec<String>]) -> UnitIndex {
    let mut index = UnitIndex { ascii: std::array::from_fn(|_| Vec::new()), other: std::collections::HashMap::new() };
    for row in rows {
        if let Some(first) = row.first().and_then(|s| s.chars().next()) {
            if first.is_ascii() {
                index.ascii[first as usize].push(row);
            } else {
                index.other.entry(first).or_default().push(row);
            }
        }
    }
    index
}
static SPECIAL_INDEX: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| index_rows(&SPECIAL_UNITS_LABELS));
static PREFIX_INDEX: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| index_rows(&PREFIXES_LABELS));
static BASE_INDEX: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| index_rows(&BASE_UNITS_LABELS));
static UNIT_INITIALS: std::sync::LazyLock<([bool; 128], std::collections::HashSet<char>)> =
    std::sync::LazyLock::new(|| {
        let mut ascii = [false; 128];
        let mut other = std::collections::HashSet::new();
        for rows in [&*SPECIAL_UNITS_LABELS, &*PREFIXES_LABELS, &*BASE_UNITS_LABELS] {
            for c in rows.iter().filter_map(|row| row.first()?.chars().next()) {
                if c.is_ascii() {
                    ascii[c as usize] = true;
                } else {
                    other.insert(c);
                }
            }
        }
        (ascii, other)
    });
fn rows_at(index: &'static UnitIndex, text: &[char], pos: usize) -> &'static [&'static Vec<String>] {
    match text.get(pos).copied() {
        Some(c) if c.is_ascii() => &index.ascii[c as usize],
        Some(c) => index.other.get(&c).map_or(&[], Vec::as_slice),
        None => &[],
    }
}

#[derive(Clone, Copy)]
struct SimpleUnit {
    prefix: Option<&'static str>,
    base: &'static str,
}
#[derive(Clone, Copy)]
struct Unit {
    first: SimpleUnit,
    second: Option<SimpleUnit>,
}
impl std::fmt::Display for Unit {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        fn simple(f: &mut std::fmt::Formatter<'_>, unit: SimpleUnit) -> std::fmt::Result {
            if let Some(prefix) = unit.prefix {
                write!(f, "{} ", prefix)?;
            }
            f.write_str(unit.base)
        }
        simple(f, self.first)?;
        if let Some(second) = self.second {
            f.write_str("  trên  ")?;
            simple(f, second)?;
        }
        Ok(())
    }
}

fn scan_simple_unit(text: &[char], start: usize, mut f: impl FnMut(usize, SimpleUnit, Cost)) {
    let matches_at = |pos: usize, value: &str| {
        let mut i = pos;
        for c in value.chars() {
            if text.get(i) != Some(&c) {
                return None;
            }
            i += 1;
        }
        Some(i)
    };
    for row in rows_at(&SPECIAL_INDEX, text, start) {
        if row.len() >= 2 {
            if let Some(end) = matches_at(start, &row[0]) {
                f(end, SimpleUnit { prefix: None, base: &row[1] }, cost(-0.01));
            }
        }
    }
    for prefix in rows_at(&PREFIX_INDEX, text, start) {
        if prefix.len() < 2 {
            continue;
        }
        if let Some(pos) = matches_at(start, &prefix[0]) {
            for base in rows_at(&BASE_INDEX, text, pos) {
                if base.len() >= 2 {
                    if let Some(end) = matches_at(pos, &base[0]) {
                        f(end, SimpleUnit { prefix: Some(&prefix[1]), base: &base[1] }, 0);
                    }
                }
            }
        }
    }
    for base in rows_at(&BASE_INDEX, text, start) {
        if base.len() >= 2 {
            if let Some(end) = matches_at(start, &base[0]) {
                f(end, SimpleUnit { prefix: None, base: &base[1] }, 0);
            }
        }
    }
}

fn scan_unit(text: &[char], start: usize, _render: bool, mut f: impl FnMut(usize, Unit, Cost)) {
    let Some(&first) = text.get(start) else { return };
    if if first.is_ascii() { !UNIT_INITIALS.0[first as usize] } else { !UNIT_INITIALS.1.contains(&first) } {
        return;
    }
    scan_simple_unit(text, start, |end, unit, weight| {
        if text.get(end) == Some(&'/') {
            scan_simple_unit(text, end + 1, |compound_end, suffix, suffix_weight| {
                f(compound_end, Unit { first: unit, second: Some(suffix) }, weight + suffix_weight);
            });
        }
        f(end, Unit { first: unit, second: None }, weight);
    });
}

fn is_decimal_domain(text: &[char]) -> bool {
    let Some(sep) = text.iter().position(|&c| c == ',' || c == '.') else { return false };
    let fraction = &text[sep + 1..];
    sep > 0
        && text[..sep].iter().all(char::is_ascii_digit)
        && !fraction.is_empty()
        && fraction.iter().all(char::is_ascii_digit)
        && (text[sep] == ',' || fraction.len() != 3)
}

fn is_fraction_domain(text: &[char]) -> bool {
    let Some(&last) = text.last() else { return false };
    if super::fraction::UNICODE_FRACTIONS.iter().any(|&(ch, _, _)| ch == last) {
        let mut prefix = &text[..text.len() - 1];
        if prefix.last() == Some(&' ') {
            prefix = &prefix[..prefix.len() - 1];
        }
        if prefix.is_empty() || prefix.iter().all(char::is_ascii_digit) {
            return true;
        }
        let mut parts = prefix.split(|&c| c == '.');
        let first = parts.next().unwrap_or(&[]);
        return !first.is_empty()
            && first.len() <= 3
            && first.iter().all(char::is_ascii_digit)
            && parts.all(|part| part.len() == 3 && part.iter().all(char::is_ascii_digit));
    }
    let Some(slash) = text.iter().position(|&c| c == '/' || c == '⁄') else { return false };
    let den = &text[slash + 1..];
    let mut before = text[..slash].split(|&c| c == ' ');
    let Some(first) = before.next() else { return false };
    if first.is_empty()
        || !first.iter().all(char::is_ascii_digit)
        || den.is_empty()
        || !den.iter().all(char::is_ascii_digit)
    {
        return false;
    }
    match before.next() {
        None => true,
        Some(num) => !num.is_empty() && num.iter().all(char::is_ascii_digit) && before.next().is_none(),
    }
}

impl Measure {
    pub fn scan_spans(text: &[char], start: usize, f: impl FnMut(usize, String, Cost)) {
        Self::scan(text, start, true, f);
    }

    pub(super) fn scan(text: &[char], start: usize, render: bool, f: impl FnMut(usize, String, Cost)) {
        Self::scan_selected(text, start, render, None, f);
    }

    fn scan_selected(
        text: &[char],
        start: usize,
        render: bool,
        target: Option<(usize, Cost)>,
        mut f: impl FnMut(usize, String, Cost),
    ) {
        let found = std::cell::Cell::new(false);
        if start >= text.len() {
            return;
        }

        // Optional negative '-'
        let (negative, num_start) =
            if text[start] == '-' && start + 1 < text.len() { (true, start + 1) } else { (false, start) };
        // The accepted cardinal and decimal domains start with a digit; the
        // fraction branch additionally accepts the explicit vulgar symbols.
        if !text[num_start].is_ascii_digit()
            && !super::fraction::UNICODE_FRACTIONS.iter().any(|&(c, _, _)| c == text[num_start])
        {
            return;
        }

        macro_rules! emit_selected {
            ($end:expr, $kind:expr, $number:expr, $unit:expr, $cost:expr) => {{
                let end = $end;
                let cost = $cost;
                if !found.get() && target.is_none_or(|wanted| wanted == (end, cost)) {
                    let mut full = if render { String::with_capacity(96) } else { String::new() };
                    if render {
                        use std::fmt::Write;
                        full.push_str("measure { ");
                        if negative {
                            full.push_str("negative: \"true\" ");
                        }
                        full.push_str($kind);
                        full.push_str(" { ");
                        write!(&mut full, "{}", $number).expect("writing to String cannot fail");
                        full.push_str(" } units: \"");
                        write!(&mut full, "{}", $unit).expect("writing to String cannot fail");
                        full.push_str("\" }");
                    }
                    f(end, full, cost);
                    if target.is_some() {
                        found.set(true);
                    }
                }
            }};
        }

        // 1. Cardinal + unit
        // Check dot-grouped integer: 1.000 kg
        let mut pos = num_start;
        let mut grouped_digits = [0u8; 21];
        let mut grouped_len = 0;
        while pos < text.len() && text[pos].is_ascii_digit() && grouped_len < 3 {
            grouped_digits[grouped_len] = text[pos] as u8;
            grouped_len += 1;
            pos += 1;
        }
        while grouped_len != 0
            && grouped_len + 3 <= 21
            && pos + 3 < text.len()
            && text[pos] == '.'
            && text[pos + 1..pos + 4].iter().all(char::is_ascii_digit)
        {
            for &c in &text[pos + 1..pos + 4] {
                grouped_digits[grouped_len] = c as u8;
                grouped_len += 1;
            }
            pos += 4;
            let u_start = pos + usize::from(text.get(pos) == Some(&' '));
            scan_unit(text, u_start, render, |u_end, u_spoken, u_cost| {
                if let Some(extra_cost) = cardinal_cost(&grouped_digits[..grouped_len]) {
                    let int_str = SpokenNumber { digits: &grouped_digits[..grouped_len], grouped: false };
                    emit_selected!(
                        u_end,
                        "cardinal",
                        format_args!("integer: \"{}\"", int_str),
                        &u_spoken,
                        extra_cost + u_cost
                    );
                }
            });
            if found.get() {
                return;
            }
        }

        // Plain cardinal integer: 12kg, 1 kg
        let mut numeric_end = num_start;
        if text[num_start].is_ascii_digit() {
            let mut digits = [0u8; 21];
            let mut len = 0;
            let mut k = num_start;
            while k < text.len() && text[k].is_ascii_digit() && len < 21 {
                digits[len] = text[k] as u8;
                len += 1;
                k += 1;
                numeric_end = k;

                let u_start = k + usize::from(text.get(k) == Some(&' '));
                scan_unit(text, u_start, render, |u_end, u_spoken, u_cost| {
                    if !negative
                        && k == num_start + 4
                        && matches!(text[num_start], '1' | '2')
                        && text[k - 1] == '0'
                        && text.get(k) == Some(&'s')
                        && u_end == k + 1
                    {
                        return;
                    }
                    if let Some(int_cost) = cardinal_cost(&digits[..len]) {
                        let int_str = SpokenNumber { digits: &digits[..len], grouped: false };
                        emit_selected!(
                            u_end,
                            "cardinal",
                            format_args!("integer: \"{}\"", int_str),
                            &u_spoken,
                            int_cost + u_cost
                        );
                    }
                });
                if found.get() {
                    return;
                }
            }
        }

        // Only digit-separator-digit spans survive is_decimal_domain.
        if numeric_end > num_start
            && matches!(text.get(numeric_end), Some(',' | '.'))
            && text.get(numeric_end + 1).is_some_and(char::is_ascii_digit)
        {
            Decimal::scan_positive(text, num_start, render && target.is_none(), |d_end, d_body, d_cost| {
                if !is_decimal_domain(&text[num_start..d_end]) {
                    return;
                }
                let u_start = d_end + usize::from(text.get(d_end) == Some(&' '));
                scan_unit(text, u_start, render, |u_end, u_spoken, u_cost| {
                    if render && target.is_some() {
                        if found.get() || target != Some((u_end, d_cost + u_cost)) {
                            return;
                        }
                        let mut selected = String::new();
                        Decimal::scan_selected(text, num_start, true, Some((d_end, d_cost)), |_, body, _| {
                            selected = body
                        });
                        emit_selected!(u_end, "decimal", &selected, &u_spoken, d_cost + u_cost);
                    } else {
                        emit_selected!(u_end, "decimal", &d_body, &u_spoken, d_cost + u_cost);
                    }
                });
            });
            if found.get() {
                return;
            }
        }

        // 3. Fraction + unit: 1/2kg, ½kg
        Fraction::scan_positive(text, num_start, render && target.is_none(), |frac_end, frac_body, frac_cost| {
            if !is_fraction_domain(&text[num_start..frac_end]) {
                return;
            }
            let u_start = frac_end + usize::from(text.get(frac_end) == Some(&' '));
            scan_unit(text, u_start, render, |u_end, u_spoken, u_cost| {
                if render && target.is_some() {
                    if found.get() || target != Some((u_end, frac_cost + u_cost)) {
                        return;
                    }
                    let mut selected = String::new();
                    Fraction::scan_selected(text, num_start, true, Some((frac_end, frac_cost)), |_, body, _| {
                        selected = body
                    });
                    emit_selected!(u_end, "fraction", &selected, &u_spoken, frac_cost + u_cost);
                } else {
                    emit_selected!(u_end, "fraction", &frac_body, &u_spoken, frac_cost + u_cost);
                }
            });
        });
    }
}

impl Classifier for Measure {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan(text, start, false, |end, _, extra_cost| {
            out.push(Candidate { end, cost: WEIGHT_MEASURE + extra_cost, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        Self::scan_selected(
            text,
            start,
            true,
            Some((candidate.end, candidate.cost - WEIGHT_MEASURE)),
            |end, tag, extra_cost| {
                if candidate.tag.is_empty() && end == candidate.end && WEIGHT_MEASURE + extra_cost == candidate.cost {
                    candidate.tag = tag;
                }
            },
        );
    }
}

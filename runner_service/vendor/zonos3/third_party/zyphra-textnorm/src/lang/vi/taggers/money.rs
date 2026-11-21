// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese money classifier: currency symbols, minor units (cents/xu), quantities, per-units.

use super::super::graph_utils::{
    Digits, SpokenFraction, SpokenNumber, WEIGHT_MONEY, cardinal_cost, dot_cost, scan_dot_grouped_numbers,
};
use super::decimal::Decimal;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_nemo_space};

pub struct Money;

crate::labels!(CURRENCY_LABELS, "vi/money/currency.tsv");
crate::labels!(CURRENCY_MINOR_LABELS, "vi/money/currency_minor.tsv");
crate::labels!(PER_UNIT_NON_METRIC, "vi/money/per_unit_non_metric.tsv");
crate::labels!(PER_UNIT_PREFIXES, "vi/money/per_unit_prefixes.tsv");
crate::labels!(PER_UNIT_BASES, "vi/money/per_unit_bases.tsv");
crate::labels!(QUANTITY_ABBRS, "vi/numbers/quantity_abbr.tsv");

struct QuantityIndex {
    ascii: [Vec<(&'static str, &'static str)>; 128],
    other: std::collections::HashMap<char, Vec<(&'static str, &'static str)>>,
}
static QUANTITY_INDEX: std::sync::LazyLock<QuantityIndex> = std::sync::LazyLock::new(|| {
    let mut index =
        QuantityIndex { ascii: std::array::from_fn(|_| Vec::new()), other: std::collections::HashMap::new() };
    for (key, value) in QUANTITY_ABBRS
        .iter()
        .filter_map(|row| Some((row.first()?.as_str(), row.get(1)?.as_str())))
        .chain([("m", "triệu"), ("M", "triệu"), ("b", "tỷ"), ("B", "tỷ")])
    {
        if let Some(first) = key.chars().next() {
            if first.is_ascii() {
                index.ascii[first as usize].push((key, value));
            } else {
                index.other.entry(first).or_default().push((key, value));
            }
        }
    }
    index
});
fn quantity_rows(text: &[char], pos: usize) -> &'static [(&'static str, &'static str)] {
    match text.get(pos).copied() {
        Some(c) if c.is_ascii() => &QUANTITY_INDEX.ascii[c as usize],
        Some(c) => QUANTITY_INDEX.other.get(&c).map_or(&[], Vec::as_slice),
        None => &[],
    }
}

struct UnitIndex {
    ascii: [Vec<&'static Vec<String>>; 128],
    other: std::collections::HashMap<char, Vec<&'static Vec<String>>>,
}
fn unit_index(rows: &'static [Vec<String>], skip: usize, prefix_only: bool) -> UnitIndex {
    let mut index = UnitIndex { ascii: std::array::from_fn(|_| Vec::new()), other: std::collections::HashMap::new() };
    for row in rows {
        if prefix_only && (row.len() < 2 || !is_prefix_currency(&row[0])) {
            continue;
        }
        if let Some(c) = row.first().and_then(|s| s.chars().nth(skip)) {
            if c.is_ascii() {
                index.ascii[c as usize].push(row);
            } else {
                index.other.entry(c).or_default().push(row);
            }
        }
    }
    index
}
static CURRENCY_INDEX: std::sync::LazyLock<UnitIndex> =
    std::sync::LazyLock::new(|| unit_index(&CURRENCY_LABELS, 0, false));
static PREFIX_CURRENCY_INDEX: std::sync::LazyLock<UnitIndex> =
    std::sync::LazyLock::new(|| unit_index(&CURRENCY_LABELS, 0, true));
fn currency_rows(text: &[char], pos: usize) -> &'static [&'static Vec<String>] {
    unit_rows(&CURRENCY_INDEX, text, pos)
}
static PER_NONMETRIC_INDEX: std::sync::LazyLock<UnitIndex> =
    std::sync::LazyLock::new(|| unit_index(&PER_UNIT_NON_METRIC, 1, false));
static PER_PREFIX_INDEX: std::sync::LazyLock<UnitIndex> =
    std::sync::LazyLock::new(|| unit_index(&PER_UNIT_PREFIXES, 0, false));
static PER_BASE_INDEX: std::sync::LazyLock<UnitIndex> =
    std::sync::LazyLock::new(|| unit_index(&PER_UNIT_BASES, 0, false));
fn unit_rows(index: &'static UnitIndex, text: &[char], pos: usize) -> &'static [&'static Vec<String>] {
    match text.get(pos).copied() {
        Some(c) if c.is_ascii() => &index.ascii[c as usize],
        Some(c) => index.other.get(&c).map_or(&[], Vec::as_slice),
        None => &[],
    }
}

// If a source currency or abbreviated quantity could begin inside a numeric
// run, the inexpensive continuation check below must not consume past it.
static NUMERIC_ALIAS_INITIAL: std::sync::LazyLock<bool> = std::sync::LazyLock::new(|| {
    CURRENCY_LABELS.iter().chain(QUANTITY_ABBRS.iter()).any(|row| {
        row.first().and_then(|s| s.chars().next()).is_some_and(|c| c.is_ascii_digit() || matches!(c, '.' | ','))
    })
});

fn may_have_numeric_currency(text: &[char], start: usize) -> bool {
    if *NUMERIC_ALIAS_INITIAL {
        return true;
    }
    let mut end = start;
    while text.get(end).is_some_and(|c| c.is_ascii_digit() || matches!(c, '.' | ',')) {
        end += 1;
    }
    let currency_after = |mut pos: usize| {
        while text.get(pos).is_some_and(|&c| is_nemo_space(c)) {
            pos += 1;
        }
        !currency_rows(text, pos).is_empty()
    };
    if currency_after(end) {
        return true;
    }
    for (abbr, _) in quantity_rows(text, end).iter().copied() {
        let len = abbr.chars().count();
        if starts_limited(text, end, len, abbr) {
            let after = end + len;
            if currency_after(after) || (text.get(after).is_some_and(char::is_ascii_digit) && currency_after(after + 1))
            {
                return true;
            }
        }
    }
    Decimal::quantity_suffixes(text, end).any(|(after, _)| currency_after(after))
}

fn is_prefix_currency(s: &str) -> bool {
    let first = s.chars().next().unwrap_or(' ');
    if !first.is_alphabetic() {
        return true;
    }
    if s.ends_with('$') || s.ends_with('¥') || s == "Rs." || s == "Rs" || s == "rs" {
        return true;
    }
    if s.chars().count() == 3 && s.chars().all(|c| c.is_alphabetic() && c.is_uppercase()) {
        return true;
    }
    false
}

fn lookup_currency_min(s: &str) -> Option<&'static str> {
    for row in CURRENCY_MINOR_LABELS.iter() {
        if row.len() >= 2 && row[0] == s {
            return Some(&row[1]);
        }
    }
    None
}

fn starts_limited(text: &[char], start: usize, limit: usize, value: &str) -> bool {
    for (offset, c) in value.chars().enumerate() {
        if offset >= limit || text.get(start + offset) != Some(&c) {
            return false;
        }
    }
    true
}

pub(in crate::lang::vi) fn starts_prefix(text: &[char], start: usize) -> bool {
    unit_rows(&PREFIX_CURRENCY_INDEX, text, start).iter().any(|row| starts_limited(text, start, 6, &row[0]))
}

#[derive(Clone, Copy)]
struct PerUnit {
    literal: Option<&'static str>,
    prefix: Option<&'static str>,
    base: &'static str,
}
impl std::fmt::Display for PerUnit {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if let Some(literal) = self.literal {
            return f.write_str(literal);
        }
        f.write_str("một ")?;
        if let Some(prefix) = self.prefix {
            write!(f, "{} ", prefix)?;
        }
        f.write_str(self.base)
    }
}

fn scan_per_unit(text: &[char], start: usize, _render: bool, mut f: impl FnMut(usize, PerUnit)) {
    if text.get(start) != Some(&'/') {
        return;
    }
    let matches_at = |pos: usize, value: &str| {
        let mut chars = value.chars();
        let mut i = pos;
        loop {
            match chars.next() {
                None => return Some(i),
                Some(c) if text.get(i) == Some(&c) => i += 1,
                Some(_) => return None,
            }
        }
    };
    for row in unit_rows(&PER_NONMETRIC_INDEX, text, start + 1) {
        if row.len() >= 2 {
            if let Some(end) = matches_at(start, &row[0]) {
                f(end, PerUnit { literal: Some(&row[1]), prefix: None, base: "" });
            }
        }
    }
    for prefix in unit_rows(&PER_PREFIX_INDEX, text, start + 1) {
        if prefix.len() < 2 {
            continue;
        }
        if let Some(pos) = matches_at(start + 1, &prefix[0]) {
            for base in unit_rows(&PER_BASE_INDEX, text, pos) {
                if base.len() >= 2 {
                    if let Some(end) = matches_at(pos, &base[0]) {
                        f(end, PerUnit { literal: None, prefix: Some(&prefix[1]), base: &base[1] });
                    }
                }
            }
        }
    }
    for base in unit_rows(&PER_BASE_INDEX, text, start + 1) {
        if base.len() >= 2 {
            if let Some(end) = matches_at(start + 1, &base[0]) {
                f(end, PerUnit { literal: None, prefix: None, base: &base[1] });
            }
        }
    }
}

struct Cents {
    digits: [u8; 2],
    len: usize,
}
impl std::fmt::Display for Cents {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{}", SpokenNumber { digits: &self.digits[..self.len], grouped: false })
    }
}

fn spell_cents(frac: &[char], _render: bool) -> Option<Cents> {
    // Strip trailing zeros after non-zero digits
    let mut end = frac.len();
    while end > 0 && frac[end - 1] == '0' {
        end -= 1;
    }
    if end == 0 {
        return None;
    }
    let trimmed = &frac[..end];

    let mut digits = [0u8; 2];
    let len = if trimmed.len() == 1 {
        digits = [trimmed[0] as u8, b'0'];
        2
    } else if trimmed.len() == 2 {
        if trimmed[0] == '0' {
            digits[0] = trimmed[1] as u8;
            1
        } else {
            digits = [trimmed[0] as u8, trimmed[1] as u8];
            2
        }
    } else {
        return None;
    };
    cardinal_cost(&digits[..len])?;
    Some(Cents { digits, len })
}

impl Money {
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
        macro_rules! format {
            ($($arg:tt)*) => { if render { std::format!($($arg)*) } else { String::new() } };
        }
        if start >= text.len() {
            return;
        }

        // Check optional negative: '-' or '−'
        let (negative, num_start) = if text[start] == '-' || text[start] == '−' {
            let next = start + 1;
            let mut skip_sp = next;
            while skip_sp < text.len() && is_nemo_space(text[skip_sp]) {
                skip_sp += 1;
            }
            (true, skip_sp)
        } else {
            (false, start)
        };
        if num_start >= text.len() {
            return;
        }
        let first = text[num_start];
        let prefix_rows = unit_rows(&PREFIX_CURRENCY_INDEX, text, num_start);
        if !first.is_ascii_digit() && prefix_rows.is_empty() {
            return;
        }
        if first.is_ascii_digit() && prefix_rows.is_empty() && !may_have_numeric_currency(text, num_start) {
            return;
        }

        let emit = |end: usize, body: String, extra_cost: Cost, f: &mut dyn FnMut(usize, String, Cost)| {
            let mut full = String::new();
            if render {
                full.push_str("money { ");
                if negative {
                    full.push_str("negative: \"true\" ");
                }
                full.push_str(&body);
            }
            // Check optional per-unit suffix
            scan_per_unit(text, end, render, |pu_end, pu_val| {
                if found.get() || target.is_some_and(|wanted| wanted != (pu_end, extra_cost)) {
                    return;
                }
                let mut with_unit = String::new();
                if render {
                    with_unit.push_str(&full);
                    with_unit.push_str(" morphosyntactic_features: \"");
                    use std::fmt::Write;
                    write!(&mut with_unit, "{}", pu_val).expect("writing to String cannot fail");
                    with_unit.push_str("\" }");
                }
                f(pu_end, with_unit, extra_cost);
                if target.is_some() {
                    found.set(true);
                }
            });
            if !found.get() && target.is_none_or(|wanted| wanted == (end, extra_cost)) {
                if render {
                    full.push_str(" }");
                }
                f(end, full, extra_cost);
                if target.is_some() {
                    found.set(true);
                }
            }
        };
        macro_rules! emit_selected {
            ($end:expr, $body:expr, $cost:expr) => {{
                let end = $end;
                let extra = $cost;
                let mut matches = target.is_none_or(|wanted| wanted == (end, extra));
                if let Some((wanted_end, wanted_cost)) = target {
                    if !matches && wanted_cost == extra && !found.get() {
                        scan_per_unit(text, end, false, |suffix_end, _| {
                            if suffix_end == wanted_end {
                                matches = true;
                            }
                        });
                    }
                }
                if matches && !found.get() {
                    emit(end, $body, extra, &mut f);
                }
            }};
        }

        // 1. Prefix currency patterns: $10, $10,5, $0,5, $0.99, USD 100, CA$50
        let rem = &text[num_start..text.len().min(num_start + 6)];
        for row in prefix_rows {
            if found.get() {
                return;
            }
            let sym = &row[0];
            let maj_name = &row[1];
            if sym.chars().count() <= rem.len() && rem.iter().copied().zip(sym.chars()).all(|(a, b)| a == b) {
                let sym_end = num_start + sym.chars().count();
                let mut p = sym_end;
                while p < text.len() && is_nemo_space(text[p]) {
                    p += 1;
                }

                if p < text.len() {
                    Decimal::scan_positive(text, p, render && target.is_none(), |end, body, c| {
                        emit_selected!(
                            end,
                            {
                                let mut selected = String::new();
                                if render && target.is_some() {
                                    Decimal::scan_selected(text, p, true, Some((end, c)), |_, value, _| {
                                        selected = value
                                    });
                                }
                                let value = if render && target.is_some() { &selected } else { &body };
                                format!("currency_maj: \"{}\" {}", maj_name, value)
                            },
                            c + cost(0.1)
                        );
                    });
                    scan_dot_grouped_numbers(text, p, false, |end, digits| {
                        if let Some(c) = dot_cost(digits) {
                            let integer = SpokenNumber { digits, grouped: true };
                            emit_selected!(
                                end,
                                format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, integer),
                                c
                            );
                            if text.get(end) == Some(&',') {
                                let mut q = end + 1;
                                let frac_start = q;
                                while text.get(q).is_some_and(char::is_ascii_digit) {
                                    q += 1;
                                    if q - frac_start <= 2 && text[frac_start..q].iter().all(|&d| d == '0') {
                                        emit_selected!(
                                            q,
                                            format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, integer),
                                            c
                                        );
                                    }
                                    if let (Some(cents), Some(minor)) =
                                        (spell_cents(&text[frac_start..q], render), lookup_currency_min(sym))
                                    {
                                        emit_selected!(
                                            q,
                                            format!(
                                                "integer_part: \"{}\" currency_maj: \"{}\" fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                                integer, maj_name, cents, minor
                                            ),
                                            c
                                        );
                                    }
                                }
                            }
                            for (abbr, quantity) in quantity_rows(text, end).iter().copied() {
                                let q = end + abbr.chars().count();
                                if text.get(end..q).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                                    emit_selected!(
                                        q,
                                        format!(
                                            "currency_maj: \"{}\" integer_part: \"{}\" quantity: \"{}\"",
                                            maj_name, integer, quantity
                                        ),
                                        c
                                    );
                                }
                            }
                            if matches!(text.get(end), Some(&',') | Some(&'.')) {
                                let mut q = end + 1;
                                let frac_start = q;
                                while text.get(q).is_some_and(char::is_ascii_digit) {
                                    q += 1;
                                    let fractional = SpokenFraction(&text[frac_start..q]);
                                    for (abbr, quantity) in quantity_rows(text, q).iter().copied() {
                                        let e = q + abbr.chars().count();
                                        if text.get(q..e).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                                            emit_selected!(
                                                e,
                                                format!(
                                                    "currency_maj: \"{}\" integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                                    maj_name, integer, fractional, quantity
                                                ),
                                                c
                                            );
                                        }
                                    }
                                }
                            }
                        }
                    });
                    // 1a. Prefix minor-only: $0,99 or $0.99 (cost -0.0001)
                    if text[p] == '0' && p + 1 < text.len() && (text[p + 1] == ',' || text[p + 1] == '.') {
                        let frac_start = p + 2;
                        let mut k = p + 2;
                        while k < text.len() && text[k].is_ascii_digit() {
                            k += 1;
                            if let Some(cents) = spell_cents(&text[frac_start..k], render) {
                                if let Some(min_name) = lookup_currency_min(sym) {
                                    emit_selected!(
                                        k,
                                        format!(
                                            "fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                            cents, min_name
                                        ),
                                        cost(-0.0001)
                                    );
                                }
                            }
                        }
                    }

                    // English comma groups are stripped before composition with cardinal.graph.
                    if text[p].is_ascii_digit() && text[p] != '0' {
                        let mut grouped = Digits::new();
                        let mut q = p;
                        while q < text.len() && text[q].is_ascii_digit() && grouped.len() < 3 {
                            grouped.push(text[q] as u8);
                            q += 1;
                        }
                        for _ in 0..3 {
                            if q + 3 >= text.len()
                                || text[q] != ','
                                || !text[q + 1..q + 4].iter().all(char::is_ascii_digit)
                            {
                                break;
                            }
                            for &c in &text[q + 1..q + 4] {
                                grouped.push(c as u8);
                            }
                            q += 4;
                            if let Some(c) = cardinal_cost(&grouped) {
                                let integer = SpokenNumber { digits: &grouped, grouped: false };
                                emit_selected!(
                                    q,
                                    format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, integer),
                                    c + cost(0.05)
                                );
                                if text.get(q..q + 3) == Some(&['.', '0', '0'][..]) {
                                    emit_selected!(
                                        q + 3,
                                        format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, integer),
                                        c
                                    );
                                }
                                if text.get(q) == Some(&'.') {
                                    for count in 1..=2 {
                                        if q + 1 + count > text.len() {
                                            break;
                                        }
                                        let frac = &text[q + 1..q + 1 + count];
                                        if frac.iter().all(char::is_ascii_digit) {
                                            if let (Some(cents), Some(min)) =
                                                (spell_cents(frac, render), lookup_currency_min(sym))
                                            {
                                                emit_selected!(
                                                    q + 1 + count,
                                                    format!(
                                                        "integer_part: \"{}\" currency_maj: \"{}\" fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                                        integer, maj_name, cents, min
                                                    ),
                                                    c
                                                );
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                    // 1b. Prefix amount with integer: $10, $10.50, $10,50, $1,000, $2,5M
                    if text[p].is_ascii_digit() {
                        let mut int_digits = Digits::new();
                        let mut k = p;
                        while k < text.len() && text[k].is_ascii_digit() && int_digits.len() < 21 {
                            int_digits.push(text[k] as u8);
                            k += 1;

                            if let Some(int_cost) = cardinal_cost(&int_digits) {
                                let int_str = SpokenNumber { digits: &int_digits, grouped: false };
                                // Plain integer: $10
                                emit_selected!(
                                    k,
                                    format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, int_str),
                                    int_cost
                                );

                                if text.get(k) == Some(&',') && text.get(k + 1) == Some(&'0') {
                                    emit_selected!(
                                        k + 2,
                                        format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, int_str),
                                        int_cost
                                    );
                                }
                                // with zero cents: $10,00 or $10.00
                                if k + 2 < text.len()
                                    && (text[k] == ',' || text[k] == '.')
                                    && text[k + 1] == '0'
                                    && text[k + 2] == '0'
                                {
                                    emit_selected!(
                                        k + 3,
                                        format!("currency_maj: \"{}\" integer_part: \"{}\"", maj_name, int_str),
                                        int_cost
                                    );
                                }

                                // with shorthand amount: $2,5M or $10M
                                for (abbr, full_name) in quantity_rows(text, k).iter().copied() {
                                    let len = abbr.chars().count();
                                    if starts_limited(text, k, len, abbr) {
                                        emit_selected!(
                                            k + len,
                                            format!(
                                                "currency_maj: \"{}\" integer_part: \"{}\" quantity: \"{}\"",
                                                maj_name, int_str, full_name
                                            ),
                                            int_cost
                                        );
                                    }
                                }

                                // Major + minor: $10,50 or $10.50
                                if k < text.len() && (text[k] == ',' || text[k] == '.') {
                                    let frac_start = k + 1;
                                    let mut m = k + 1;
                                    while m < text.len() && text[m].is_ascii_digit() {
                                        m += 1;
                                        if let Some(cents) = spell_cents(&text[frac_start..m], render)
                                            .filter(|_| text[k] != '.' || m - frac_start <= 2)
                                        {
                                            if let Some(min_name) = lookup_currency_min(sym) {
                                                emit_selected!(
                                                    m,
                                                    format!(
                                                        "integer_part: \"{}\" currency_maj: \"{}\" fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                                        int_str, maj_name, cents, min_name
                                                    ),
                                                    int_cost
                                                );
                                            }
                                        }

                                        // with shorthand (e.g. $2,5M)
                                        let frac_str = SpokenFraction(&text[frac_start..m]);
                                        for (abbr, full_name) in quantity_rows(text, m).iter().copied() {
                                            let len = abbr.chars().count();
                                            if starts_limited(text, m, len, abbr) {
                                                emit_selected!(
                                                    m + len,
                                                    format!(
                                                        "currency_maj: \"{}\" integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                                                        maj_name, int_str, frac_str, full_name
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
                }
            }
        }
        if found.get() {
            return;
        }

        scan_dot_grouped_numbers(text, num_start, false, |end, digits| {
            if found.get() {
                return;
            }
            if let Some(c) = dot_cost(digits) {
                let integer = SpokenNumber { digits, grouped: true };
                let mut p = end;
                while p < text.len() && is_nemo_space(text[p]) {
                    p += 1;
                }
                for row in currency_rows(text, p) {
                    let (Some(written), Some(spoken)) = (row.first(), row.get(1)) else { continue };
                    let currency_end = p + written.chars().count();
                    if text.get(p..currency_end).is_some_and(|s| s.iter().copied().eq(written.chars())) {
                        if p == end {
                            emit_selected!(
                                currency_end,
                                format!("integer_part: \"{}\" currency_maj: \"{}\"", integer, spoken),
                                c
                            );
                        }
                        emit_selected!(
                            currency_end,
                            format!(
                                "integer_part: \"{}\" currency_maj: \"{}\"",
                                integer,
                                spoken.replace(' ', "\u{a0}")
                            ),
                            c + cost(0.1)
                        );
                    }
                }
                for (abbr, quantity) in quantity_rows(text, end).iter().copied() {
                    let q = end + abbr.chars().count();
                    if !text.get(end..q).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                        continue;
                    }
                    let mut p = q;
                    while p < text.len() && is_nemo_space(text[p]) {
                        p += 1;
                    }
                    for row in currency_rows(text, p) {
                        let (Some(written), Some(spoken)) = (row.first(), row.get(1)) else { continue };
                        let e = p + written.chars().count();
                        if text.get(p..e).is_some_and(|s| s.iter().copied().eq(written.chars())) {
                            emit_selected!(
                                e,
                                format!(
                                    "integer_part: \"{}\" quantity: \"{}\" currency_maj: \"{}\"",
                                    integer,
                                    quantity,
                                    spoken.replace(' ', "\u{a0}")
                                ),
                                c + cost(0.1)
                            );
                        }
                    }
                }
                if matches!(text.get(end), Some(&',') | Some(&'.')) {
                    let mut q = end + 1;
                    let frac_start = q;
                    while text.get(q).is_some_and(char::is_ascii_digit) {
                        q += 1;
                        let fractional = SpokenFraction(&text[frac_start..q]);
                        for (abbr, quantity) in quantity_rows(text, q).iter().copied() {
                            let e = q + abbr.chars().count();
                            if !text.get(q..e).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                                continue;
                            }
                            let mut p = e;
                            while p < text.len() && is_nemo_space(text[p]) {
                                p += 1;
                            }
                            for row in currency_rows(text, p) {
                                let (Some(written), Some(spoken)) = (row.first(), row.get(1)) else { continue };
                                let currency_end = p + written.chars().count();
                                if text.get(p..currency_end).is_some_and(|s| s.iter().copied().eq(written.chars())) {
                                    emit_selected!(
                                        currency_end,
                                        format!(
                                            "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\" currency_maj: \"{}\"",
                                            integer,
                                            fractional,
                                            quantity,
                                            spoken.replace(' ', "\u{a0}")
                                        ),
                                        c + cost(0.1)
                                    );
                                }
                            }
                        }
                        if let Some(cents) = spell_cents(&text[frac_start..q], render).filter(|_| text[end] == ',') {
                            let mut p = q;
                            while p < text.len() && is_nemo_space(text[p]) {
                                p += 1;
                            }
                            for row in currency_rows(text, p) {
                                let (Some(written), Some(spoken)) = (row.first(), row.get(1)) else { continue };
                                let currency_end = p + written.chars().count();
                                if text.get(p..currency_end).is_some_and(|s| s.iter().copied().eq(written.chars())) {
                                    if let Some(minor) = lookup_currency_min(written) {
                                        emit_selected!(
                                            currency_end,
                                            format!(
                                                "integer_part: \"{}\" currency_maj: \"{}\" fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                                integer, spoken, cents, minor
                                            ),
                                            c
                                        );
                                    }
                                }
                            }
                        }
                    }
                }
            }
        });
        if found.get() {
            return;
        }

        Decimal::scan_positive(text, num_start, render && target.is_none(), |end, body, c| {
            if found.get() {
                return;
            }
            let mut currency_start = end;
            while currency_start < text.len() && is_nemo_space(text[currency_start]) {
                currency_start += 1;
            }
            for row in currency_rows(text, currency_start) {
                let (Some(written), Some(spoken)) = (row.first(), row.get(1)) else { continue };
                let currency_end = currency_start + written.chars().count();
                if text.get(currency_start..currency_end).is_some_and(|part| part.iter().copied().eq(written.chars())) {
                    emit_selected!(
                        currency_end,
                        {
                            let mut selected = String::new();
                            if render && target.is_some() {
                                Decimal::scan_selected(text, num_start, true, Some((end, c)), |_, value, _| {
                                    selected = value
                                });
                            }
                            let value = if render && target.is_some() { &selected } else { &body };
                            format!("{} currency_maj: \"{}\"", value, spoken.replace(' ', "\u{a0}"))
                        },
                        c + cost(0.1)
                    );
                }
            }
        });
        if found.get() {
            return;
        }

        // 2. Trailing currency patterns: 10$, 10,5$, 0,5$, 10đ, 10 đồng, 10 triệu đồng
        // Minor-only trailing: 0,5$ -> weight -0.0001
        if text[num_start] == '0' && num_start + 1 < text.len() && text[num_start + 1] == ',' {
            let frac_start = num_start + 2;
            let mut k = num_start + 2;
            while k < text.len() && text[k].is_ascii_digit() {
                k += 1;
                if let Some(cents) = spell_cents(&text[frac_start..k], render) {
                    let mut p = k;
                    while p < text.len() && is_nemo_space(text[p]) {
                        p += 1;
                    }
                    for row in currency_rows(text, p) {
                        let sym = &row[0];
                        if starts_limited(text, p, 6, sym) {
                            let end = p + sym.chars().count();
                            if let Some(min_name) = lookup_currency_min(sym) {
                                emit_selected!(
                                    end,
                                    format!(
                                        "fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                        cents, min_name
                                    ),
                                    cost(-0.0001)
                                );
                            }
                        }
                    }
                }
            }
        }
        if found.get() {
            return;
        }

        // Number before trailing symbol/word
        if text[num_start].is_ascii_digit() {
            let mut int_digits = Digits::new();
            let mut k = num_start;
            while k < text.len() && text[k].is_ascii_digit() && int_digits.len() < 21 {
                int_digits.push(text[k] as u8);
                k += 1;
                // Each trailing branch needs a currency initial, a quantity
                // initial, or a decimal separator immediately after the digits.
                let mut p = k;
                while p < text.len() && is_nemo_space(text[p]) {
                    p += 1;
                }
                let currencies = currency_rows(text, p);
                let quantities = quantity_rows(text, k);
                if currencies.is_empty() && quantities.is_empty() && !matches!(text.get(k), Some(',' | '.')) {
                    continue;
                }
                if let Some(int_cost) = cardinal_cost(&int_digits) {
                    let int_str = SpokenNumber { digits: &int_digits, grouped: false };
                    // Check trailing currency immediately or after space.
                    for row in currencies {
                        let sym = &row[0];
                        let maj_name = &row[1];
                        if starts_limited(text, p, 10, sym) {
                            let end = p + sym.chars().count();
                            if p == k {
                                emit_selected!(
                                    end,
                                    format!("integer_part: \"{}\" currency_maj: \"{}\"", int_str, maj_name),
                                    int_cost
                                );
                            }
                            emit_selected!(
                                end,
                                format!(
                                    "integer_part: \"{}\" currency_maj: \"{}\"",
                                    int_str,
                                    maj_name.replace(' ', "\u{a0}")
                                ),
                                cost(0.1) + int_cost
                            );
                            if found.get() {
                                return;
                            }
                        }
                    }

                    // Money shorthand quantities may also be followed by a currency.
                    for (abbr, full_name) in quantities.iter().copied() {
                        let q_start = k;
                        let len = abbr.chars().count();
                        if starts_limited(text, q_start, len, abbr) {
                            let q_end = q_start + len;
                            let mut c_start = q_end;
                            while c_start < text.len() && is_nemo_space(text[c_start]) {
                                c_start += 1;
                            }
                            for row in currency_rows(text, c_start) {
                                let sym = &row[0];
                                let maj_name = &row[1];
                                if starts_limited(text, c_start, 10, sym) {
                                    let end = c_start + sym.chars().count();
                                    emit_selected!(
                                        end,
                                        format!(
                                            "integer_part: \"{}\" quantity: \"{}\" currency_maj: \"{}\"",
                                            int_str,
                                            full_name,
                                            maj_name.replace(' ', "\u{a0}")
                                        ),
                                        cost(0.1) + int_cost
                                    );
                                }
                            }
                        }
                    }

                    // Trailing major + minor: 10,5$ -> mười đô la năm mươi xu
                    if k < text.len() && (text[k] == ',' || text[k] == '.') {
                        let frac_start = k + 1;
                        let mut m = k + 1;
                        while m < text.len() && text[m].is_ascii_digit() {
                            m += 1;
                            let fractional = SpokenFraction(&text[frac_start..m]);
                            for (abbr, quantity) in quantity_rows(text, m).iter().copied() {
                                let q_end = m + abbr.chars().count();
                                if !text.get(m..q_end).is_some_and(|s| s.iter().copied().eq(abbr.chars())) {
                                    continue;
                                }
                                let mut p = q_end;
                                while p < text.len() && is_nemo_space(text[p]) {
                                    p += 1;
                                }
                                for row in currency_rows(text, p) {
                                    let (Some(written), Some(spoken)) = (row.first(), row.get(1)) else { continue };
                                    let end = p + written.chars().count();
                                    if text.get(p..end).is_some_and(|s| s.iter().copied().eq(written.chars())) {
                                        emit_selected!(
                                            end,
                                            format!(
                                                "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\" currency_maj: \"{}\"",
                                                int_str,
                                                fractional,
                                                quantity,
                                                spoken.replace(' ', "\u{a0}")
                                            ),
                                            int_cost + cost(0.1)
                                        );
                                    }
                                }
                            }
                            if let Some(cents) = spell_cents(&text[frac_start..m], render).filter(|_| text[k] == ',') {
                                let mut p = m;
                                while p < text.len() && is_nemo_space(text[p]) {
                                    p += 1;
                                }
                                for row in currency_rows(text, p) {
                                    let sym = &row[0];
                                    let maj_name = &row[1];
                                    if starts_limited(text, p, 6, sym) {
                                        let end = p + sym.chars().count();
                                        if let Some(min_name) = lookup_currency_min(sym) {
                                            emit_selected!(
                                                end,
                                                format!(
                                                    "integer_part: \"{}\" currency_maj: \"{}\" fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                                    int_str, maj_name, cents, min_name
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
            }
        }
    }
}

impl Classifier for Money {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan(text, start, false, |end, _, extra_cost| {
            out.push(Candidate { end, cost: WEIGHT_MONEY + extra_cost, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        Self::scan_selected(
            text,
            start,
            true,
            Some((candidate.end, candidate.cost - WEIGHT_MONEY)),
            |end, tag, extra_cost| {
                if candidate.tag.is_empty() && end == candidate.end && WEIGHT_MONEY + extra_cost == candidate.cost {
                    candidate.tag = tag;
                }
            },
        );
    }
}

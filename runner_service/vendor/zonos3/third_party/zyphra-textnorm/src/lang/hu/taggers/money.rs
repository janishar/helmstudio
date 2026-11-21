// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.

use crate::lang::hu::graph_utils::{inflect_abbreviation, naive_inflector};
use crate::lang::hu::taggers::{cardinal, decimal};
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.10);
crate::tsv!(PER_UNITS, "hu/money/per_unit.tsv");

struct CurrencyInfo {
    major: String,
    minor: Option<&'static str>,
    leading: bool,
    minor_key: bool,
}

#[derive(Default)]
struct CurrencyNode {
    next: HashMap<char, usize>,
    info: Option<CurrencyInfo>,
}

static CURRENCIES: LazyLock<Vec<CurrencyNode>> = LazyLock::new(|| {
    let major = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/money/currency.tsv"));
    let minor = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/money/currency_minor.tsv"));
    let minors: HashMap<&'static str, &'static str> = minor.lines().filter_map(|l| l.split_once('\t')).collect();
    let mut map = HashMap::new();
    for (symbol, name) in major.lines().filter_map(|l| l.split_once('\t')) {
        let name = name.replace(' ', "\u{a0}");
        let min = minors.get(symbol).copied();
        map.insert(
            symbol.to_owned(),
            CurrencyInfo { major: name.clone(), minor: min, leading: true, minor_key: min.is_some() },
        );
        map.entry(name.replace('\u{a0}', " ")).or_insert_with(|| CurrencyInfo {
            major: name.clone(),
            minor: None,
            leading: false,
            minor_key: false,
        });
        for (input, output) in naive_inflector(symbol, &name.replace('\u{a0}', " "), false) {
            let output = output.replace(' ', "\u{a0}");
            map.insert(input, CurrencyInfo { major: output.clone(), minor: None, leading: false, minor_key: false });
            map.insert(
                output.replace('\u{a0}', " "),
                CurrencyInfo { major: output, minor: None, leading: false, minor_key: false },
            );
        }
        if symbol.len() == 3 && symbol.chars().all(|c| c.is_ascii_uppercase()) {
            for (input, output) in inflect_abbreviation(symbol, &name.replace('\u{a0}', " "), false) {
                map.insert(
                    input,
                    CurrencyInfo {
                        major: output.replace(' ', "\u{a0}"),
                        minor: None,
                        leading: false,
                        minor_key: false,
                    },
                );
            }
        }
    }
    let mut nodes = vec![CurrencyNode::default()];
    for (key, info) in map {
        let mut node = 0;
        for c in key.chars() {
            let next = match nodes[node].next.get(&c) {
                Some(&next) => next,
                None => {
                    let next = nodes.len();
                    nodes.push(CurrencyNode::default());
                    nodes[node].next.insert(c, next);
                    next
                }
            };
            node = next;
        }
        nodes[node].info = Some(info);
    }
    nodes
});

fn currencies(text: &[char], start: usize) -> impl Iterator<Item = (&'static CurrencyInfo, usize)> + '_ {
    let mut pos = start;
    let mut node = 0;
    std::iter::from_fn(move || {
        while let Some(&c) = text.get(pos) {
            node = *CURRENCIES[node].next.get(&c)?;
            pos += 1;
            if let Some(info) = &CURRENCIES[node].info {
                return Some((info, pos));
            }
        }
        None
    })
}

fn spaces(text: &[char], pos: usize) -> usize {
    if text.get(pos) == Some(&' ') { pos + 1 } else { pos }
}

fn ensure_space(text: &[char], mut pos: usize) -> usize {
    while matches!(text.get(pos), Some(' ' | '\t' | '\n' | '\r' | '\u{a0}')) {
        pos += 1;
    }
    pos
}

fn counted(n: u128) -> String {
    let mut s = String::new();
    cardinal::spell_counted_number(n, &mut s);
    s
}

fn emit(
    out: &mut Sink,
    text: &[char],
    end: usize,
    weight: Cost,
    rank: i32,
    negative: bool,
    fields: impl Fn() -> String,
) {
    let sign = if negative { "negative: \"true\" " } else { "" };
    out.push(end, weight, rank, || format!("money {{ {sign}{} }}", fields()));
    if out.found.is_some() {
        return;
    }
    for row in PER_UNITS.rows() {
        let mut e = end;
        for c in row.key.chars() {
            if text.get(e).copied() != Some(c) {
                break;
            }
            e += 1;
        }
        if e - end == row.key.chars().count() {
            out.push(e, weight, rank, || {
                format!(
                    "money {{ {sign}{} morphosyntactic_features: \"{}\" }}",
                    fields(),
                    row.value.replace(' ', "\u{a0}")
                )
            });
            if out.found.is_some() {
                return;
            }
        }
    }
}

pub struct Money;
impl Lazy for Money {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }
        let negative = matches!(text[start], '-' | '−');
        let p = if negative { spaces(text, start + 1) } else { start };
        if p >= text.len() {
            return;
        }
        // Leading currency accepts only the original currency labels.
        for (cur, ce) in currencies(text, p).into_iter().filter(|(c, _)| c.leading) {
            let n = spaces(text, ce);
            amounts(
                text,
                n,
                |end, body, w, rank| {
                    if out.found.is_some() {
                        return;
                    }
                    emit(out, text, end, WEIGHT + w, rank, negative, || {
                        format!("currency_maj: \"{}\" {}", cur.major, body.render())
                    });
                },
                true,
            );
            if out.found.is_some() {
                return;
            }
            if let Some(min) = cur.minor.filter(|_| cur.minor_key) {
                minor_amounts(text, n, |end, int, frac, w| {
                    emit(out, text, end, WEIGHT + w, 4, negative, || {
                        if let Some(int) = int {
                            format!(
                                "currency_maj: \"{}\" currency_min: \"{}\" integer_part: \"{}\" fractional_part: \"{}\"",
                                cur.major,
                                min.replace(' ', "\u{a0}"),
                                counted(int),
                                spell(frac)
                            )
                        } else {
                            format!(
                                "currency_min: \"{}\" fractional_part: \"{}\"",
                                min.replace(' ', "\u{a0}"),
                                spell(frac)
                            )
                        }
                    });
                });
                if out.found.is_some() {
                    return;
                }
            }
            // Repeated-symbol ranges have no whitespace around the dash.
            let sym = &text[p..ce];
            if sym.iter().any(|c| !c.is_alphabetic()) {
                for (e, a) in cardinal::numbers(text, n) {
                    if !text[n..e].iter().all(|c| c.is_ascii_digit()) || !matches!(text.get(e), Some('-' | '–' | '—'))
                    {
                        continue;
                    }
                    let s = e + 1;
                    if text.get(s..s + sym.len()) != Some(sym) {
                        continue;
                    }
                    let n2 = spaces(text, s + sym.len());
                    for (end, b) in cardinal::numbers(text, n2) {
                        if !text[n2..end].iter().all(|c| c.is_ascii_digit()) {
                            continue;
                        }
                        emit(
                            out,
                            text,
                            end,
                            WEIGHT + cardinal::number_cost(a) + cardinal::number_cost(b),
                            1,
                            negative,
                            || {
                                format!(
                                    "currency_maj: \"{}\" integer_part: \"{} {}\"",
                                    cur.major,
                                    cardinal::from_suffix(&counted(a)),
                                    cardinal::to_suffix(&counted(b))
                                )
                            },
                        );
                    }
                }
            }
        }
        if out.found.is_some() {
            return;
        }
        // Every remaining suffix form starts with an unsigned decimal digit
        // (the optional sign and its optional space were consumed above).
        if !text[p].is_ascii_digit() {
            return;
        }
        // Suffix forms accept inflected currencies; minor forms only accept original labels.
        amounts(
            text,
            p,
            |end, body, w, rank| {
                if out.found.is_some() {
                    return;
                }
                for (cur, ce) in currencies(text, ensure_space(text, end)) {
                    emit(out, text, ce, WEIGHT + w, rank + 10, negative, || {
                        format!("{} currency_maj: \"{}\" preserve_order: true", body.render(), cur.major)
                    });
                }
            },
            false,
        );
        if out.found.is_some() {
            return;
        }
        if negative && p == start + 1 {
            amounts(
                text,
                start,
                |end, body, w, rank| {
                    if out.found.is_some() {
                        return;
                    }
                    for (cur, ce) in currencies(text, ensure_space(text, end)) {
                        emit(out, text, ce, WEIGHT + w, rank + 10, false, || {
                            format!("{} currency_maj: \"{}\" preserve_order: true", body.render(), cur.major)
                        });
                    }
                },
                false,
            );
            if out.found.is_some() {
                return;
            }
        }
        minor_amounts(text, p, |end, int, frac, w| {
            if out.found.is_some() {
                return;
            }
            for (cur, ce) in currencies(text, spaces(text, end)) {
                if let Some(min) = cur.minor.filter(|_| cur.minor_key) {
                    emit(out, text, ce, WEIGHT + w - cost(0.0001), 14, negative, || {
                        if let Some(int) = int {
                            format!(
                                "integer_part: \"{}\" fractional_part: \"{}\" currency_maj: \"{}\" currency_min: \"{}\"",
                                counted(int),
                                spell(frac),
                                cur.major,
                                min.replace(' ', "\u{a0}")
                            )
                        } else {
                            format!(
                                "fractional_part: \"{}\" currency_min: \"{}\" preserve_order: true",
                                spell(frac),
                                min.replace(' ', "\u{a0}")
                            )
                        }
                    });
                }
            }
        });
        for (end, n) in cardinal::numbers(text, p) {
            let e = spaces(text, end);
            if text.get(e) == Some(&'¢') {
                emit(out, text, e + 1, WEIGHT + cardinal::number_cost(n), 20, negative, || {
                    format!("fractional_part: \"{}\" currency_min: \"cent\" preserve_order: true", counted(n))
                });
            }
        }
    }
}

fn spell(n: u128) -> String {
    let mut s = String::new();
    cardinal::spell_number(n, &mut s);
    s
}
enum Amount<'a> {
    Integer(u128),
    Range(u128, u128, bool, bool),
    Magnitude(u128, char),
    Decimal(decimal::RawDecimal<'a>),
    DecimalMagnitude(u128, &'a [char], char),
}

impl Amount<'_> {
    fn render(&self) -> String {
        match self {
            Self::Integer(n) => format!("integer_part: \"{}\"", counted(*n)),
            Self::Range(a, b, negative_first, negative_second) => {
                let first = if *negative_first { "mínusz " } else { "" };
                let second = if *negative_second { "mínusz " } else { "" };
                format!(
                    "integer_part: \"{first}{} {second}{}\"",
                    cardinal::from_suffix(&counted(*a)),
                    cardinal::to_suffix(&counted(*b))
                )
            }
            Self::Magnitude(n, suffix) => {
                let mag = magnitude(*suffix);
                let word = if *n == 1 && matches!(*suffix, 'k' | 'K') {
                    mag.to_owned()
                } else {
                    format!("{}{mag}", spell(*n))
                };
                format!("integer_part: \"{word}\"")
            }
            Self::Decimal(body) => body.render(),
            Self::DecimalMagnitude(n, digits, suffix) => format!(
                "integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\"",
                spell(*n),
                decimal::spell_fractional_part(digits).unwrap(),
                magnitude(*suffix)
            ),
        }
    }
}

fn magnitude(suffix: char) -> &'static str {
    match suffix {
        'k' | 'K' => "ezer",
        'M' => "millió",
        _ => "milliárd",
    }
}

fn amounts<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, Amount<'a>, Cost, i32), leading: bool) {
    if matches!(text.get(start), Some('-' | '−')) {
        for (end, a) in cardinal::numbers(text, start + 1) {
            if !matches!(text.get(end), Some('-' | '–' | '—')) {
                continue;
            }
            let second = end + 1;
            let neg_second = matches!(text.get(second), Some('-' | '−'));
            for (e, b) in cardinal::numbers(text, second + usize::from(neg_second)) {
                emit(e, Amount::Range(a, b, true, neg_second), cardinal::number_cost(a) + cardinal::number_cost(b), 1);
                if matches!(text.get(e), Some(',' | '.')) {
                    let mut z = e + 1;
                    while text.get(z) == Some(&'0') {
                        z += 1;
                        emit(
                            z,
                            Amount::Range(a, b, true, neg_second),
                            cardinal::number_cost(a) + cardinal::number_cost(b),
                            1,
                        );
                    }
                }
            }
        }
        return;
    }
    for (end, n) in cardinal::numbers(text, start) {
        let w = cardinal::number_cost(n);
        emit(end, Amount::Integer(n), w, 5);
        if matches!(text.get(end), Some(',' | '.')) {
            let mut e = end + 1;
            while text.get(e) == Some(&'0') {
                e += 1;
                emit(e, Amount::Integer(n), w, 5);
            }
        }
        if matches!(text.get(end), Some('-' | '–' | '—')) {
            let second = end + 1;
            let neg_second = matches!(text.get(second), Some('-' | '−'));
            let n2 = second + usize::from(neg_second);
            for (e, b) in cardinal::numbers(text, n2) {
                emit(e, Amount::Range(n, b, false, neg_second), w + cardinal::number_cost(b), 1);
                if matches!(text.get(e), Some(',' | '.')) {
                    let mut z = e + 1;
                    while text.get(z) == Some(&'0') {
                        z += 1;
                        emit(z, Amount::Range(n, b, false, neg_second), w + cardinal::number_cost(b), 1);
                    }
                }
            }
        }
        if text[start..end].iter().all(|c| c.is_ascii_digit()) && n != 0 {
            let s = spaces(text, end);
            if let Some(&suffix @ ('k' | 'K' | 'M' | 'B')) = text.get(s) {
                emit(s + 1, Amount::Magnitude(n, suffix), w, 2);
            }
        }
    }
    let mut digit_end = start;
    while text.get(digit_end).is_some_and(|c| c.is_ascii_digit()) {
        digit_end += 1;
    }
    decimal::parse_raw_money_decimal(text, start, |candidate| {
        let accept = if leading {
            if candidate.quantity.is_some() {
                // decimal_with_quantity begins with an ungrouped run of digits.
                candidate.separator.is_none() || matches!(text.get(digit_end), Some(',' | '.'))
            } else if let Some(digits) = candidate.fractional_digits {
                matches!(text.get(digit_end), Some(',' | '.'))
                    && digits.len() >= 3
                    && digits[2] != '0'
                    && digits[3..].iter().all(|c| *c == '0')
            } else {
                false
            }
        } else {
            candidate.separator != Some('.') || candidate.fractional_digits.is_some_and(|digits| digits.len() != 3)
        };
        if accept {
            emit(candidate.end, Amount::Decimal(candidate), candidate.cost, 3);
        }
    });
    // Decimal shorthand uses the decimal graph's fractional transducer, followed by a magnitude.
    let sep = digit_end;
    if sep > start && matches!(text.get(sep), Some(',' | '.')) {
        let mut end = sep + 1;
        while text.get(end).is_some_and(|c| c.is_ascii_digit()) {
            end += 1;
            let s = spaces(text, end);
            if let Some(&suffix @ ('k' | 'K' | 'M' | 'B')) = text.get(s) {
                let digits = &text[sep + 1..end];
                let significant =
                    digits.iter().rposition(|c| *c != '0').map_or(if digits.len() >= 2 { 2 } else { 0 }, |i| i + 1);
                if significant > 0
                    && significant < 24
                    && !(significant >= 3 && digits[0] == '0' && digits[1] == '0')
                    && sep - start <= 24
                    && (sep - start == 1 || text[start] != '0')
                {
                    let n = text[start..sep].iter().fold(0u128, |v, c| v * 10 + (*c as u8 - b'0') as u128);
                    let fraction = digits[..significant].iter().fold(0u128, |v, c| v * 10 + (*c as u8 - b'0') as u128);
                    emit(
                        s + 1,
                        Amount::DecimalMagnitude(n, digits, suffix),
                        cardinal::number_cost(n) + cardinal::number_cost(fraction),
                        3,
                    );
                }
            }
        }
    }
}

fn minor_amounts(text: &[char], start: usize, mut emit: impl FnMut(usize, Option<u128>, u128, Cost)) {
    let mut sep = start;
    while text.get(sep).is_some_and(|c| c.is_ascii_digit()) {
        sep += 1;
    }
    if sep == start || !matches!(text.get(sep), Some(',' | '.')) {
        return;
    }
    if sep - start > 24 || (sep - start > 1 && text[start] == '0') {
        return;
    }
    let n = text[start..sep].iter().fold(0u128, |v, c| v * 10 + (*c as u8 - b'0') as u128);
    let mut end = sep + 1;
    while text.get(end).is_some_and(|c| c.is_ascii_digit()) {
        end += 1;
        let digits = &text[sep + 1..end];
        let Some(last) = digits.iter().rposition(|c| *c != '0') else { continue };
        let significant = &digits[..=last];
        let value = match significant {
            [a] => (*a as u8 - b'0') as u128 * 10,
            ['0', b] => (*b as u8 - b'0') as u128,
            [a, b] if *a != '0' => ((*a as u8 - b'0') as u128) * 10 + (*b as u8 - b'0') as u128,
            _ => continue,
        };
        emit(end, if n == 0 { None } else { Some(n) }, value, cardinal::number_cost(n));
    }
}

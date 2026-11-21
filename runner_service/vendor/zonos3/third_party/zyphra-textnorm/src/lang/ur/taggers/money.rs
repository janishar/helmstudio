//! `ur/taggers/money.py`: productive amounts, currencies, ranges and per-unit suffixes.

use super::{Sink, cardinal};
use crate::lang::common::field;
use crate::lang::ur::lazy;
use crate::text::{Cost, cost};
use std::collections::HashSet;
use std::sync::LazyLock;

static CURRENCY_STARTS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    let mut first = HashSet::new();
    for data in [
        include_str!("../../../../data/ur/money/currency.tsv"),
        include_str!("../../../../data/ur/money/currency_singular.tsv"),
    ] {
        for (key, _) in data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t')) {
            if let Some(c) = key.chars().next() {
                first.insert(c);
            }
        }
    }
    first
});

static ASCII_CURRENCY_STARTS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut starts = [false; 128];
    for &c in CURRENCY_STARTS.iter() {
        if c.is_ascii() {
            starts[c as usize] = true;
        }
    }
    starts
});

fn currency_start(c: char) -> bool {
    if c.is_ascii() { ASCII_CURRENCY_STARTS[c as usize] } else { CURRENCY_STARTS.contains(&c) }
}

pub(crate) fn possible_start(first: char) -> bool {
    first.is_ascii_digit() || currency_start(first)
}

/// Before the first currency key, every branch contains only an amount or
/// range separator. Stop at the first character outside that source alphabet.
pub(crate) fn reachable_currency(text: &[char], start: usize) -> bool {
    for pos in start..text.len() {
        let c = text[pos];
        if currency_start(c)
            && (CURRENCIES.prefix_matches(text, pos).next().is_some()
                || SINGULAR.prefix_matches(text, pos).next().is_some())
        {
            return true;
        }
        if !c.is_ascii_digit() && !matches!(c, ',' | '.' | ' ' | '-' | '–' | 'k' | 'K' | 'M' | 'm' | 'B' | 'b' | 'T')
        {
            return false;
        }
    }
    false
}

crate::tsv!(CURRENCIES, "ur/money/currency.tsv");
crate::tsv!(SINGULAR, "ur/money/currency_singular.tsv");
crate::tsv!(MINOR, "ur/money/major_minor_currencies.tsv");

const PER_UNITS: &[(&str, &str)] = &[
    ("h", "فی گھنٹہ"),
    ("hr", "فی گھنٹہ"),
    ("hrs", "فی گھنٹہ"),
    ("hour", "فی گھنٹہ"),
    ("min", "فی منٹ"),
    ("s", "فی سیکنڈ"),
    ("sec", "فی سیکنڈ"),
    ("d", "فی دن"),
    ("day", "فی دن"),
    ("wk", "فی ہفتہ"),
    ("week", "فی ہفتہ"),
    ("mo", "فی مہینہ"),
    ("month", "فی مہینہ"),
    ("yr", "فی سال"),
    ("y", "فی سال"),
    ("year", "فی سال"),
    ("kg", "فی کلو گرام"),
    ("g", "فی گرام"),
    ("mg", "فی ملی گرام"),
    ("t", "فی ٹن"),
    ("lb", "فی پونڈ"),
    ("oz", "فی اونس"),
    ("L", "فی لیٹر"),
    ("l", "فی لیٹر"),
    ("mL", "فی ملی لیٹر"),
    ("ml", "فی ملی لیٹر"),
    ("m", "فی میٹر"),
    ("km", "فی کلومیٹر"),
    ("m²", "فی مربع میٹر"),
    ("m2", "فی مربع میٹر"),
    ("m³", "فی کیوبک میٹر"),
    ("m3", "فی کیوبک میٹر"),
    ("kWh", "فی کلو واٹ آور"),
    ("unit", "فی یونٹ"),
];

#[derive(Clone, Copy)]
struct Integer<'a> {
    raw: &'a [char],
    cost: Cost,
}

#[derive(Clone, Copy)]
struct Short<'a> {
    integer: Integer<'a>,
    fractional: Option<&'a [char]>,
    magnitude: Option<&'static str>,
}

#[derive(Clone, Copy)]
enum Amount<'a> {
    Major(Integer<'a>),
    Minor { integer: Integer<'a>, digits: [char; 2], len: usize, cost: Cost },
    Decimal { integer: Integer<'a>, fractional: &'a [char] },
    Shorthand(Short<'a>),
    Range(Short<'a>, Short<'a>),
}

impl Amount<'_> {
    fn cost(self) -> Cost {
        match self {
            Self::Major(integer) | Self::Decimal { integer, .. } => integer.cost,
            Self::Minor { integer, cost, .. } => integer.cost + cost,
            Self::Shorthand(number) => number.integer.cost,
            Self::Range(first, second) => first.integer.cost + second.integer.cost,
        }
    }
}

fn one_space(text: &[char], pos: usize) -> usize {
    pos + usize::from(text.get(pos) == Some(&' '))
}

fn prefix(text: &[char], start: usize, key: &str) -> Option<usize> {
    let mut end = start;
    for c in key.chars() {
        if text.get(end) != Some(&c) {
            return None;
        }
        end += 1;
    }
    Some(end)
}

fn magnitude(text: &[char], pos: usize) -> Option<(usize, &'static str)> {
    let pos = one_space(text, pos);
    let word = match text.get(pos)? {
        'k' | 'K' => "ہزار",
        'M' | 'm' => "ملین",
        'B' | 'b' => "ارب",
        'T' => "ٹریلین",
        _ => return None,
    };
    Some((pos + 1, word))
}

fn integers<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, Integer<'a>)) {
    if !text.get(start).is_some_and(char::is_ascii_digit) {
        return;
    }
    let mut end = start;
    let mut grouped = false;
    while let Some(&c) = text.get(end) {
        if !c.is_ascii_digit() && c != ',' {
            break;
        }
        grouped |= c == ',';
        end += 1;
        if !c.is_ascii_digit() {
            continue;
        }
        let raw = &text[start..end];
        if !grouped && raw.len() > 1 && raw[0] == '0' {
            // The source leading-zero graph is digitwise, unbounded, and weighted 0.5.
            emit(end, Integer { raw, cost: cost(0.5) });
        } else {
            lazy::with_build(false, || {
                cardinal::enumerate_cardinal(raw, &mut |_, cost| emit(end, Integer { raw, cost }));
            });
        }
    }
}

fn shorts<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, Short<'a>)) {
    integers(text, start, |integer_end, integer| {
        let number = Short { integer, fractional: None, magnitude: None };
        emit(integer_end, number);
        if let Some((end, word)) = magnitude(text, integer_end) {
            emit(end, Short { magnitude: Some(word), ..number });
        }
        if text.get(integer_end) != Some(&'.') {
            return;
        }
        let begin = integer_end + 1;
        let mut end = begin;
        while text.get(end).is_some_and(char::is_ascii_digit) {
            end += 1;
            let number = Short { integer, fractional: Some(&text[begin..end]), magnitude: None };
            emit(end, number);
            if let Some((mag_end, word)) = magnitude(text, end) {
                emit(mag_end, Short { magnitude: Some(word), ..number });
            }
        }
    });
}

fn amounts<'a>(text: &'a [char], start: usize, singular: bool, mut emit: impl FnMut(usize, Amount<'a>, Cost)) {
    integers(text, start, |integer_end, integer| {
        if singular && integer.raw != ['1'] {
            return;
        }
        let adjustment = if singular { cost(-0.001) } else { 0 };
        emit(integer_end, Amount::Major(integer), adjustment);
        if !singular {
            if let Some((end, word)) = magnitude(text, integer_end) {
                emit(end, Amount::Shorthand(Short { integer, fractional: None, magnitude: Some(word) }), cost(-0.0005));
            }
        }
        if text.get(integer_end) != Some(&'.') {
            return;
        }
        let begin = integer_end + 1;
        let mut end = begin;
        let mut significant = 0;
        while let Some(&c) = text.get(end).filter(|c| c.is_ascii_digit()) {
            end += 1;
            if c != '0' {
                significant = end - begin;
            }
            let fractional = &text[begin..end];
            if significant == 0 {
                emit(end, Amount::Major(integer), adjustment);
            } else if significant <= 2 {
                let (digits, len) = if significant == 1 {
                    ([fractional[0], '0'], 2)
                } else if fractional[0] == '0' {
                    ([fractional[1], '0'], 1)
                } else {
                    ([fractional[0], fractional[1]], 2)
                };
                lazy::with_build(false, || {
                    cardinal::enumerate_cardinal(&digits[..len], &mut |_, cost| {
                        emit(end, Amount::Minor { integer, digits, len, cost }, adjustment);
                    });
                });
            } else if !singular {
                emit(end, Amount::Decimal { integer, fractional }, cost(-0.0005));
            }
            if !singular {
                if let Some((mag_end, word)) = magnitude(text, end) {
                    emit(
                        mag_end,
                        Amount::Shorthand(Short { integer, fractional: Some(fractional), magnitude: Some(word) }),
                        cost(-0.0005),
                    );
                }
            }
        }
    });
}

fn spoken_integer(integer: Integer<'_>) -> String {
    let mut found = None;
    cardinal::enumerate_cardinal(integer.raw, &mut |word, cost| {
        if found.is_none() && cost == integer.cost {
            found = Some(word);
        }
    });
    found.expect("recognized Urdu cardinal reading must render")
}

fn append_digits(out: &mut String, digits: &[char]) {
    for (i, &c) in digits.iter().enumerate() {
        if i != 0 {
            out.push(' ');
        }
        out.push_str(cardinal::digit(c).expect("recognized ASCII digit"));
    }
}

fn spoken_short(number: Short<'_>) -> String {
    let mut out = spoken_integer(number.integer);
    if let Some(fractional) = number.fractional {
        out.push_str(" اعشاریہ ");
        append_digits(&mut out, fractional);
    }
    if let Some(word) = number.magnitude {
        out.push(' ');
        out.push_str(word);
    }
    out
}

fn add(out: &mut String, key: &str, value: &str) {
    out.push(' ');
    field(out, key, value);
}

fn tag(
    amount: Amount<'_>,
    currency: &str,
    minor: Option<&str>,
    leading: bool,
    negative: bool,
    per: Option<&str>,
) -> String {
    let mut out = String::from("money {");
    if negative {
        add(&mut out, "negative", "true");
    }
    if leading {
        add(&mut out, "currency_maj", currency);
    }
    match amount {
        Amount::Major(integer) => add(&mut out, "integer_part", &spoken_integer(integer)),
        Amount::Minor { integer, digits, len, cost } => {
            add(&mut out, "integer_part", &spoken_integer(integer));
            add(&mut out, "fractional_part", &spoken_integer(Integer { raw: &digits[..len], cost }));
        }
        Amount::Decimal { integer, fractional } => {
            add(&mut out, "integer_part", &spoken_integer(integer));
            out.push_str(" fractional_part: \"");
            append_digits(&mut out, fractional);
            out.push('"');
        }
        Amount::Shorthand(number) => add(&mut out, "integer_part", &spoken_short(number)),
        Amount::Range(first, second) => {
            let mut value = spoken_short(first);
            value.push_str(" سے ");
            value.push_str(&spoken_short(second));
            add(&mut out, "integer_part", &value);
        }
    }
    if !leading {
        add(&mut out, "currency_maj", currency);
    }
    if let Some(minor) = minor {
        add(&mut out, "currency_min", minor);
    }
    if let Some(per) = per {
        add(&mut out, "morphosyntactic_features", per);
    }
    out.push_str(" }");
    out
}

fn emit(
    text: &[char],
    end: usize,
    amount: Amount<'_>,
    currency: &str,
    leading: bool,
    negative: bool,
    adjustment: Cost,
    sink: &mut Sink,
) {
    let minor = if matches!(amount, Amount::Minor { .. }) {
        let Some(row) = MINOR.exact(currency).last() else { return };
        Some(row.value.as_str())
    } else {
        None
    };
    let weight = cost(1.1) + amount.cost() + adjustment;
    sink.push(end, weight, 0, || lazy::with_build(true, || tag(amount, currency, minor, leading, negative, None)));
    let slash = one_space(text, end);
    if text.get(slash) != Some(&'/') {
        return;
    }
    let start = one_space(text, slash + 1);
    for &(key, spoken) in PER_UNITS {
        if let Some(end) = prefix(text, start, key) {
            sink.push(end, weight + cost(-0.01), 0, || {
                lazy::with_build(true, || tag(amount, currency, minor, leading, negative, Some(spoken)))
            });
        }
    }
}

fn dash(text: &[char], pos: usize) -> Option<usize> {
    if text.get(pos..pos + 3).is_some_and(|s| matches!(s, [' ', '-' | '–', ' '])) {
        Some(pos + 3)
    } else if matches!(text.get(pos), Some('-' | '–')) {
        Some(pos + 1)
    } else {
        None
    }
}

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() {
        return;
    }
    let negative = text[start] == '-';
    let begin = start + usize::from(negative);
    for (table, singular) in [(&*CURRENCIES, false), (&*SINGULAR, true)] {
        for (currency, end) in table.prefix_matches(text, begin) {
            amounts(text, one_space(text, end), singular, |end, amount, adjustment| {
                emit(text, end, amount, &currency.value, true, negative, adjustment, sink);
            });
        }
        amounts(text, begin, singular, |end, amount, adjustment| {
            for (currency, end) in table.prefix_matches(text, one_space(text, end)) {
                emit(text, end, amount, &currency.value, false, negative, adjustment, sink);
            }
        });
    }
    if negative {
        return;
    }
    // Both leading endpoints use the same major-name equivalence class.
    // A currency on the left alone is legal only for nonalphabetic signs.
    for (currency, end) in CURRENCIES.prefix_matches(text, start) {
        shorts(text, one_space(text, end), |end, first| {
            let Some(second_start) = dash(text, end) else { return };
            for (second_currency, end) in CURRENCIES.prefix_matches(text, second_start) {
                if second_currency.value != currency.value {
                    continue;
                }
                shorts(text, one_space(text, end), |end, second| {
                    emit(text, end, Amount::Range(first, second), &currency.value, false, false, cost(-0.01), sink);
                });
            }
            if !currency.key.chars().all(char::is_alphabetic) {
                shorts(text, second_start, |end, second| {
                    emit(text, end, Amount::Range(first, second), &currency.value, false, false, cost(-0.01), sink);
                });
            }
        });
    }
    shorts(text, start, |end, first| {
        let Some(second_start) = dash(text, end) else { return };
        shorts(text, second_start, |end, second| {
            for (currency, end) in CURRENCIES.prefix_matches(text, one_space(text, end)) {
                emit(text, end, Amount::Range(first, second), &currency.value, false, false, cost(-0.01), sink);
            }
        });
    });
}

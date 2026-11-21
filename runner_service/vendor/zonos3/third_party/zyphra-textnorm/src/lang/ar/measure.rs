//! Arabic measure classifier and verbalizer.
//! Direct composition of the Arabic measure and numeric graphs.

use super::cardinal::{self, parse_cardinal_span, spell_cardinal};
use super::char_util::{token_end, token_start};
use super::{decimal, fraction};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::{Cost, Row, Table, cost};
use std::collections::HashSet;
use std::sync::LazyLock;

crate::tsv!(MEASUREMENTS, "ar/measure/measurements.tsv");

// Every generated unit starts with a base unit's initial or '/'.
static UNIT_INITIALS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    let mut initials = HashSet::from(['/']);
    initials.extend(MEASUREMENTS.rows().iter().filter_map(|row| row.key.chars().next()));
    initials
});

static UNITS: LazyLock<Table> = LazyLock::new(|| {
    let base = MEASUREMENTS.rows();
    let mut rows = Vec::new();
    let mut add = |key: String, value: String| rows.push(Row { key, value, cost: 0 });
    for a in base {
        let av = a.value.replace(' ', "\u{a0}");
        add(a.key.clone(), av.clone());
        add(format!("/{}", a.key), format!("في\u{a0}{av}"));
        add(format!("{}/", a.key), format!("{av}\u{a0}في"));
        for b in base {
            let bv = b.value.replace(' ', "\u{a0}");
            add(format!("{}/{}", a.key, b.key), format!("{av}\u{a0}في\u{a0}{bv}"));
            add(format!("{}{}/", a.key, b.key), format!("{av}\u{a0}{bv}\u{a0}في"));
        }
    }
    Table::from_rows(rows)
});
pub struct MeasureClassifier<'a> {
    pub punct: &'a Punctuation,
}

#[derive(Clone, Copy)]
enum Number {
    Cardinal { value: u64, negative: bool },
    Decimal { end: usize, cost: Cost, rank: i32 },
    Fraction { end: usize, cost: Cost, rank: i32 },
    Range(u64, u64),
}
struct Path<'a> {
    end: usize,
    number_start: usize,
    number_end: usize,
    number: Number,
    unit: &'a str,
    prefix: bool,
    weight: Cost,
}

// Enumerate all accepted numeric prefixes. Isolating each prefix removes the
// sentence-level boundary checks from the component classifiers, as required
// when their FSTs are composed directly with a unit graph.
#[inline]
fn number_start(text: &[char], start: usize) -> bool {
    text.get(start).is_some_and(|&c| {
        c.is_ascii_digit()
            || matches!(
                c,
                '-' | '−'
                    | '.'
                    | ','
                    | '¼'
                    | '½'
                    | '¾'
                    | '⅐'
                    | '⅑'
                    | '⅒'
                    | '⅓'
                    | '⅔'
                    | '⅕'
                    | '⅖'
                    | '⅗'
                    | '⅘'
                    | '⅙'
                    | '⅚'
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
            )
    })
}

fn numbers<F: FnMut(usize, Number, Cost)>(
    text: &[char],
    start: usize,
    punct: &Punctuation,
    mut accepts_end: impl FnMut(usize) -> bool,
    mut emit: F,
) {
    if !number_start(text, start) {
        return;
    }
    let mut scratch = Vec::new();
    let mut has_decimal_separator = false;
    let mut has_fraction_marker = false;
    for end in start + 1..=text.len() {
        let ch = text[end - 1];
        has_decimal_separator |= matches!(ch, '.' | ',');
        has_fraction_marker |= matches!(
            ch,
            '/' | '¼'
                | '½'
                | '¾'
                | '⅐'
                | '⅑'
                | '⅒'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
        );
        if !ch.is_ascii_digit()
            && !matches!(
                ch,
                '-' | '−'
                    | '.'
                    | ','
                    | '/'
                    | ' '
                    | '¼'
                    | '½'
                    | '¾'
                    | '⅐'
                    | '⅑'
                    | '⅒'
                    | '⅓'
                    | '⅔'
                    | '⅕'
                    | '⅖'
                    | '⅗'
                    | '⅘'
                    | '⅙'
                    | '⅚'
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
            )
            && !"مليوناربت".contains(ch)
        {
            break;
        }
        if !accepts_end(end) {
            continue;
        }
        let slice = &text[start..end];
        let mut p = 0;
        let negative = matches!(slice.first(), Some('-' | '−'));
        if negative {
            p += 1;
            if slice.get(p) == Some(&' ') {
                p += 1;
            }
        }
        if slice[p..].iter().all(|c| c.is_ascii_digit() || *c == ',') {
            if let Some((value, weight)) = parse_cardinal_span(slice, p, slice.len(), true) {
                emit(end, Number::Cardinal { value, negative }, cost(weight));
            }
        }
        if has_decimal_separator {
            scratch.clear();
            decimal::DecimalClassifier { punct }.candidates(slice, 0, &mut scratch);
            for c in scratch.drain(..).filter(|c| c.end == slice.len()) {
                // The Arabic quantity graph consumes ASCII space, not other whitespace.
                if slice.iter().any(|c| matches!(c, '\t' | '\n' | '\r' | '\u{a0}')) {
                    continue;
                }
                let weight = c.cost - cost(1.1);
                emit(end, Number::Decimal { end: c.end, cost: c.cost, rank: c.rank }, weight);
            }
        }
        if has_fraction_marker {
            scratch.clear();
            fraction::FractionClassifier { punct }.candidates(slice, 0, &mut scratch);
            for c in scratch.drain(..).filter(|c| c.end == slice.len()) {
                let weight = c.cost - cost(1.0);
                emit(end, Number::Fraction { end: c.end, cost: c.cost, rank: c.rank }, weight);
            }
        }
    }
}

fn range_numbers<F: FnMut(usize, Number, Cost)>(text: &[char], start: usize, mut emit: F) {
    if !text.get(start).is_some_and(char::is_ascii_digit) {
        return;
    }
    for first_end in start + 1..=text.len().min(start + 6) {
        let first = &text[start..first_end];
        if !first.iter().all(char::is_ascii_digit) || (first.len() > 1 && first[0] == '0') {
            continue;
        }
        let Some((a, wa)) = parse_cardinal_span(text, start, first_end, false) else { continue };
        let second = if matches!(text.get(first_end), Some('-' | '–')) {
            first_end + 1
        } else if text.get(first_end) == Some(&' ')
            && matches!(text.get(first_end + 1), Some('-' | '–'))
            && text.get(first_end + 2) == Some(&' ')
        {
            first_end + 3
        } else {
            continue;
        };
        for end in second + 1..=text.len().min(second + 6) {
            let s = &text[second..end];
            if !s.iter().all(char::is_ascii_digit) || (s.len() > 1 && s[0] == '0') {
                continue;
            }
            if let Some((b, wb)) = parse_cardinal_span(text, second, end, false) {
                emit(end, Number::Range(a, b), cost(wa + wb));
            }
        }
    }
}

fn paths<'a, F: FnMut(Path<'a>)>(text: &'a [char], start: usize, punct: &Punctuation, mut emit: F) {
    // Number followed by a lexical unit, including ranges.
    let mut suffix = |end: usize, number: Number, weight: Cost| {
        for unit_start in [end, end + 1] {
            if unit_start > end && text.get(end) != Some(&' ') {
                continue;
            }
            for (unit, unit_end) in UNITS.prefix_matches(text, unit_start) {
                if token_end(text, unit_end, punct) {
                    emit(Path {
                        end: unit_end,
                        number_start: start,
                        number_end: end,
                        number,
                        unit: &unit.value,
                        prefix: false,
                        weight,
                    });
                }
            }
        }
    };
    numbers(
        text,
        start,
        punct,
        |end| {
            text.get(end).is_some_and(|c| UNIT_INITIALS.contains(c))
                || (text.get(end) == Some(&' ') && text.get(end + 1).is_some_and(|c| UNIT_INITIALS.contains(c)))
        },
        &mut suffix,
    );
    range_numbers(text, start, &mut suffix);
    drop(suffix);

    // Lexical unit before number: the literal unsigned input "1" is excluded.
    for (unit, unit_end) in UNITS.prefix_matches(text, start) {
        for number_start in [unit_end, unit_end + 1] {
            if number_start > unit_end && text.get(unit_end) != Some(&' ') {
                continue;
            }
            let mut prefix = |end: usize, number: Number, weight: Cost| {
                if token_end(text, end, punct)
                    && !(matches!(number, Number::Cardinal { .. }) && &text[number_start..end] == ['1'])
                {
                    emit(Path { end, number_start, number_end: end, number, unit: &unit.value, prefix: true, weight });
                }
            };
            numbers(text, number_start, punct, |end| token_end(text, end, punct), &mut prefix);
            range_numbers(text, number_start, &mut prefix);
        }
    }

    // Unlexicalized ASCII alpha compounds and multiplication suffixes.
    numbers(
        text,
        start,
        punct,
        |end| matches!(text.get(end), Some('x' | 'X' | '-')),
        |end, number, weight| {
            let is_card = matches!(number, Number::Cardinal { negative: false, .. })
                && parse_cardinal_span(text, start, end, false).is_some();
            let is_decimal = matches!(number, Number::Decimal { .. });
            if !is_card && !is_decimal {
                return;
            }
            if matches!(text.get(end), Some('x' | 'X')) && token_end(text, end + 1, punct) {
                let unit = if text[end] == 'x' { "x" } else { "X" };
                emit(Path { end: end + 1, number_start: start, number_end: end, number, unit, prefix: false, weight });
            }
            if text.get(end) != Some(&'-') {
                return;
            }
            let mut alpha_end = end + 1;
            let mut lower = false;
            while text.get(alpha_end).is_some_and(char::is_ascii_alphabetic) {
                lower |= text[alpha_end].is_ascii_lowercase();
                alpha_end += 1;
                if (!is_card || lower) && token_end(text, alpha_end, punct) {
                    // Alpha is copied from the input at materialization.
                    emit(Path {
                        end: alpha_end,
                        number_start: start,
                        number_end: end,
                        number,
                        unit: "",
                        prefix: false,
                        weight,
                    });
                }
            }
        },
    );
    let mut alpha_end = start;
    let mut lower = false;
    while text.get(alpha_end).is_some_and(char::is_ascii_alphabetic) {
        lower |= text[alpha_end].is_ascii_lowercase();
        alpha_end += 1;
        if text.get(alpha_end) != Some(&'-') {
            continue;
        }
        let number_start = alpha_end + 1;
        numbers(
            text,
            number_start,
            punct,
            |end| token_end(text, end, punct),
            |end, number, weight| {
                let is_card = lower
                    && matches!(number, Number::Cardinal { negative: false, .. })
                    && parse_cardinal_span(text, number_start, end, false).is_some();
                if token_end(text, end, punct) && (is_card || matches!(number, Number::Decimal { .. })) {
                    emit(Path { end, number_start, number_end: end, number, unit: "", prefix: true, weight });
                }
            },
        );
    }
}

impl Classifier for MeasureClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }
        if !number_start(text, start)
            && !text.get(start).is_some_and(|c| c.is_ascii_alphabetic() || UNIT_INITIALS.contains(c))
        {
            return;
        }
        let base = out.len();
        paths(text, start, self.punct, |path| {
            let rank = out[base..].iter().filter(|c| c.end == path.end).count() as i32;
            out.push(Candidate { end: path.end, cost: cost(1.0) + path.weight, rank, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut rank = 0;
        paths(&text[..candidate.end], start, self.punct, |path| {
            if path.end != candidate.end {
                return;
            }
            if rank == candidate.rank {
                let number = match path.number {
                    Number::Cardinal { value, negative } => format!(
                        "cardinal {{ {}integer: \"{}\" }}",
                        if negative { "negative: \"true\" " } else { "" },
                        spell_cardinal(value)
                    ),
                    Number::Range(a, b) => {
                        format!("cardinal {{ integer: \"{} إلى {}\" }}", spell_cardinal(a), spell_cardinal(b))
                    }
                    Number::Decimal { end, cost, rank } => {
                        let mut c = Candidate { end, cost, rank, tag: String::new() };
                        decimal::DecimalClassifier { punct: self.punct }.materialize(
                            &text[path.number_start..path.number_end],
                            0,
                            &mut c,
                        );
                        c.tag
                    }
                    Number::Fraction { end, cost, rank } => {
                        let mut c = Candidate { end, cost, rank, tag: String::new() };
                        fraction::FractionClassifier { punct: self.punct }.materialize(
                            &text[path.number_start..path.number_end],
                            0,
                            &mut c,
                        );
                        c.tag
                    }
                };
                let copied;
                let unit = if path.unit.is_empty() {
                    copied = if path.prefix {
                        text[start..path.number_start - 1].iter().collect::<String>()
                    } else {
                        text[path.number_end + 1..path.end].iter().collect::<String>()
                    };
                    copied.as_str()
                } else {
                    path.unit
                };
                candidate.tag = if path.prefix {
                    format!("measure {{ units: \"{unit}\" {number} }}")
                } else {
                    format!("measure {{ {number} units: \"{unit}\" }}")
                };
            }
            rank += 1;
        });
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let number = if let Some(card) = f.dict("cardinal") {
        cardinal::verbalize(card)
    } else if let Some(dec) = f.dict("decimal") {
        decimal::verbalize(dec)
    } else if let Some(frac) = f.dict("fraction") {
        fraction::verbalize(frac)
    } else {
        None
    }?;
    let units = f.take("units")?;
    loop {
        if f.preserve_order() {
            continue;
        }
        if f.peek_key() == Some("field_order") {
            if f.take("field_order")?.chars().count() != 1 {
                return None;
            }
            continue;
        }
        break;
    }
    if !f.done() {
        return None;
    }
    Some(format!("{number} {units}"))
}

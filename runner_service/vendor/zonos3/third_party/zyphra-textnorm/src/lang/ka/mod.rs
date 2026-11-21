//! Georgian text normalization, ported from the production NeMo grammar.
mod amount;
mod context;
mod datetime;
mod electronic;
mod graph_utils;
mod numeric;
mod verbalize;

use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::cell::RefCell;
use std::collections::{HashMap, HashSet};
use std::hash::{BuildHasherDefault, Hasher};
use std::sync::LazyLock;

// Tables contain fixed, source-owned keys.  A small non-cryptographic hash
// avoids SipHash on every rejected lexical lookup.
struct TableHasher(u64);
impl Default for TableHasher {
    fn default() -> Self {
        Self(0xcbf29ce484222325)
    }
}
impl Hasher for TableHasher {
    fn finish(&self) -> u64 {
        self.0
    }
    fn write(&mut self, bytes: &[u8]) {
        for &byte in bytes {
            self.0 = (self.0 ^ u64::from(byte)).wrapping_mul(0x100000001b3);
        }
    }
}
type TableIndex = HashMap<&'static str, &'static str, BuildHasherDefault<TableHasher>>;
static TABLE_SOURCES: [&str; 11] = [
    include_str!("../../../data/ka/whitelist.tsv"),
    include_str!("../../../data/ka/measurements.tsv"),
    include_str!("../../../data/ka/measurements_latin.tsv"),
    include_str!("../../../data/ka/measurement_dates.tsv"),
    include_str!("../../../data/ka/currency_minor.tsv"),
    include_str!("../../../data/ka/dates/month_number.tsv"),
    include_str!("../../../data/ka/dates/month_name.tsv"),
    include_str!("../../../data/ka/time/hours.tsv"),
    include_str!("../../../data/ka/time/minutes.tsv"),
    include_str!("../../../data/ka/electronic/symbols.tsv"),
    include_str!("../../../data/ka/numbers/digit.tsv"),
];
static TABLES: LazyLock<[TableIndex; 11]> = LazyLock::new(|| {
    std::array::from_fn(|i| {
        let mut rows = TableIndex::default();
        for line in TABLE_SOURCES[i].lines().filter(|line| !line.starts_with('#')) {
            if let Some((key, rest)) = line.trim_end_matches('\r').split_once('\t') {
                rows.entry(key).or_insert_with(|| rest.split('\t').next().unwrap_or(rest));
            }
        }
        rows
    })
});

pub(crate) fn lookup(table: &'static str, key: &str) -> Option<&'static str> {
    let mut index = None;
    let mut collision = false;
    for (i, source) in TABLE_SOURCES.iter().enumerate() {
        if source.len() == table.len() {
            if index.is_some() {
                collision = true;
                break;
            }
            index = Some(i);
        }
    }
    let index = if collision {
        TABLE_SOURCES
            .iter()
            .position(|source| source.len() == table.len() && (source.as_ptr() == table.as_ptr() || *source == table))?
    } else {
        index?
    };
    TABLES[index].get(key).copied()
}
const WHITELIST: &str = include_str!("../../../data/ka/whitelist.tsv");
struct CurrencyInitials {
    ascii: [bool; 128],
    other: Vec<char>,
}
impl CurrencyInitials {
    #[inline]
    fn contains(&self, c: char) -> bool {
        if c.is_ascii() { self.ascii[c as usize] } else { self.other.binary_search(&c).is_ok() }
    }
}
static CURRENCY_INITIALS: LazyLock<CurrencyInitials> = LazyLock::new(|| {
    let mut result = CurrencyInitials { ascii: [false; 128], other: Vec::new() };
    for c in [include_str!("../../../data/ka/currency.tsv"), include_str!("../../../data/ka/currency_minor.tsv")]
        .into_iter()
        .flat_map(|table| table.lines().filter_map(|line| line.split('\t').next()?.chars().next()))
    {
        if c.is_ascii() {
            result.ascii[c as usize] = true;
        } else {
            result.other.push(c);
        }
    }
    result.other.sort_unstable();
    result.other.dedup();
    result
});
static WHITELIST_FIRST: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    WHITELIST
        .lines()
        .filter_map(|line| {
            let (key, _) = line.split_once('\t')?;
            key.split_once(' ').map(|(first, _)| first)
        })
        .collect()
});
static CURRENCY_KEYS: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    include_str!("../../../data/ka/currency.tsv")
        .lines()
        .filter_map(|line| line.split_once('\t').map(|(key, _)| key))
        .collect()
});
struct Georgian {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Georgian {
    fn code(&self) -> &'static str {
        "ka"
    }
    fn shape(&self) -> Shape {
        Shape::Separated
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict<'_>) -> Option<String> {
        verbalize::verbalize(token)
    }
}
/// Exact punctuation alphabet of the source `PunctuationFst`.
#[inline]
fn punctuation(c: char) -> bool {
    if c.is_ascii() {
        b"!#$%&'()*+,-./:;<=>?@^_`{|}~\"[]".contains(&(c as u8))
    } else {
        matches!(c, '«' | '»' | '„' | '“' | '”' | '‘' | '’' | '–' | '—' | '…' | '჻')
    }
}
#[inline]
fn boundary(c: char) -> bool {
    is_nemo_space(c) || punctuation(c)
}
struct Punctuation;
impl Classifier for Punctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if let Some(&c) = text.get(start) {
            if punctuation(c) {
                out.push(Candidate { end: start + 1, cost: cost(1.1), rank: 0, tag: format!("name: \"{c}\"") });
            }
        }
    }
}
struct Union;
#[derive(Clone, Copy)]
struct MeasureReading {
    start: usize,
    end: usize,
    cost: i64,
    split: usize,
    decimal: bool,
}
thread_local! {
    // Reused only as the current UTF-8 span, never as an input/result cache.
    static SPAN: RefCell<String> = const { RefCell::new(String::new()) };
    static MEASURES: RefCell<Vec<MeasureReading>> = const { RefCell::new(Vec::new()) };
}
fn emit(out: &mut Vec<Candidate>, end: usize, weight: f64, rank: i32, tag: String) {
    out.push(Candidate { end, cost: cost(weight), rank, tag });
}
// Only these source branches can cross whitespace after their first word:
// numeric expressions, currency prefixes, the fixed whitelist, and Roman or
// number-abbreviation contexts.  Word and electronic paths never cross it.
fn can_cross_space(first: &str) -> bool {
    if first.chars().next().is_some_and(|c| c.is_ascii_digit() || matches!(c, '-' | '−' | '+' | '(')) {
        return true;
    }
    if context::multiword_start(first) {
        return true;
    }
    WHITELIST_FIRST.contains(first) || CURRENCY_KEYS.iter().any(|key| first.starts_with(key))
}
fn simple_clock(s: &str) -> bool {
    let Some((hour, minute)) = s.split_once(':') else { return false };
    (1..=2).contains(&hour.len())
        && minute.len() == 2
        && hour.bytes().all(|b| b.is_ascii_digit())
        && minute.bytes().all(|b| b.is_ascii_digit())
        && hour.parse::<u8>().is_ok_and(|h| h < 24)
        && minute.parse::<u8>().is_ok_and(|m| m < 60)
        && !(hour == "0" && minute == "00")
}

// Only grouped numbers, clock ranges, periods, and zones can extend HH:MM
// over whitespace in this grammar.
fn clock_can_continue(tail: &[char]) -> bool {
    let first_nonspace = tail.iter().position(|&c| !is_nemo_space(c)).unwrap_or(tail.len());
    let tail = &tail[first_nonspace..];
    let Some(&first) = tail.first() else { return false };
    if first.is_ascii_digit() || matches!(first, '-' | '–' | '—' | '֊') {
        return true;
    }
    [
        "am", "AM", "a.m.", "A.M.", "Am", "pm", "PM", "p.m.", "P.M.", "Pm", "UTC", "GMT", "EST", "PST", "CET", "EET",
        "MSK",
    ]
    .iter()
    .any(|marker| marker.chars().count() <= tail.len() && marker.chars().zip(tail.iter().copied()).all(|(a, b)| a == b))
}

/// Paths on simple words, plain integers, and two-field clocks are recognized
/// without spelling numbers or constructing tags.  More expensive competing
/// paths on these spans are dominated by the candidates emitted here.
fn simple(s: &str, end: usize, out: &mut Vec<Candidate>) -> bool {
    let (signed, digits) = if let Some(rest) = s.strip_prefix(['-', '−']) { (true, rest) } else { (false, s) };
    if !digits.is_empty() && digits.bytes().all(|b| b.is_ascii_digit()) {
        let leading = digits.len() > 1 && digits.starts_with('0');
        if leading || digits.len() <= 15 {
            if !signed && digits.starts_with('0') && (9..=12).contains(&digits.len()) {
                emit(out, end, 1.08, 12000, String::new());
            }
            emit(out, end, 1.1 + if leading { 0.1 } else { 0.0 }, 6000, String::new());
            return true;
        }
    }
    if simple_clock(s) {
        emit(out, end, 1.1, 3000, String::new());
        return true;
    }
    if !s.is_empty()
        && !s.starts_with(['~', '±'])
        && !s.chars().any(|c| is_nemo_space(c) || c.is_ascii_digit() || "@.:/½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c))
    {
        let rank = if lookup(WHITELIST, s).is_some() { 0 } else { 15000 };
        emit(out, end, if rank == 0 { 1.01 } else { 100.0 }, rank, String::new());
        return true;
    }
    false
}
fn branches(s: &str, end: usize, out: &mut Vec<Candidate>, extra: f64, prefix: &str) {
    let mut push = |w: f64, r: i32, t: String| {
        let tag = if prefix.is_empty() { t } else { format!("{prefix}{t}") };
        emit(out, end, w + extra, r, tag)
    };
    if let Some(v) = lookup(WHITELIST, s) {
        let tag = if prefix.is_empty() { String::new() } else { format!("name: \"{}\"", v.replace(' ', "\u{a0}")) };
        push(1.01, 0, tag);
    }
    let mut has_digit = false;
    let mut has_space = false;
    let mut ascii_space = false;
    let mut has_dash = false;
    let mut has_colon = false;
    let mut has_decimal_separator = false;
    let mut has_dot = false;
    let mut has_at = false;
    let mut has_fraction = false;
    let mut has_unit_char = false;
    let mut has_currency = false;
    let mut commas = 0;
    let mut chars = 0;
    let initials = &*CURRENCY_INITIALS;
    for c in s.chars() {
        chars += 1;
        let digit = c.is_ascii_digit();
        let space = is_nemo_space(c);
        has_digit |= digit;
        has_space |= space;
        ascii_space |= c == ' ';
        has_dash |= matches!(c, '-' | '–' | '—' | '֊');
        has_colon |= c == ':';
        has_decimal_separator |= matches!(c, '.' | ',');
        has_dot |= c == '.';
        has_at |= c == '@';
        has_fraction |= matches!(c, '/' | '⁄' | '½' | '¼' | '¾' | '\u{2150}'..='\u{215e}');
        has_unit_char |= !digit && !space && !matches!(c, '-' | '−' | '.' | ',' | '–' | '—' | '֊');
        has_currency |= initials.contains(c);
        commas += usize::from(c == ',');
    }
    if has_digit && context::may_composite(s) {
        for (t, w) in context::composite(s) {
            push(1.02 + w, 1000, t);
        }
    }
    if ascii_space && !s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        for (t, w) in context::roman(s) {
            push(1.04 + w, 2000, t);
        }
    }
    if has_digit {
        if has_colon {
            for t in datetime::time_tags(s) {
                push(1.1, 3000, t);
            }
        }
        if has_decimal_separator || has_space {
            if prefix.is_empty() {
                if let Some(w) = numeric::decimal_cost(s) {
                    push(1.1 + w, 4000, String::new());
                }
            } else if let Some((body, w)) = numeric::decimal_body_weight(s) {
                push(1.1 + w, 4000, format!("decimal {{ {body} }}"));
            }
        }
        if has_unit_char {
            if prefix.is_empty() {
                amount::measure_costs(s, |split, decimal, w| {
                    let weight = 0.9 + w;
                    MEASURES.with_borrow_mut(|readings| {
                        readings.push(MeasureReading { start: end - chars, end, cost: cost(weight), split, decimal })
                    });
                    push(weight, 5000, String::new());
                });
            } else {
                for (t, w) in amount::measures(s) {
                    push(0.9 + w, 5000, t);
                }
            }
        }
        if s.chars().next().is_some_and(|c| c.is_ascii_digit() || matches!(c, '-' | '−')) {
            let unsigned = s.strip_prefix(['-', '−']).unwrap_or(s);
            if prefix.is_empty() && (s.starts_with(['-', '−']) || commas < 2) {
                if let Some(w) = numeric::all_number_cost(unsigned) {
                    push(1.1 + w, 6000, String::new());
                } else if numeric::may_cardinal(s, has_dash) {
                    for (t, w) in numeric::cardinal_tag(s) {
                        push(1.1 + w, 6000, t);
                    }
                }
            } else if numeric::may_cardinal(s, has_dash) {
                for (t, w) in numeric::cardinal_tag(s) {
                    push(1.1 + w, 6000, t);
                }
            }
            if has_dash {
                for t in numeric::ordinal_tags(s) {
                    push(1.05, 7000, t);
                }
            }
        }
    }
    if has_fraction {
        for (t, w) in numeric::fraction_tags(s) {
            push(1.09 + w, 8000, t);
        }
    }
    if has_digit {
        if has_currency {
            if amount::may_be_money(s) {
                for (t, w) in amount::money(s) {
                    push(1.05 + w, 9000, t);
                }
            }
            if has_dash {
                for (t, w) in amount::money_range(s) {
                    push(1.06 + w, 10000, t);
                }
            }
        }
        if datetime::may_be_date(s) {
            for t in datetime::date_tags(s) {
                push(1.07, 11000, t);
            }
        }
        if s.starts_with(['+', '0', '(']) {
            for t in electronic::telephone(s) {
                push(1.08, 12000, t);
            }
        }
    }
    if has_dot || has_at || s.starts_with("http://") || s.starts_with("https://") {
        for t in electronic::electronic(s) {
            push(1.11, 13000, t);
        }
    }
    if has_digit && !has_space {
        for (t, w) in context::serial_tags(s) {
            push(1.5 + w, 14000, t);
        }
    }
    if !s.is_empty() && !has_space {
        let tag = if prefix.is_empty() { String::new() } else { format!("name: \"{s}\"") };
        push(100.0, 15000, tag);
    }
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            MEASURES.with_borrow_mut(Vec::clear);
        }
        if start != 0 && !boundary(text[start - 1]) {
            return;
        }
        SPAN.with_borrow_mut(|s| {
            s.clear();
            let mut first_checked = false;
            for end in start + 1..=text.len() {
                let c = text[end - 1];
                if is_nemo_space(c) && simple_clock(s) && !clock_can_continue(&text[end - 1..]) {
                    break;
                }
                // Measurement keys do not overlap currency or month keys.  The
                // only measurement key that can start a longer date is "წელი".
                if is_nemo_space(c)
                    && !s.ends_with("წელი")
                    && out.iter().any(|candidate| candidate.end == end - 1 && candidate.rank == 5000)
                {
                    break;
                }
                if is_nemo_space(c) && first_checked {
                    let rest = s.strip_prefix('~').or_else(|| s.strip_prefix('±')).unwrap_or(s.as_str());
                    let rest = rest.strip_prefix(' ').unwrap_or(rest);
                    let mut words = rest.split(is_nemo_space).filter(|word| !word.is_empty());
                    if let (Some(first), Some(second)) = (words.next(), words.next()) {
                        let numeric_start = first.starts_with(['-', '−', '+', '('])
                            || first.as_bytes().first().is_some_and(|b| b.is_ascii_digit());
                        let pending_era = matches!(
                            second,
                            "წლის"
                                | "წელს"
                                | "წ."
                                | "წწ."
                                | "წ"
                                | "წელი"
                                | "ძვ."
                                | "ახ."
                                | "ჩვ."
                        );
                        let accepted_context = out
                            .iter()
                            .any(|candidate| candidate.end == end - 1 && matches!(candidate.rank, 9000 | 11000));
                        if numeric_start
                            && !second.bytes().any(|b| b.is_ascii_digit())
                            && !pending_era
                            && !accepted_context
                        {
                            break;
                        }
                    }
                }
                if is_nemo_space(c) && !first_checked {
                    let rest = s.strip_prefix('~').or_else(|| s.strip_prefix('±')).unwrap_or(s.as_str());
                    let rest = rest.strip_prefix(' ').unwrap_or(rest);
                    if !rest.is_empty() {
                        first_checked = true;
                        if !can_cross_space(rest) {
                            break;
                        }
                    } else if !s.is_empty() && s.as_str() != "~" && s.as_str() != "±" {
                        break;
                    }
                }
                s.push(c);
                if end != text.len() && !boundary(text[end]) {
                    continue;
                }
                if !simple(s, end, out) {
                    branches(s, end, out, 0.0, "");
                }
                if let Some((p, rest)) = s
                    .strip_prefix('~')
                    .map(|s| ("დაახლოებით", s))
                    .or_else(|| s.strip_prefix('±').map(|s| ("პლუს-მინუს", s)))
                {
                    let rest = rest.strip_prefix(' ').unwrap_or(rest);
                    if !rest.is_empty() {
                        branches(rest, end, out, -2.0, &format!("name: \"{p}\" }} tokens {{ "));
                    }
                }
            }
        });
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 15000 {
            let chars = &text[start..candidate.end];
            let bytes = chars.iter().map(|c| c.len_utf8()).sum::<usize>();
            let mut tag = String::with_capacity(bytes + 8);
            tag.push_str("name: \"");
            tag.extend(chars);
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        SPAN.with_borrow_mut(|s| {
            s.clear();
            s.extend(&text[start..candidate.end]);
            candidate.tag = match candidate.rank {
                0 => lookup(WHITELIST, s)
                    .map(|v| {
                        let mut tag = String::with_capacity(v.len() + 8);
                        tag.push_str("name: \"");
                        for c in v.chars() {
                            tag.push(if c == ' ' { '\u{a0}' } else { c });
                        }
                        tag.push('"');
                        tag
                    })
                    .unwrap_or_default(),
                3000 => datetime::simple_clock_tag(s).unwrap_or_default(),
                4000 => {
                    numeric::decimal_body_weight(s).map(|(body, _)| format!("decimal {{ {body} }}")).unwrap_or_default()
                }
                5000 => MEASURES
                    .with_borrow(|readings| {
                        readings
                            .iter()
                            .find(|reading| {
                                reading.start == start && reading.end == candidate.end && reading.cost == candidate.cost
                            })
                            .and_then(|reading| amount::measure_at(s, reading.split, reading.decimal))
                    })
                    .unwrap_or_default(),
                6000 => {
                    let (sign, rest) = if let Some(rest) = s.strip_prefix(['-', '−']) {
                        ("negative: \"true\" ", rest)
                    } else {
                        ("", s.as_str())
                    };
                    let mut tag = String::with_capacity(48 + rest.len() * 3);
                    tag.push_str("cardinal { ");
                    tag.push_str(sign);
                    tag.push_str("integer: \"");
                    if graph_utils::number_into(rest, &mut tag).is_some() {
                        tag.push_str("\" }");
                        tag
                    } else {
                        String::new()
                    }
                }
                12000 => electronic::telephone(s).into_iter().next().unwrap_or_default(),
                _ => String::new(),
            };
        });
    }
}
static GRAMMAR: LazyLock<Georgian> =
    LazyLock::new(|| Georgian { classifiers: vec![Box::new(Union)], punct: Punctuation });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Cardinal classifier, ported from ru/taggers/cardinal.py.

use super::number::{
    Case, Gender, OrdinalForm, PluralCategory, SeparatorKind, cardinal, ordinal, parse_grouped_number, plural_category,
};
use super::units::QUANTITY_SUFFIXES;
use super::whitelist::LATIN_TO_CYRILLIC;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

#[derive(Clone, Copy)]
pub(super) struct NumberGraph {
    pub value: u64,
    pub form: super::units::Form,
    pub cost: Cost,
}

impl NumberGraph {
    pub fn render(self, gender: Gender, case: Case) -> String {
        cardinal(self.value, gender, case)
    }
}

pub(super) fn number_graph(s: &str, genitive: bool) -> Option<NumberGraph> {
    let (value, separator) = parse_grouped_number(s)?;
    let category = plural_category(value);
    let form = if genitive {
        if category == PluralCategory::One { super::units::Form::Gen } else { super::units::Form::Many }
    } else {
        match category {
            PluralCategory::One => super::units::Form::One,
            PluralCategory::Few => super::units::Form::Few,
            PluralCategory::Many => super::units::Form::Many,
        }
    };
    let extra = match separator {
        SeparatorKind::None => 0.0,
        SeparatorKind::Space | SeparatorKind::Nbsp => 0.01,
        SeparatorKind::Dot => 0.11,
    };
    Some(NumberGraph { value, form, cost: cost(extra) })
}

pub(super) fn range_graph(s: &str) -> Option<NumberGraph> {
    if s.is_empty() || s.len() > 4 || s.starts_with('0') || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    number_graph(s, true)
}

pub(super) const GENITIVE_PREPOSITIONS: &[&str] = &[
    "около",
    "Около",
    "более",
    "Более",
    "менее",
    "Менее",
    "больше",
    "Больше",
    "меньше",
    "Меньше",
    "свыше",
    "Свыше",
    "порядка",
    "Порядка",
    "до",
    "До",
    "от",
    "От",
];

const MATH_VAR_MAP: &[(&str, &str)] = &[
    ("x", "икс"),
    ("X", "икс"),
    ("y", "игрек"),
    ("Y", "игрек"),
    ("z", "зет"),
    ("Z", "зет"),
    ("a", "а"),
    ("A", "а"),
    ("b", "бэ"),
    ("B", "бэ"),
    ("c", "цэ"),
    ("C", "цэ"),
    ("d", "дэ"),
    ("D", "дэ"),
    ("k", "ка"),
    ("K", "ка"),
    ("m", "эм"),
    ("M", "эм"),
    ("n", "эн"),
    ("N", "эн"),
    ("p", "пэ"),
    ("P", "пэ"),
    ("q", "ку"),
    ("Q", "ку"),
    ("r", "эр"),
    ("R", "эр"),
    ("s", "эс"),
    ("S", "эс"),
    ("t", "тэ"),
    ("T", "тэ"),
    ("u", "у"),
    ("U", "у"),
    ("v", "вэ"),
    ("V", "вэ"),
];

pub fn math_var_spoken(s: &str) -> Option<&'static str> {
    for &(var, sp) in MATH_VAR_MAP {
        if s == var {
            return Some(sp);
        }
    }
    None
}

pub fn render_cardinal_tag(spoken: &str, neg: bool) -> String {
    if neg { format!("negative: \"true\" integer: \"{spoken}\"") } else { format!("integer: \"{spoken}\"") }
}

#[derive(Clone, Copy)]
enum SerialKind {
    Model,
    Mixed,
    NumericDots,
    NumericSlashes,
}

pub(super) fn serial_alpha(c: char) -> bool {
    super::whitelist::is_ru_alpha(c) || LATIN_TO_CYRILLIC.iter().any(|(letter, _, _)| *letter == c.to_ascii_lowercase())
}

fn serial_kind(s: &str) -> Option<(SerialKind, Cost)> {
    if let Some((letters, digits)) = s.split_once('-') {
        if !letters.is_empty()
            && letters.chars().all(serial_alpha)
            && digits.len() == 2
            && !digits.starts_with('0')
            && digits.bytes().all(|b| b.is_ascii_digit())
        {
            return Some((SerialKind::Model, cost(-0.05)));
        }
    }

    let mut has_alpha = false;
    let mut has_digit = false;
    let mut numeric = true;
    let mut separators = 0usize;
    let mut dot_count = 0usize;
    let mut slash_count = 0usize;
    let mut segment_start = 0usize;
    let mut previous_separator = None;
    let mut extra = cost(0.0);
    for (i, c) in s.char_indices().chain(std::iter::once((s.len(), '\0'))) {
        if matches!(c, '.' | '-' | '/' | '\0') {
            if i == segment_start {
                return None;
            }
            let part = &s[segment_start..i];
            if previous_separator == Some('.') && part.chars().any(serial_alpha) {
                extra += cost(0.05);
            }
            if c != '\0' {
                separators += 1;
                if c == '.' {
                    dot_count += 1;
                } else {
                    slash_count += 1;
                }
                previous_separator = Some(c);
                segment_start = i + c.len_utf8();
            }
        } else if c.is_ascii_digit() {
            has_digit = true;
        } else if serial_alpha(c) {
            has_alpha = true;
            numeric = false;
        } else {
            return None;
        }
    }
    if has_alpha && has_digit {
        Some((SerialKind::Mixed, extra))
    } else if numeric && has_digit && separators >= 2 && slash_count == 0 {
        Some((SerialKind::NumericDots, cost(0.0)))
    } else if numeric && has_digit && separators >= 2 && dot_count == 0 {
        Some((SerialKind::NumericSlashes, cost(0.0)))
    } else {
        None
    }
}

fn append_word(out: &mut String, word: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(word);
}

fn append_serial_segment(out: &mut String, segment: &str, number_as_cardinal: bool) {
    if number_as_cardinal && segment.bytes().all(|b| b.is_ascii_digit()) {
        if segment.len() <= 3 && (segment == "0" || !segment.starts_with('0')) {
            let n = segment.parse::<u64>().unwrap();
            append_word(out, &cardinal(n, Gender::Masc, Case::Nom));
            return;
        }
    }
    for c in segment.chars() {
        if c.is_ascii_digit() {
            append_word(out, &cardinal((c as u8 - b'0') as u64, Gender::Masc, Case::Nom));
        } else if let Some((_, spoken, _)) = LATIN_TO_CYRILLIC.iter().find(|(ch, _, _)| *ch == c.to_ascii_lowercase()) {
            append_word(out, spoken);
        } else {
            let mut buf = [0; 4];
            append_word(out, c.encode_utf8(&mut buf));
        }
    }
}

fn render_serial_kind(s: &str, kind: SerialKind) -> String {
    if matches!(kind, SerialKind::Model) {
        let (letters, digits) = s.split_once('-').unwrap();
        let mut out = String::new();
        append_serial_segment(&mut out, letters, false);
        append_serial_segment(&mut out, digits, true);
        return out;
    }
    let mut out = String::new();
    let mut start = 0;
    let mut preceding = None;
    for (i, c) in s.char_indices().chain(std::iter::once((s.len(), '\0'))) {
        if matches!(c, '.' | '-' | '/' | '\0') {
            if preceding == Some('.') {
                append_word(&mut out, "точка");
            }
            let as_cardinal = matches!(kind, SerialKind::NumericDots) || preceding == Some('.');
            append_serial_segment(&mut out, &s[start..i], as_cardinal);
            preceding = Some(c);
            start = i + c.len_utf8();
        }
    }
    out
}

#[derive(Clone, Copy)]
enum Reading<'a> {
    Number(NumberGraph, Option<&'a super::units::Noun>, bool),
    Approx(&'a str, NumberGraph, bool, Option<&'a super::units::Noun>),
    Power(&'a str, &'a str),
    Range(NumberGraph, NumberGraph),
    List(&'a str),
    Genitive(u64),
    Serial(SerialKind),
}

fn counted_number(s: &str) -> Option<(NumberGraph, Option<&super::units::Noun>)> {
    if let Some(number) = number_graph(s, false) {
        return Some((number, None));
    }
    for &noun in QUANTITY_SUFFIXES.matching(s) {
        if let Some(prefix) = s.strip_suffix(noun.written.as_str()) {
            let prefix = prefix.strip_suffix(' ').or_else(|| prefix.strip_suffix('\u{a0}')).unwrap_or(prefix);
            if let Some(number) = number_graph(prefix, false) {
                return Some((number, Some(noun)));
            }
        }
    }
    None
}

fn math_variable(s: &str) -> Option<(&str, Cost)> {
    if let Some(word) = math_var_spoken(s) {
        return Some((word, cost(0.0)));
    }
    let mut chars = s.chars();
    let c = chars.next()?;
    if chars.next().is_some() {
        return None;
    }
    if super::whitelist::is_ru_alpha(c) {
        return Some((s, cost(0.1)));
    }
    LATIN_TO_CYRILLIC.iter().find(|(ch, _, _)| *ch == c.to_ascii_lowercase()).map(|(_, spoken, _)| (*spoken, cost(0.1)))
}

fn recognize_cardinal<'a>(s: &'a str) -> Option<(Reading<'a>, Cost, i32)> {
    let mut best: Option<(Reading<'a>, Cost, i32)> = None;
    let mut add = |reading, weight, rank| {
        if best.as_ref().is_none_or(|(_, current, _)| weight < *current) {
            best = Some((reading, weight, rank));
        }
    };
    let (negative, unsigned) = if let Some(rest) = s.strip_prefix('-') { (true, rest) } else { (false, s) };
    if let Some((number, noun)) = counted_number(unsigned) {
        add(Reading::Number(number, noun, negative), cost(0.95) + number.cost, 0);
    }
    if let Some(rest) = s.strip_prefix('~') {
        let rest = rest.strip_prefix(' ').unwrap_or(rest);
        if let Some(n) = number_graph(rest, true) {
            add(Reading::Approx("около", n, false, None), cost(0.8) + n.cost, 0);
        }
    }
    if let Some(rest) = s.strip_prefix('±') {
        let rest = rest.strip_prefix(' ').unwrap_or(rest);
        let (negative, rest) = if let Some(rest) = rest.strip_prefix('-') { (true, rest) } else { (false, rest) };
        if let Some((n, noun)) = counted_number(rest) {
            add(Reading::Approx("плюс-минус", n, negative, noun), cost(0.8) + n.cost, 0);
        }
    }
    if s.chars().next().is_some_and(super::whitelist::is_ru_alpha) {
        for &prep in GENITIVE_PREPOSITIONS {
            if let Some(rest) = s.strip_prefix(prep).and_then(|r| r.strip_prefix(' ')) {
                if let Some(n) = number_graph(rest, true) {
                    add(Reading::Approx(prep, n, false, None), cost(0.8) + n.cost, 0);
                }
            }
        }
    }
    if let Some(rest) = s.strip_prefix('№') {
        let rest = rest.strip_prefix(' ').unwrap_or(rest);
        if let Some(n) = number_graph(rest, false) {
            add(Reading::Approx("номер", n, false, None), cost(0.8) + n.cost, 0);
        }
    }
    // The remaining power, range, list, genitive and serial graphs contain
    // no whitespace; grouped and counted numbers were already considered.
    if s.contains(' ') || s.contains('\u{a0}') {
        return best;
    }
    if let Some((var, exp)) = s.split_once('^') {
        if let Some((_, weight)) = math_variable(var) {
            let digits = exp.strip_prefix('-').unwrap_or(exp);
            let valid = exp == "0"
                || (!digits.is_empty()
                    && digits.len() <= 3
                    && !digits.starts_with('0')
                    && digits.bytes().all(|b| b.is_ascii_digit()));
            if valid {
                add(Reading::Power(var, exp), cost(0.85) + weight, 0);
            }
        }
    }
    for sep in ['-', '–'] {
        if let Some((left, right)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (range_graph(left), range_graph(right)) {
                add(Reading::Range(a, b), cost(1.05), 1);
            }
        }
    }
    if s.split(',').count() >= 3 && s.split(',').all(|p| p == "0" || range_graph(p).is_some()) {
        add(Reading::List(s), cost(1.05), 1);
    }
    if let Some(digits) = s.strip_suffix("-х") {
        if !digits.is_empty()
            && digits.len() <= 3
            && digits.bytes().all(|b| b.is_ascii_digit())
            && digits.as_bytes().last().is_some_and(|b| *b != b'0')
        {
            if let Ok(n) = digits.parse() {
                add(Reading::Genitive(n), cost(1.05), 2);
            }
        }
    }
    if let Some((kind, extra)) = serial_kind(s) {
        add(Reading::Serial(kind), cost(11.05) + extra, 10);
    }
    best
}

fn render_number(n: NumberGraph, noun: Option<&super::units::Noun>, case: Case) -> String {
    let word = n.render(Gender::Masc, case);
    if let Some(noun) = noun { super::units::agree(&word, n.form, noun) } else { word }
}

fn render_reading(s: &str, reading: Reading<'_>) -> String {
    match reading {
        Reading::Number(n, noun, negative) => render_cardinal_tag(&render_number(n, noun, Case::Nom), negative),
        Reading::Approx(prefix, n, negative, noun) => {
            let case = if prefix == "плюс-минус" || prefix == "номер" { Case::Nom } else { Case::Gen };
            let sign = if negative { " минус" } else { "" };
            render_cardinal_tag(&format!("{prefix}{sign} {}", render_number(n, noun, case)), false)
        }
        Reading::Power(var, exp) => {
            let word = math_variable(var).unwrap().0;
            let power = match exp {
                "2" => "в квадрате".to_string(),
                "3" => "в кубе".to_string(),
                "0" => "в нулевой степени".to_string(),
                _ => {
                    let (sign, digits) =
                        if let Some(rest) = exp.strip_prefix('-') { ("минус ", rest) } else { ("", exp) };
                    format!("в {sign}{} степени", ordinal(digits.parse().unwrap(), OrdinalForm::FemGen))
                }
            };
            render_cardinal_tag(&format!("{word} {power}"), false)
        }
        Reading::Range(a, b) => render_cardinal_tag(
            &format!("от {} до {}", a.render(Gender::Masc, Case::Gen), b.render(Gender::Masc, Case::Gen)),
            false,
        ),
        Reading::List(values) => render_cardinal_tag(
            &values
                .split(',')
                .map(|n| cardinal(n.parse().unwrap(), Gender::Masc, Case::Nom))
                .collect::<Vec<_>>()
                .join(", "),
            false,
        ),
        Reading::Genitive(n) => render_cardinal_tag(&cardinal(n, Gender::Masc, Case::Gen), false),
        Reading::Serial(kind) => render_cardinal_tag(&render_serial_kind(s, kind), false),
    }
}

pub(super) fn span_plain_digits(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(number) = number_graph(s, false) {
        out.push(Candidate { end, cost: cost(0.95) + number.cost, rank: 0, tag: String::new() });
    }
}
pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some((_, weight, rank)) = recognize_cardinal(s) {
        out.push(Candidate { end, cost: weight, rank, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some((reading, _, _)) = recognize_cardinal(s) {
        candidate.tag = format!("cardinal {{ {} }}", render_reading(s, reading));
    }
}

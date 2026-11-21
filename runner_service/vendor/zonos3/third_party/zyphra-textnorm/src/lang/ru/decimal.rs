// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Decimal classifier, ported from ru/taggers/decimals.py.

use super::number::{Case, Gender, PluralCategory, cardinal, plural_category};
use crate::lattice::Candidate;
use crate::text::cost;

const DENOMINATORS: &[(usize, &str, &str)] = &[
    (1, "десятая", "десятых"),
    (2, "сотая", "сотых"),
    (3, "тысячная", "тысячных"),
    (4, "десятитысячная", "десятитысячных"),
    (5, "стотысячная", "стотысячных"),
    (6, "миллионная", "миллионных"),
];

pub(super) fn integer_graph(s: &str) -> Option<super::cardinal::NumberGraph> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    super::cardinal::number_graph(s, false)
}

#[derive(Clone, Copy)]
pub(super) struct FractionalGraph {
    pub value: u64,
    pub digits: usize,
    pub form: super::units::Form,
}

impl FractionalGraph {
    pub fn render(self) -> String {
        let (_, singular, plural) = DENOMINATORS[self.digits - 1];
        let denominator = if self.form == super::units::Form::One { singular } else { plural };
        format!("{} {denominator}", cardinal(self.value, Gender::Fem, Case::Nom))
    }
}

pub(super) fn fractional_graph(s: &str) -> Option<FractionalGraph> {
    if s.is_empty() || s.len() > DENOMINATORS.len() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let value: u64 = s.parse().ok()?;
    let form = match plural_category(value) {
        PluralCategory::One => super::units::Form::One,
        PluralCategory::Few => super::units::Form::Few,
        PluralCategory::Many => super::units::Form::Many,
    };
    Some(FractionalGraph { value, digits: s.len(), form })
}

pub fn render_integer_part(n: u64) -> String {
    let card_sp = cardinal(n, Gender::Fem, Case::Nom);
    let ending = if plural_category(n) == PluralCategory::One { "целая" } else { "целых" };
    format!("{card_sp} {ending}")
}

pub fn quantity_after_decimal(q_str: &str) -> Option<&'static str> {
    match q_str {
        "тыс" | "тыс." | "тысяча" | "тысячи" | "тысяч" => Some("тысячи"),
        "млн" | "млн." | "миллион" | "миллиона" | "миллионов" => Some("миллиона"),
        "млрд" | "млрд." | "миллиард" | "миллиарда" | "миллиардов" => {
            Some("миллиарда")
        }
        _ => None,
    }
}

pub struct ParsedDecimal {
    pub negative: bool,
    pub integer_part: String,
    pub fractional_part: String,
    pub quantity: Option<&'static str>,
}

struct DecimalMatch {
    negative: bool,
    integer: super::cardinal::NumberGraph,
    fractional: FractionalGraph,
    quantity: Option<&'static str>,
    is_point: bool,
}

fn recognize_decimal(s: &str) -> Option<DecimalMatch> {
    let (negative, s) = if let Some(rest) = s.strip_prefix('-') { (true, rest) } else { (false, s) };
    let (num_part, quantity) = if let Some(pos) = s.find(|c: char| !(c.is_ascii_digit() || c == ',' || c == '.')) {
        let (number, suffix) = s.split_at(pos);
        let suffix = if suffix.starts_with(crate::text::is_nemo_space) {
            let ch = suffix.chars().next()?;
            &suffix[ch.len_utf8()..]
        } else {
            suffix
        };
        (number, Some(quantity_after_decimal(suffix)?))
    } else {
        (s, None)
    };
    let (int_s, frac_s, is_point) = if let Some((i, f)) = num_part.split_once(',') {
        (i, f, false)
    } else {
        let (i, f) = num_part.split_once('.')?;
        (i, f, true)
    };
    if is_point && (int_s.len() > 3 || frac_s.len() > 2) {
        return None;
    }
    Some(DecimalMatch {
        negative,
        integer: integer_graph(int_s)?,
        fractional: fractional_graph(frac_s)?,
        quantity,
        is_point,
    })
}

pub fn parse_decimal(s: &str) -> Option<ParsedDecimal> {
    let parsed = recognize_decimal(s)?;
    Some(ParsedDecimal {
        negative: parsed.negative,
        integer_part: render_integer_part(parsed.integer.value),
        fractional_part: parsed.fractional.render(),
        quantity: parsed.quantity,
    })
}

pub fn render_decimal_tag(parsed: &ParsedDecimal) -> String {
    let mut tag = String::from("decimal { ");
    if parsed.negative {
        tag.push_str("negative: \"true\" ");
    }
    tag.push_str(&format!("integer_part: \"{}\" fractional_part: \"{}\"", parsed.integer_part, parsed.fractional_part));
    if let Some(q) = parsed.quantity {
        tag.push_str(&format!(" quantity: \"{q}\""));
    }
    tag.push_str(" }");
    tag
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(parsed) = recognize_decimal(s) {
        let mut weight = 1.05;
        if parsed.is_point {
            weight += 0.08;
        }
        if parsed.quantity.is_some() {
            weight -= 0.1;
        }
        out.push(Candidate { end, cost: cost(weight), rank: if parsed.is_point { 1 } else { 0 }, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(parsed) = parse_decimal(s) {
        candidate.tag = render_decimal_tag(&parsed);
    }
}

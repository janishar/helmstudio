// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Fraction classifier, ported from ru/taggers/fraction.py.

use super::number::{Case, Gender, OrdinalForm, PluralCategory, cardinal, ordinal, plural_category};
use crate::lattice::Candidate;
use crate::text::cost;

pub(super) const VULGAR_FRACTIONS: &[(char, u64, u64)] = &[
    ('¼', 1, 4),
    ('½', 1, 2),
    ('¾', 3, 4),
    ('⅐', 1, 7),
    ('⅑', 1, 9),
    ('⅒', 1, 10),
    ('⅓', 1, 3),
    ('⅔', 2, 3),
    ('⅕', 1, 5),
    ('⅖', 2, 5),
    ('⅗', 3, 5),
    ('⅘', 4, 5),
    ('⅙', 1, 6),
    ('⅚', 5, 6),
    ('⅛', 1, 8),
    ('⅜', 3, 8),
    ('⅝', 5, 8),
    ('⅞', 7, 8),
];

pub struct ParsedFraction {
    pub negative: bool,
    pub integer_part: Option<u64>,
    pub numerator: u64,
    pub denominator: u64,
}

pub fn render_fraction_tag(parsed: &ParsedFraction) -> String {
    let mut tag = String::from("fraction { ");
    if parsed.negative {
        tag.push_str("negative: \"true\" ");
    }
    if let Some(int_val) = parsed.integer_part {
        let int_sp = cardinal(int_val, Gender::Fem, Case::Nom);
        let ending = if plural_category(int_val) == PluralCategory::One { "целая" } else { "целых" };
        tag.push_str(&format!("integer_part: \"{int_sp} {ending}\" "));
    }

    let num_sp = cardinal(parsed.numerator, Gender::Fem, Case::Nom);
    let den_form =
        if plural_category(parsed.numerator) == PluralCategory::One { OrdinalForm::FemNom } else { OrdinalForm::PlGen };
    let den_sp = ordinal(parsed.denominator, den_form);

    tag.push_str(&format!("numerator: \"{num_sp}\" denominator: \"{den_sp}\""));
    tag.push_str(" }");
    tag
}

pub fn parse_fraction(s: &str) -> Option<ParsedFraction> {
    let (neg, s) = if let Some(rest) = s.strip_prefix('-') { (true, rest) } else { (false, s) };

    // Check vulgar fraction
    for &(vc, num, den) in VULGAR_FRACTIONS {
        if s.ends_with(vc) {
            let int_str = &s[..s.len() - vc.len_utf8()];
            let int_str = int_str.strip_suffix(' ').unwrap_or(int_str);
            let integer_part = if int_str.is_empty() && s.len() == vc.len_utf8() {
                None
            } else {
                if int_str.is_empty()
                    || int_str.len() > 6
                    || int_str.starts_with('0')
                    || !int_str.bytes().all(|b| b.is_ascii_digit())
                {
                    return None;
                }
                let v = int_str.parse::<u64>().ok()?;
                if v == 0 || v > 999_999 {
                    return None;
                }
                Some(v)
            };
            return Some(ParsedFraction { negative: neg, integer_part, numerator: num, denominator: den });
        }
    }

    // Check standard fraction with '/'
    if let Some((left, right)) = s.split_once('/') {
        if right.is_empty() || !right.bytes().all(|b| b.is_ascii_digit()) || (right.len() > 1 && right.starts_with('0'))
        {
            return None;
        }
        let den: u64 = right.parse().ok()?;
        let valid_den = (2..=999).contains(&den) || matches!(den, 1000 | 10000 | 100000 | 1000000);
        if !valid_den {
            return None;
        }

        if let Some((int_str, num_str)) = left.split_once(' ') {
            if int_str.is_empty()
                || int_str.len() > 6
                || int_str.starts_with('0')
                || !int_str.bytes().all(|b| b.is_ascii_digit())
                || num_str.is_empty()
                || num_str.len() > 3
                || (num_str.len() > 1 && num_str.starts_with('0'))
                || !num_str.bytes().all(|b| b.is_ascii_digit())
            {
                return None;
            }
            let int_val: u64 = int_str.parse().ok()?;
            let num: u64 = num_str.parse().ok()?;
            if int_val == 0 || int_val > 999_999 || num > 999 {
                return None;
            }
            return Some(ParsedFraction {
                negative: neg,
                integer_part: Some(int_val),
                numerator: num,
                denominator: den,
            });
        } else {
            if left.is_empty()
                || left.len() > 3
                || (left.len() > 1 && left.starts_with('0'))
                || !left.bytes().all(|b| b.is_ascii_digit())
            {
                return None;
            }
            let num: u64 = left.parse().ok()?;
            if num > 999 {
                return None;
            }
            return Some(ParsedFraction { negative: neg, integer_part: None, numerator: num, denominator: den });
        }
    }

    None
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if parse_fraction(s).is_some() {
        out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(parsed) = parse_fraction(s) {
        candidate.tag = render_fraction_tag(&parsed);
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese text normalization grammar, ported from NeMo vi.

pub mod graph_utils;
pub mod taggers;
pub mod verbalizers;

use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use std::sync::LazyLock;

pub struct Vi {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: taggers::punctuation::Punctuation,
}

// Keep the original classifier order and its lattice rank contribution while
// sharing starter tests and avoiding repeated lattice scratch dispatch.
struct Union {
    members: Vec<Box<dyn Classifier>>,
}

fn starts_power(text: &[char], start: usize) -> bool {
    let mut end = start;
    while end < text.len() && end - start < 4 && text[end].is_ascii_alphabetic() {
        end += 1;
        if text.get(end) == Some(&'^') {
            return true;
        }
    }
    false
}

fn starts_date_phrase(text: &[char], start: usize) -> bool {
    let rest = &text[start..];
    rest.starts_with(&['n', 'g', 'à', 'y', ' '])
        || rest.starts_with(&['n', 'ă', 'm', ' '])
        || rest.starts_with(&['t', 'h', 'á', 'n', 'g', ' '])
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let Some(&first) = text.get(start) else { return };
        let digit = first.is_ascii_digit();
        let vulgar = !first.is_ascii() && taggers::fraction::UNICODE_FRACTIONS.iter().any(|&(c, _, _)| c == first);
        let signed = matches!(first, '-' | '−');
        let date_phrase = matches!(first, 'n' | 't') && starts_date_phrase(text, start);
        let currency_prefix = !digit && !signed && taggers::money::starts_prefix(text, start);
        let eligible = [
            true,                                                                                         // whitelist
            first == '~',                       // approximation
            starts_power(text, start),          // power
            digit || signed || currency_prefix, // money
            digit || signed || vulgar || first == ',' || first == '\'' || date_phrase || currency_prefix, // range
            digit || matches!(first, '-' | ','), // decimal
            digit || first == '\'' || date_phrase, // date
            digit || first == '-',              // cardinal
            first == 't' && text[start..].starts_with(&['t', 'h', 'ứ', ' ']), // ordinal
            digit || first == '-' || vulgar,    // fraction
            digit,                              // time
            digit || first == '-' || vulgar,    // measure
            matches!(first, '0' | '(' | '+'),   // telephone
            first.is_ascii_alphanumeric(),      // electronic
            taggers::word::is_word_char(first), // word
            true,                               // contextual Roman keyword
        ];
        for (index, member) in self.members.iter().enumerate() {
            if !eligible[index] {
                continue;
            }
            let begin = out.len();
            member.candidates(text, start, out);
            for candidate in &mut out[begin..] {
                candidate.rank += (index as i32) * 1000;
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        // Every Vietnamese member emits local rank zero; the lattice used the
        // original member index as its 1000-stride global contribution.
        let index = (candidate.rank / 1000) as usize;
        candidate.rank -= (index as i32) * 1000;
        self.members[index].materialize(text, start, candidate);
    }
}

fn append_typed(out: &mut String, value: &str) {
    if !value.contains('\u{a0}') {
        out.push_str(value);
    } else {
        for c in value.chars() {
            out.push(if c == '\u{a0}' { ' ' } else { c });
        }
    }
}

impl Grammar for Vi {
    fn code(&self) -> &'static str {
        "vi"
    }

    fn shape(&self) -> Shape {
        Shape::De
    }

    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }

    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }

    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalizers::verbalize(token)
    }

    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(key, Value::Str(value))] = body.as_slice() {
                if key == "name" {
                    if value.is_empty() || (value.contains('"') && value.chars().any(crate::text::is_nemo_space)) {
                        return None;
                    }
                    for c in value.chars() {
                        out.push(if c == '\u{a0}' { ' ' } else { c });
                    }
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, Value::Str(value))] = fields.as_slice() {
                        if key == "integer" && !value.is_empty() {
                            append_typed(&mut out, value);
                            continue;
                        }
                    }
                }
                if class == "time" {
                    if let [(hours, Value::Str(h)), (minutes, Value::Str(m))] = fields.as_slice() {
                        if hours == "hours" && minutes == "minutes" && !h.is_empty() && !m.is_empty() {
                            append_typed(&mut out, h);
                            out.push_str(" giờ");
                            if m != "không" {
                                out.push(' ');
                                append_typed(&mut out, m);
                                out.push_str(" phút");
                            }
                            continue;
                        }
                    }
                }
                if class == "money" {
                    if let [(integer, Value::Str(n)), (currency, Value::Str(unit))] = fields.as_slice() {
                        if integer == "integer_part" && currency == "currency_maj" && !n.is_empty() && !unit.is_empty()
                        {
                            append_typed(&mut out, n);
                            out.push(' ');
                            append_typed(&mut out, unit);
                            continue;
                        }
                    }
                }
                if class == "measure" {
                    if let [(number, Value::Dict(sub)), (units, Value::Str(unit))] = fields.as_slice() {
                        if number == "cardinal" && units == "units" && !unit.is_empty() {
                            if let [(integer, Value::Str(n))] = sub.as_slice() {
                                if integer == "integer" && !n.is_empty() {
                                    append_typed(&mut out, n);
                                    out.push(' ');
                                    append_typed(&mut out, unit);
                                    continue;
                                }
                            }
                        }
                    }
                }
            }
            let spoken = crate::permute::first_ordering(body, &mut |ordered| self.verbalize(ordered))?;
            out.push_str(&spoken);
        }
        Some(out)
    }

    fn post_process(&self, text: String) -> String {
        verbalizers::post_processing::apply(text)
    }
}

static GRAMMAR: LazyLock<Vi> = LazyLock::new(|| {
    let members: Vec<Box<dyn Classifier>> = vec![
        Box::new(taggers::whitelist::Whitelist),
        Box::new(taggers::approx::Approx),
        Box::new(taggers::power::Power),
        Box::new(taggers::money::Money),
        Box::new(taggers::range::Range),
        Box::new(taggers::decimal::Decimal),
        Box::new(taggers::date::Date),
        Box::new(taggers::cardinal::Cardinal),
        Box::new(taggers::ordinal::Ordinal),
        Box::new(taggers::fraction::Fraction),
        Box::new(taggers::time::Time),
        Box::new(taggers::measure::Measure),
        Box::new(taggers::telephone::Telephone),
        Box::new(taggers::electronic::Electronic),
        Box::new(taggers::word::Word),
        Box::new(taggers::roman::Roman),
    ];
    let classifiers: Vec<Box<dyn Classifier>> = vec![Box::new(Union { members })];
    Vi { classifiers, punct: taggers::punctuation::Punctuation }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

//! Arabic text normalization grammar.
//!
//! Ported from the production Arabic NeMo grammar:
//! `text_normalization/ar`: `ClassifyFst` + `VerbalizeFst`.

mod cardinal;
mod char_util;
mod date;
mod decimal;
mod electronic;
mod fraction;
mod measure;
mod money;
mod ordinal;
mod range;
mod roman;
mod serial;
mod telephone;
mod time;
mod whitelist;
mod word;

use std::sync::LazyLock;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::cost;

use self::char_util::Prefixed;

static PUNCT: LazyLock<Punctuation> = LazyLock::new(|| Punctuation::en(cost(1.1)));

pub struct Arabic {
    classifiers: Vec<Box<dyn Classifier>>,
}

impl Grammar for Arabic {
    fn code(&self) -> &'static str {
        "ar"
    }

    fn shape(&self) -> Shape {
        Shape::SeparatedWeighted(cost(1.1))
    }

    fn pre_classify<'t>(&self, text: &'t [char]) -> std::borrow::Cow<'t, [char]> {
        use std::borrow::Cow;
        if !text.iter().any(|c| matches!(c, '٠'..='٩' | '۰'..='۹' | '٫' | '٬')) {
            return Cow::Borrowed(text);
        }
        let mut normalized = text.to_vec();
        for c in &mut normalized {
            *c = match *c {
                '٠'..='٩' => char::from_u32('0' as u32 + *c as u32 - '٠' as u32).unwrap(),
                '۰'..='۹' => char::from_u32('0' as u32 + *c as u32 - '۰' as u32).unwrap(),
                other => other,
            };
        }
        for i in 1..normalized.len().saturating_sub(1) {
            if normalized[i - 1].is_ascii_digit() && normalized[i + 1].is_ascii_digit() {
                normalized[i] = match normalized[i] {
                    '٫' => '.',
                    '٬' => ',',
                    other => other,
                };
            }
        }
        Cow::Owned(normalized)
    }

    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }

    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&*PUNCT)
    }

    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(s) = common::verbalize_name(token) {
            return Some(s);
        }
        let (k, v) = common::class(token)?;
        let Value::Dict(d) = v else { return None };
        let s = match k {
            "cardinal" => cardinal::verbalize(d)?,
            "decimal" => decimal::verbalize(d)?,
            "fraction" => fraction::verbalize(d)?,
            "ordinal" => ordinal::verbalize(d)?,
            "money" => money::verbalize(d)?,
            "measure" => measure::verbalize(d)?,
            "date" => date::verbalize(d)?,
            "time" => time::verbalize(d)?,
            "telephone" => telephone::verbalize(d)?,
            "electronic" => electronic::verbalize(d)?,
            _ => return None,
        };
        Some(s.replace('\u{a0}', " "))
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
                // The raw name verbalizer permits quotes and an empty string,
                // but not ASCII space; it converts NBSP to ordinary space.
                if key == "name" && !value.contains(' ') {
                    append_normalized(&mut out, value);
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, value)] = fields.as_slice() {
                        if key == "integer" {
                            if let Some(value) = common::str_value(value).filter(|s| !s.is_empty()) {
                                append_normalized(&mut out, value);
                                continue;
                            }
                        }
                    }
                }
                if class == "time" && append_time(&mut out, fields) {
                    continue;
                }
                if class == "measure" && append_measure(&mut out, fields) {
                    continue;
                }
                if class == "money" && append_money(&mut out, fields) {
                    continue;
                }
            }
            let spoken = crate::permute::first_ordering(body, &mut |ordering| self.verbalize(ordering))?;
            out.push_str(&spoken);
        }
        Some(out)
    }
}

fn append_normalized(out: &mut String, value: &str) {
    let mut parts = value.split('\u{a0}');
    if let Some(first) = parts.next() {
        out.push_str(first);
    }
    for part in parts {
        out.push(' ');
        out.push_str(part);
    }
}

fn append_time(out: &mut String, fields: &Dict) -> bool {
    let mut f = common::Fields::new(fields);
    let Some(hours) = f.take("hours") else { return false };
    let minutes = f.take("minutes");
    let seconds = f.take("seconds");
    let suffix = f.take("suffix");
    let zone = f.take("zone");
    while f.preserve_order() {}
    if !f.done() || hours.is_empty() || [minutes, seconds, suffix, zone].into_iter().flatten().any(str::is_empty) {
        return false;
    }
    out.push_str("الساعة ");
    append_normalized(out, hours);
    for value in [minutes, seconds, suffix, zone].into_iter().flatten() {
        out.push(' ');
        append_normalized(out, value);
    }
    true
}

fn append_measure(out: &mut String, fields: &Dict) -> bool {
    let [(number, Value::Dict(card)), (unit, unit_value)] = fields.as_slice() else { return false };
    let [(integer, value)] = card.as_slice() else { return false };
    if number != "cardinal" || integer != "integer" || unit != "units" {
        return false;
    }
    let Some(value) = common::str_value(value).filter(|s| !s.is_empty()) else { return false };
    let Some(unit_value) = common::str_value(unit_value) else { return false };
    append_normalized(out, value);
    out.push(' ');
    append_normalized(out, unit_value);
    true
}

fn append_money(out: &mut String, fields: &Dict) -> bool {
    let (integer, currency) = match fields.as_slice() {
        [(a, av), (b, bv)] if a == "integer_part" && b == "currency_maj" => (av, bv),
        [(a, av), (b, bv), (p, Value::True)] if a == "integer_part" && b == "currency_maj" && p == "preserve_order" => {
            (av, bv)
        }
        _ => return false,
    };
    let Some(integer) = common::str_value(integer).filter(|s| !s.is_empty()) else { return false };
    let Some(currency) = common::str_value(currency).filter(|s| !s.is_empty()) else { return false };
    append_normalized(out, integer);
    out.push(' ');
    append_normalized(out, currency);
    true
}

static GRAMMAR: LazyLock<Arabic> = LazyLock::new(|| Arabic {
    classifiers: vec![
        Box::new(Prefixed(cardinal::CardinalClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(decimal::DecimalClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(fraction::FractionClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(money::MoneyClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(measure::MeasureClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(ordinal::OrdinalClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(date::DateClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(time::TimeClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(telephone::TelephoneClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(range::RangeClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(roman::RomanClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(electronic::ElectronicClassifier)),
        Box::new(Prefixed(whitelist::WhitelistClassifier { punct: &*PUNCT })),
        Box::new(Prefixed(serial::SerialClassifier { punct: &*PUNCT })),
        Box::new(word::WordClassifier { punct: &*PUNCT }),
    ],
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

//! `text_normalization/hu` (Hungarian): `ClassifyFst` + `VerbalizeFinalFst`.

pub mod graph_utils;
pub mod taggers;
pub mod verbalizers;

use std::sync::LazyLock;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, cost, is_nemo_space};

pub trait Lazy: Send + Sync {
    fn run(&self, text: &[char], start: usize, out: &mut Sink);
}

pub struct Sink<'a> {
    pub start: usize,
    pub out: &'a mut Vec<Candidate>,
    pub target: Option<(usize, Cost, i32)>,
    pub found: Option<String>,
}

impl Sink<'_> {
    #[inline]
    pub fn push(&mut self, end: usize, cost: Cost, rank: i32, tag: impl FnOnce() -> String) {
        match self.target {
            None => self.out.push(Candidate { end, cost, rank, tag: String::new() }),
            Some(t) => {
                if self.found.is_none() && t == (end, cost, rank) {
                    self.found = Some(tag());
                }
            }
        }
    }

    #[inline]
    pub fn exploring(&self) -> bool {
        self.target.is_none()
    }
}

pub fn ok_end(text: &[char], e: usize) -> bool {
    let Some(&c) = text.get(e) else { return true };
    is_nemo_space(c) || GRAMMAR.punct.is_mark(c)
}

// Source-derived starters, computed once per lattice start.
fn member_mask(text: &[char], start: usize) -> u16 {
    let Some(&first) = text.get(start) else { return 0 };
    let digit = first.is_ascii_digit();
    let sign = matches!(first, '-' | '−');
    let signed = if sign { text.get(start + 1).copied() } else { Some(first) };
    let signed_digit = signed.is_some_and(|c| c.is_ascii_digit());
    let alpha = graph_utils::is_hu_alpha(first);
    let fraction = signed_digit || (!alpha && signed.is_some_and(taggers::fraction::is_vulgar_fraction));
    let roman = alpha;
    let serial = alpha || digit || matches!(first, 'α' | 'β' | 'γ' | 'δ' | 'π' | 'θ' | 'λ' | 'μ' | 'σ' | 'ω');
    let telephone = digit || matches!(first, '(' | '+' | 'A' | 'h' | 't' | 'f');
    let ordinal = digit || alpha || first == '{';
    let mut mask = u16::MAX;
    for (enabled, bit) in [
        (digit, 1),
        (roman, 2),
        (alpha || fraction, 3),
        (digit, 4),
        (fraction, 5),
        (signed_digit, 6),
        (ordinal, 8),
        (signed_digit, 9),
        (telephone, 11),
        (serial, 13),
    ] {
        if !enabled {
            mask &= !(1 << bit);
        }
    }
    mask
}

struct Union {
    members: [&'static dyn Lazy; 15],
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            taggers::time::clear_readings();
        }
        if start != 0 && !ok_end(text, start - 1) {
            return;
        }

        let mask = member_mask(text, start);
        // 1. Normal exploration at start
        for (index, member) in self.members.iter().enumerate() {
            if mask & (1 << index) == 0 {
                continue;
            }
            let n = out.len();
            member.run(text, start, &mut Sink { start, out: &mut *out, target: None, found: None });
            let mut k = n;
            for i in n..out.len() {
                if ok_end(text, out[i].end) {
                    out[i].rank += index as i32 * 1000;
                    out.swap(k, i);
                    k += 1;
                }
            }
            out.truncate(k);
        }

        // 2. Prefix modifier: "~" or "±" (with optional space)
        if start < text.len() && (text[start] == '~' || text[start] == '±') {
            let mut pfx_len = 1;
            if start + pfx_len < text.len() && text[start + pfx_len] == ' ' {
                pfx_len += 1;
            }
            let inner_start = start + pfx_len;
            if inner_start < text.len() {
                let mask = member_mask(text, inner_start);
                for (index, member) in self.members.iter().enumerate() {
                    if mask & (1 << index) == 0 {
                        continue;
                    }
                    let n = out.len();
                    member.run(
                        text,
                        inner_start,
                        &mut Sink { start: inner_start, out: &mut *out, target: None, found: None },
                    );
                    let mut k = n;
                    for i in n..out.len() {
                        if ok_end(text, out[i].end) {
                            out[i].cost -= cost(1.0);
                            out[i].rank = 1_000_000 + (index as i32 * 1000) + out[i].rank;
                            out.swap(k, i);
                            k += 1;
                        }
                    }
                    out.truncate(k);
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.rank >= 1_000_000 {
            let pfx_sym = text[start];
            let pfx_name = if pfx_sym == '~' { "körülbelül" } else { "plusz-mínusz" };
            let mut pfx_len = 1;
            if start + pfx_len < text.len() && text[start + pfx_len] == ' ' {
                pfx_len += 1;
            }
            let inner_start = start + pfx_len;
            let stripped_rank = candidate.rank - 1_000_000;
            let index = (stripped_rank / 1000) as usize;
            let rank = stripped_rank - index as i32 * 1000;
            let member = self.members[index];
            let inner_cost = candidate.cost + cost(1.0);
            if index == 4 && rank == 0 && inner_cost == taggers::time::WEIGHT {
                if let Some(tag) = taggers::time::reading(inner_start, candidate.end) {
                    candidate.tag = format!("name: \"{pfx_name}\" }} tokens {{ {tag}");
                    return;
                }
            }

            let mut none = Vec::new();
            let mut sink = Sink {
                start: inner_start,
                out: &mut none,
                target: Some((candidate.end, inner_cost, rank)),
                found: None,
            };
            member.run(text, inner_start, &mut sink);
            let inner_tag = sink.found.expect("materialize: prefix re-run pushes reading");
            candidate.tag = format!("name: \"{pfx_name}\" }} tokens {{ {inner_tag}");
            return;
        }

        let index = (candidate.rank / 1000) as usize;
        let rank = candidate.rank - index as i32 * 1000;
        if index == 4 && rank == 0 && candidate.cost == taggers::time::WEIGHT {
            if let Some(tag) = taggers::time::reading(start, candidate.end) {
                candidate.tag = tag;
                return;
            }
        }
        let member = self.members[index];
        let mut none = Vec::new();
        let mut sink = Sink { start, out: &mut none, target: Some((candidate.end, candidate.cost, rank)), found: None };
        member.run(text, start, &mut sink);
        candidate.tag = sink.found.expect("materialize: re-run pushes reading");
    }
}

pub struct Hungarian {
    classifiers: Vec<Box<dyn Classifier>>,
    pub punct: Punctuation,
}

fn verbalize_name(token: &Dict) -> Option<String> {
    let [(k, Value::Str(v))] = token.as_slice() else { return None };
    if k != "name" {
        return None;
    }
    Some(v.replace('\u{a0}', " "))
}

fn verbalize_body(token: &Dict) -> Option<String> {
    if let Some(s) = verbalize_name(token) {
        return Some(s);
    }
    let (class, Value::Dict(fields)) = common::class(token)? else { return None };
    match class {
        "cardinal" => verbalizers::cardinal::verbalize(fields),
        "ordinal" => verbalizers::ordinal::verbalize(fields),
        "decimal" => verbalizers::decimal::verbalize(fields),
        "fraction" => verbalizers::fraction::verbalize(fields),
        "date" => verbalizers::date::verbalize(fields),
        "time" => verbalizers::time::verbalize(fields),
        "money" => verbalizers::money::verbalize(fields),
        "measure" => verbalizers::measure::verbalize(fields),
        "telephone" => verbalizers::telephone::verbalize(fields),
        "electronic" => verbalizers::electronic::verbalize(fields),
        _ => None,
    }
}

fn append_spoken(out: &mut String, value: &str) {
    for c in value.chars() {
        out.push(if c == '\u{a0}' { ' ' } else { c });
    }
}

fn append_simple_class(out: &mut String, class: &str, fields: &Dict) -> bool {
    match (class, fields.as_slice()) {
        ("measure", [(a, Value::Dict(number)), (b, Value::Str(unit)), (c, Value::True)])
            if a == "cardinal" && b == "units" && c == "preserve_order" && !unit.is_empty() =>
        {
            if let [(key, Value::Str(value))] = number.as_slice() {
                if key == "integer" && !value.is_empty() {
                    append_spoken(out, value);
                    out.push(' ');
                    append_spoken(out, unit);
                    return true;
                }
            }
        }
        ("money", [(a, Value::Str(number)), (b, Value::Str(currency)), (c, Value::True)])
            if a == "integer_part"
                && b == "currency_maj"
                && c == "preserve_order"
                && !number.is_empty()
                && !currency.is_empty() =>
        {
            append_spoken(out, number);
            out.push(' ');
            append_spoken(out, currency);
            return true;
        }
        ("time", [(a, Value::Str(hour)), (b, Value::Str(minute)), (c, Value::True)])
            if a == "hours" && b == "minutes" && c == "preserve_order" && !hour.is_empty() && !minute.is_empty() =>
        {
            append_spoken(out, hour);
            out.push_str(" óra");
            if minute != "nulla" {
                out.push(' ');
                append_spoken(out, minute);
            }
            out.push_str(" perc");
            return true;
        }
        _ => {}
    }
    false
}

impl Grammar for Hungarian {
    fn code(&self) -> &'static str {
        "hu"
    }
    fn shape(&self) -> Shape {
        Shape::En
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalize_body(token).map(|s| if s.contains('\u{a0}') { s.replace('\u{a0}', " ") } else { s })
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
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if append_simple_class(&mut out, class, fields) {
                    continue;
                }
            }
            let direct = match body.as_slice() {
                [(key, Value::Str(value))] if key == "name" => Some(value.as_ref()),
                [(class, Value::Dict(fields))] if class == "cardinal" => match fields.as_slice() {
                    [(key, Value::Str(value))] if key == "integer" => Some(value.as_ref()),
                    _ => None,
                },
                _ => None,
            };
            if let Some(value) = direct {
                append_spoken(&mut out, value);
            } else {
                let word = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
                out.push_str(&word);
            }
        }
        Some(out)
    }
}

static GRAMMAR: LazyLock<Hungarian> = LazyLock::new(|| {
    use taggers::*;
    Hungarian {
        classifiers: vec![Box::new(Union {
            members: [
                &whitelist::WhiteList,
                &composite::Composite,
                &roman::Roman,
                &measure::Measure,
                &time::Time,
                &fraction::Fraction,
                &cardinal::Cardinal,
                &date::Date,
                &ordinal::Ordinal,
                &decimal::Decimal,
                &money::Money,
                &telephone::Telephone,
                &electronic::Electronic,
                &serial::Serial,
                &word::Word,
            ],
        })],
        punct: Punctuation::en(cost(2.1)),
    }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

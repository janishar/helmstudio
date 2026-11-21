//! Native Dutch normalization grammar, ported from the production NeMo package.
mod cardinal;
mod date;
mod electronic;
mod fraction;
mod lexical;
mod measure;
mod money;
mod numeric;
mod ordinal;
mod serial;
mod telephone;
mod time;

use crate::lang::common::{self, Fields};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

struct Dutch {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punct,
}
impl Grammar for Dutch {
    fn code(&self) -> &'static str {
        "nl"
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
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(v) = common::verbalize_name(token) {
            return if v.is_empty() { None } else { Some(v) };
        }
        let mut outer = Fields::new(token);
        let date = outer.dict("date")?;
        if !outer.done() {
            return None;
        }
        let mut f = Fields::new(date);
        let d = f.take("day")?;
        let m = f.take("month")?;
        let y = f.take("year")?;
        if !f.done() || d.is_empty() || m.is_empty() || y.is_empty() {
            return None;
        }
        Some(format!("{} {} {}", d.replace('\u{a0}', " "), m.replace('\u{a0}', " "), y.replace('\u{a0}', " ")))
    }
}
static GRAMMAR: LazyLock<Dutch> = LazyLock::new(|| Dutch { classifiers: vec![Box::new(Union)], punct: Punct });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

struct Punct;
impl Classifier for Punct {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        const MARKS: &str = "!#$%&'()*+,-./:;<=>?@^_`{|}~\"«»„“”‘’–—…[]";
        if let Some(&c) = text.get(start) {
            if MARKS.contains(c) {
                out.push(Candidate { end: start + 1, cost: cost(1.1), rank: 0, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity(12);
        tag.push_str("name: \"");
        tag.push(text[start]);
        tag.push('"');
        candidate.tag = tag;
    }
}
fn append_name_text(out: &mut String, s: &str) {
    for c in s.chars() {
        out.push(if c == ' ' { '\u{a0}' } else { c });
    }
}

fn name(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 10);
    out.push_str("name: \"");
    append_name_text(&mut out, s);
    out.push('"');
    out
}

fn name_start(prefix: Option<&str>) -> String {
    let mut tag = String::with_capacity(64);
    if let Some(prefix) = prefix {
        tag.push_str("name: \"");
        tag.push_str(prefix);
        tag.push_str("\" } tokens { ");
    }
    tag.push_str("name: \"");
    tag
}
struct Union;
/// After an internal space the source grammars accept only these lexical
/// prefixes. Reject a span as soon as its next alphabetic component cannot
/// continue any unit, currency, date, time or Roman-context branch.
fn following_word_prefix(s: &str) -> bool {
    measure::unit_prefix(s)
        || money::currency_prefix(s)
        || date::month_prefix(s)
        || lexical::roman_prefix(s)
        || [
            "eeuw", "v", "n", "Chr", "C", "BC", "AD", "AM", "PM", "am", "pm", "UTC", "GMT", "CET", "CEST", "maand",
            "jaar", "dag", "hr",
        ]
        .iter()
        .any(|word| word.starts_with(s))
}

impl Union {
    /// Plain alphabetic chunks cannot extend beyond the next space unless
    /// they are one of the grammar's Roman, decade or currency contexts.
    fn plain_word(text: &[char], start: usize, out: &mut Vec<Candidate>) -> bool {
        if !text.get(start).is_some_and(|c| c.is_alphabetic()) {
            return false;
        }
        let mut end = start + 1;
        while end < text.len() && text[end].is_alphabetic() {
            end += 1;
        }
        if end < text.len() && !is_nemo_space(text[end]) {
            return false;
        }
        let word = &text[start..end];
        if end < text.len() && (lexical::starts_spaced_chars(word) || money::starts_spaced_chars(word)) {
            return false;
        }
        if let Some(value) = lexical::whitelist_chars(word) {
            out.push(Candidate { end, cost: cost(1.01), rank: 0, tag: name(value) });
        }
        let control = match word {
            ['X', 'L'] => Some("X L"),
            ['M', 'I', 'X'] => Some("M I X"),
            ['C', 'D'] => Some("C D"),
            _ => None,
        };
        if let Some(value) = control {
            out.push(Candidate { end, cost: cost(1.94), rank: 1000, tag: name(value) });
        }
        out.push(Candidate { end, cost: cost(100.0), rank: 13000, tag: String::new() });
        true
    }

    /// The digit-only intersection has only cardinal, serial and word paths.
    /// Recognize their costs without constructing any Dutch number names.
    fn digits(s: &str, end: usize, prefix: Option<&str>, out: &mut Vec<Candidate>) -> bool {
        if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
        let adjust = if prefix.is_some() { -2.0 } else { 0.0 };
        if let Some((weights, count)) = cardinal::weights(s) {
            for w in &weights[..count] {
                out.push(Candidate { end, cost: cost(2.11 + w + adjust), rank: 11000, tag: String::new() });
                out.push(Candidate { end, cost: cost(5.0 + w + adjust), rank: 12000, tag: String::new() });
            }
        } else {
            let suffix = s.trim_start_matches('0');
            if !suffix.is_empty() && suffix.len() <= 2 {
                out.push(Candidate { end, cost: cost(2.16 + adjust), rank: 11000, tag: String::new() });
            }
        }
        if s.len() >= 2 {
            out.push(Candidate { end, cost: cost(2.31 + adjust), rank: 11000, tag: String::new() });
        }
        out.push(Candidate { end, cost: cost(5.1 + adjust), rank: 12000, tag: String::new() });
        out.push(Candidate { end, cost: cost(100.0 + adjust), rank: 13000, tag: String::new() });
        true
    }

    fn signed_digits(s: &str, end: usize, prefix: Option<&str>, out: &mut Vec<Candidate>) -> bool {
        let Some(raw) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) else { return false };
        if raw.is_empty() || !raw.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
        let adjust = if prefix.is_some() { -2.0 } else { 0.0 };
        if let Some((weights, count)) = cardinal::weights(raw) {
            for w in &weights[..count] {
                out.push(Candidate { end, cost: cost(2.11 + w + adjust), rank: 11000, tag: String::new() });
            }
        }
        out.push(Candidate { end, cost: cost(100.0 + adjust), rank: 13000, tag: String::new() });
        true
    }

    fn alphabetic(s: &str, end: usize, prefix: Option<&str>, out: &mut Vec<Candidate>) -> bool {
        if s.is_empty() || !s.chars().all(char::is_alphabetic) {
            return false;
        }
        let adjust = if prefix.is_some() { -2.0 } else { 0.0 };
        let wrap = |tag: String| match prefix {
            Some(p) => format!("{} }} tokens {{ {tag}", name(p)),
            None => tag,
        };
        if let Some(v) = lexical::whitelist(s) {
            out.push(Candidate { end, cost: cost(1.01 + adjust), rank: 0, tag: wrap(name(v)) });
        }
        let control = match s {
            "XL" => Some("X L"),
            "MIX" => Some("M I X"),
            "CD" => Some("C D"),
            _ => None,
        };
        if let Some(v) = control {
            out.push(Candidate { end, cost: cost(1.94 + adjust), rank: 1000, tag: wrap(name(v)) });
        }
        out.push(Candidate { end, cost: cost(100.0 + adjust), rank: 13000, tag: String::new() });
        true
    }

    fn classify(s: &str, end: usize, prefix: Option<&str>, out: &mut Vec<Candidate>) {
        if Self::digits(s, end, prefix, out)
            || Self::signed_digits(s, end, prefix, out)
            || Self::alphabetic(s, end, prefix, out)
        {
            return;
        }
        let mut push = |tag: String, weight: f64, rank: i32| {
            let (tag, weight) = if let Some(prefix) = prefix {
                (if tag.is_empty() { tag } else { format!("{} }} tokens {{ {tag}", name(prefix)) }, weight - 2.0)
            } else {
                (tag, weight)
            };
            out.push(Candidate { end, cost: cost(weight), rank, tag });
        };
        if let Some(v) = lexical::whitelist(s) {
            push(name(v), 1.01, 0);
        }
        if s.as_bytes().first().is_some_and(u8::is_ascii_alphabetic) {
            for (v, w) in lexical::roman(s, false) {
                push(name(&v), 1.04 + w, 1000);
            }
        }
        let digit = s.bytes().any(|c| c.is_ascii_digit());
        if digit {
            if s.contains(':') {
                if time::simple(s) {
                    push(String::new(), 2.05, 2000);
                } else {
                    for (v, w) in time::parse(s) {
                        push(name(&v), 1.05 + w, 2000);
                    }
                }
            }
            if date::could_match(s) {
                for (tag, w) in date::parse(s) {
                    push(tag, 1.07 + w, 3000);
                }
            }
            if s.contains(['+', '(', '-', ' ', '.', '/'])
                && s.chars().all(|c| c.is_ascii_digit() || "+()- ./".contains(c))
            {
                for (v, w) in telephone::parse(s) {
                    push(name(&v), 1.08 + w, 5000);
                }
            }
            if s.chars().any(|c| c.is_ascii_uppercase() || "$€£¥₹₽₺₪₴₫৳₦₱฿֏₩".contains(c)) {
                for (v, w) in money::parse(s) {
                    push(name(&v), 1.09 + w, 6000);
                }
            }
            if s.chars().last().is_some_and(|c| c.is_alphabetic() || "%‰¢°²³".contains(c)) {
                if let Some(w) = measure::simple_weight(s) {
                    push(String::new(), 1.10 + w, 7000);
                } else {
                    for (v, w) in measure::parse(s) {
                        push(name(&v), 1.10 + w, 7000);
                    }
                }
            }
            if s.contains([',', '.']) {
                for (v, w) in numeric::decimal(s) {
                    push(name(&v), 1.10 + w, 9000);
                }
            }
            if s.chars().all(|c| {
                c.is_ascii_digit()
                    || matches!(c, '.' | ',' | ' ' | '-' | '−' | '–' | '—' | '\t' | '\n' | '\r' | '\u{a0}')
            }) {
                for (v, w) in numeric::cardinal(s) {
                    push(name(&v), 1.11 + w, 11000);
                }
            }
        }
        if (s.contains('.')
            || s.contains('@')
            || s.starts_with("http://")
            || s.starts_with("https://")
            || s.starts_with("ftp://"))
            && !s.chars().any(is_nemo_space)
        {
            for (v, w) in electronic::parse(s) {
                push(name(&v), 1.08 + w, 4000);
            }
        }
        if (digit && s.contains('/'))
            || s.chars().any(|c| {
                matches!(
                    c,
                    '½' | '⅓'
                        | '⅔'
                        | '¼'
                        | '¾'
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
                        | '⅑'
                        | '⅒'
                )
            })
        {
            for (v, w) in fraction::parse(s) {
                push(name(&v), 1.10 + w, 8000);
            }
        }
        if s.ends_with('e')
            || s.ends_with('.')
            || (s.contains(' ') && s.as_bytes().first().is_some_and(u8::is_ascii_alphabetic))
        {
            for (v, w) in lexical::ordinal(s) {
                push(name(&v), 1.10 + w, 10000);
            }
        }
        if (digit || s.contains(['²', '³'])) && s.chars().all(|c| c.is_ascii_alphanumeric() || ".-/:_^²³".contains(c))
        {
            if let Some(weights) = serial::simple_weights(s) {
                for w in weights {
                    push(String::new(), 5.0 + w, 12000);
                }
            } else {
                for (v, w) in serial::parse(s) {
                    push(name(&v), 5.0 + w, 12000);
                }
            }
        }
        if !s.is_empty() && !s.chars().any(is_nemo_space) {
            out.push(Candidate {
                end,
                cost: cost(if prefix.is_some() { 98.0 } else { 100.0 }),
                rank: 13000,
                tag: String::new(),
            });
        }
    }
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !common::possible_token_start(text, start) {
            return;
        }
        if Self::plain_word(text, start, out) {
            return;
        }
        let mut s = String::new();
        let mut after_space = false;
        let mut word_start = None;
        for end in start + 1..=text.len() {
            if is_nemo_space(text[end - 1])
                && text[start].is_alphabetic()
                && !s.chars().any(is_nemo_space)
                && !lexical::starts_spaced(&s)
                && !money::starts_spaced(&s)
            {
                break;
            }
            let c = text[end - 1];
            if is_nemo_space(c) {
                after_space = true;
                word_start = None;
            } else if after_space && word_start.is_none() {
                after_space = false;
                if c.is_alphabetic() && !matches!(text[start], '~' | '±') {
                    word_start = Some(s.len());
                }
            }
            s.push(text[end - 1]);
            if let Some(begin) = word_start {
                if c.is_alphabetic() {
                    if !following_word_prefix(&s[begin..]) {
                        break;
                    }
                } else {
                    word_start = None;
                }
            }
            if !common::possible_token_end(text, end) {
                continue;
            }
            Self::classify(&s, end, None, out);
            if let Some(first) = s.chars().next() {
                let word = match first {
                    '~' => Some("ongeveer"),
                    '±' => Some("plusminus"),
                    _ => None,
                };
                if let Some(word) = word {
                    let rest = &s[first.len_utf8()..];
                    if !rest.is_empty() {
                        Self::classify(rest, end, Some(word), out);
                    }
                    if let Some(rest) = rest.strip_prefix(' ') {
                        if !rest.is_empty() {
                            Self::classify(rest, end, Some(word), out);
                        }
                    }
                }
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let span = &text[start..candidate.end];
        if candidate.rank == 13000 {
            let (prefix, raw) = if candidate.cost == cost(98.0) {
                let prefix = if span[0] == '~' { "ongeveer" } else { "plusminus" };
                let raw = &span[1..];
                (Some(prefix), if raw.first() == Some(&' ') { &raw[1..] } else { raw })
            } else {
                (None, span)
            };
            let mut tag = name_start(prefix);
            tag.extend(raw.iter());
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        if !matches!(candidate.rank, 2000 | 7000 | 11000 | 12000) {
            return;
        }
        let (prefix, raw) = match span.first() {
            Some('~') => (Some("ongeveer"), &span[1..]),
            Some('±') => (Some("plusminus"), &span[1..]),
            _ => (None, span),
        };
        let raw = if prefix.is_some() && raw.first() == Some(&' ') { &raw[1..] } else { raw };
        let adjust = if prefix.is_some() { -2.0 } else { 0.0 };

        if candidate.rank == 2000 {
            if let Some((hour, minute)) = time::simple_chars(raw) {
                let mut tag = name_start(prefix);
                cardinal::write_small(hour, &mut tag);
                tag.push_str("\u{a0}uur\u{a0}");
                cardinal::write_small(minute, &mut tag);
                tag.push('"');
                candidate.tag = tag;
                return;
            }
        } else if candidate.rank == 7000 {
            if let Some((number, unit, weight)) = measure::simple_chars(raw) {
                if cost(1.10 + weight + adjust) == candidate.cost {
                    let mut tag = name_start(prefix);
                    cardinal::write_small(number, &mut tag);
                    tag.push('\u{a0}');
                    append_name_text(&mut tag, unit);
                    tag.push('"');
                    candidate.tag = tag;
                    return;
                }
            }
        } else if candidate.rank == 11000 {
            let (negative, digits) = match raw.first() {
                Some('-' | '−') => (true, &raw[1..]),
                _ => (false, raw),
            };
            if !digits.is_empty()
                && digits.len() <= 3
                && (digits.len() == 1 || digits[0] != '0')
                && digits.iter().all(char::is_ascii_digit)
            {
                let number = digits.iter().fold(0usize, |n, &d| n * 10 + (d as u8 - b'0') as usize);
                let weight = if (101..=109).contains(&number) { 0.01 } else { 0.0 };
                if cost(2.11 + weight + adjust) == candidate.cost {
                    let mut tag = name_start(prefix);
                    if negative {
                        tag.push_str("min\u{a0}");
                    }
                    cardinal::write_small(number, &mut tag);
                    tag.push('"');
                    candidate.tag = tag;
                    return;
                }
            }
        }

        let raw: String = raw.iter().collect();
        let (base, readings) = match candidate.rank {
            2000 => (1.05, time::parse(&raw)),
            7000 => (1.10, measure::parse(&raw)),
            11000 => (1.11, numeric::cardinal(&raw)),
            _ => (5.0, serial::parse(&raw)),
        };
        if let Some((spoken, _)) = readings.into_iter().find(|(_, w)| cost(base + w + adjust) == candidate.cost) {
            let mut tag = name_start(prefix);
            append_name_text(&mut tag, &spoken);
            tag.push('"');
            candidate.tag = tag;
        }
    }
}

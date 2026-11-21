//! Russian text normalization, ported from the production NeMo `ru` package.
// Reference: zonos3/vendor/nemo_text_processing/text_normalization/ru/

mod cardinal;
mod date;
mod decimal;
mod electronic;
mod fraction;
mod measure;
mod money;
mod number;
mod ordinal;
mod roman;
mod telephone;
mod time;
mod units;
mod whitelist;
mod word;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::cost;
use std::sync::LazyLock;

thread_local! {
    static PREFIX_SCRATCH: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}

fn with_prefix_scratch(f: impl FnOnce(&mut String)) {
    PREFIX_SCRATCH.with(|scratch| {
        let mut scratch = scratch.borrow_mut();
        scratch.clear();
        f(&mut scratch);
    });
}

type SpanFn = fn(&str, usize, &mut Vec<Candidate>);
type RenderFn = fn(&str, &mut Candidate);
const FAMILIES: [(SpanFn, RenderFn); 10] = [
    (time::span, time::materialize),
    (fraction::span, fraction::materialize),
    (date::span, date::materialize),
    (decimal::span, decimal::materialize),
    (measure::span, measure::materialize),
    (cardinal::span, cardinal::materialize),
    (ordinal::span, ordinal::materialize),
    (money::span, money::materialize),
    (telephone::span, telephone::materialize),
    (electronic::span, electronic::materialize),
];

const TIME_MARK: u16 = 1 << 0;
const FRACTION_MARK: u16 = 1 << 1;
const DATE_MARK: u16 = 1 << 2;
const DECIMAL_MARK: u16 = 1 << 3;
const MEASURE_MARK: u16 = 1 << 4;
const ORDINAL_MARK: u16 = 1 << 5;
const MONEY_MARK: u16 = 1 << 6;
const PHONE_MARK: u16 = 1 << 7;
const ELECTRONIC_MARK: u16 = 1 << 8;

struct Live {
    families: [bool; 10],
    digit: bool,
    plain_digits: bool,
    fraction_done: bool,
    shape: u16,
}
impl Live {
    fn new(text: &[char], start: usize) -> Self {
        let first = text[start];
        let digit = first.is_ascii_digit();
        let ru = whitelist::is_ru_alpha(first);
        let preposition_start = !digit
            && !matches!(first, '-' | '~' | '±')
            && cardinal::GENITIVE_PREPOSITIONS.iter().any(|prep| prep.starts_with(first));
        let preposition = preposition_start
            && cardinal::GENITIVE_PREPOSITIONS.iter().any(|prep| {
                let mut input = text[start..].iter().copied();
                prep.chars().all(|c| input.next() == Some(c)) && input.next() == Some(' ')
            });
        let mut live = Self {
            families: [
                digit || matches!(first, '+' | '~' | 'с') || first.is_whitespace(),
                digit || first == '-' || fraction::VULGAR_FRACTIONS.iter().any(|&(c, _, _)| c == first),
                digit || first == '+' || first.is_whitespace(),
                digit || first == '-',
                digit || ru || matches!(first, '-' | '±' | '~'),
                digit || cardinal::serial_alpha(first) || matches!(first, '-' | '~' | '±' | '№'),
                digit || first == '\'',
                digit
                    || matches!(first, '-' | '~' | '±')
                    || preposition_start
                    || units::MAJOR_EDGES.has_first(first)
                    || units::MINOR_EDGES.has_first(first),
                digit || matches!(first, '+' | '('),
                first.is_ascii_alphanumeric(),
            ],
            digit: false,
            plain_digits: true,
            fraction_done: false,
            shape: 0,
        };
        if live.families[7] && !digit && !matches!(first, '-' | '~' | '±') {
            live.families[7] =
                preposition || units::MAJOR_EDGES.starts_at(text, start) || units::MINOR_EDGES.starts_at(text, start);
        }
        if !digit && (live.families[4] || live.families[5]) {
            // These families cannot emit before their first digit. If a
            // digit-free word is followed by a space, apply the same death
            // rules that advance() would apply at that first space.
            if let Some(offset) = text[start..].iter().position(|c| c.is_ascii_digit() || *c == ' ') {
                if text[start + offset] == ' ' {
                    let word = &text[start..start + offset];
                    if !word.is_empty() && word.iter().copied().all(whitelist::is_ru_alpha) && !preposition {
                        live.families[4] = false;
                    }
                    if word.iter().copied().any(cardinal::serial_alpha) && !preposition {
                        live.families[5] = false;
                    }
                }
            } else {
                // No digit remains anywhere in this suffix.
                live.families[4] = false;
                live.families[5] = false;
                live.families[7] = false;
            }
        }
        live
    }
    fn advance(&mut self, c: char, prefix: &str, length: usize) {
        let digit = c.is_ascii_digit();
        let ru = whitelist::is_ru_alpha(c);
        let vulgar = self.families[1] && fraction::VULGAR_FRACTIONS.iter().any(|&(v, _, _)| v == c);
        self.families[0] &= digit
            || c.is_whitespace()
            || matches!(c, '+' | '~' | 'с' | ':' | '-' | '–' | '.' | 'a' | 'A' | 'p' | 'P' | 'm' | 'M');
        self.families[1] &=
            !self.fraction_done && (digit || vulgar || c == '/' || c == ' ' || (length == 1 && c == '-'));
        self.fraction_done = vulgar;
        if c == ':' || matches!(c, 'a' | 'A' | 'p' | 'P' | 'm' | 'M') {
            self.shape |= TIME_MARK;
        }
        if c == '/' || vulgar {
            self.shape |= FRACTION_MARK;
        }
        if c.is_whitespace() || ru || c.is_ascii_alphabetic() || matches!(c, '.' | '/' | '-') {
            self.shape |= DATE_MARK;
        }
        if matches!(c, '.' | ',') {
            self.shape |= DECIMAL_MARK;
        }
        if !digit && !matches!(c, ' ' | '\u{a0}' | '.' | ',' | '-' | '–') {
            self.shape |= MEASURE_MARK | MONEY_MARK;
        }
        if matches!(c, '-' | '\'' | 's') {
            self.shape |= ORDINAL_MARK;
        }
        if matches!(c, '+' | '(' | ' ' | '-' | '.') {
            self.shape |= PHONE_MARK;
        }
        if matches!(c, '.' | '@' | '/') {
            self.shape |= ELECTRONIC_MARK;
        }
        self.families[2] &= date::date_char(c);
        self.families[3] &= digit || ru || crate::text::is_nemo_space(c) || matches!(c, '-' | '.' | ',');
        let compound = if self.families[4] || self.families[7] { units::compound_char(c) } else { false };
        self.families[4] &=
            compound && !(c == ' ' && measure::russian(prefix) && !cardinal::GENITIVE_PREPOSITIONS.contains(&prefix));
        let serial = self.families[5] && !digit && (ru || (c.is_ascii_alphabetic() && cardinal::serial_alpha(c)));
        self.families[5] &=
            digit || serial || matches!(c, ' ' | '\u{a0}' | '.' | ',' | '-' | '–' | '/' | '^' | '~' | '±' | '№');
        if self.families[5] && c == ' ' {
            let rest = cardinal::GENITIVE_PREPOSITIONS
                .iter()
                .find_map(|p| prefix.strip_prefix(p).and_then(|r| r.strip_prefix(' ')));
            if let Some(rest) = rest {
                if rest.chars().any(cardinal::serial_alpha) {
                    self.families[5] = false;
                }
            } else if prefix.chars().any(cardinal::serial_alpha) && !cardinal::GENITIVE_PREPOSITIONS.contains(&prefix) {
                self.families[5] = false;
            }
        }
        self.families[6] &= digit || ru || matches!(c, '-' | '\'' | 's');
        self.families[7] &= compound;
        if c == ' '
            && self.digit
            && prefix.chars().last().is_some_and(|last| last.is_alphabetic() || last == '.')
            && (self.families[4] || self.families[7])
            && !units::lexical_space_continuation(prefix)
        {
            self.families[4] = false;
            self.families[7] = false;
        }
        self.families[8] &= digit || matches!(c, '+' | '(' | ')' | ' ' | '-' | '.');
        if self.families[9]
            && c.is_whitespace()
            && electronic::symbol(c).is_none()
            && !prefix.starts_with("http://")
            && !prefix.starts_with("https://")
            && !prefix.starts_with("www.")
        {
            self.families[9] = false;
        }
        self.digit |= digit;
        self.plain_digits &= digit;
    }
    fn after_endpoint(&mut self, length: usize) {
        if length > 40 {
            self.families[0] = false;
            self.families[2] = false;
        }
        if length > 30 {
            self.families[1] = false;
        }
        if length > 35 {
            self.families[8] = false;
        }
    }
    fn allows(&self, i: usize) -> bool {
        const REQUIRED: [u16; 10] = [
            TIME_MARK,
            FRACTION_MARK,
            DATE_MARK,
            DECIMAL_MARK,
            MEASURE_MARK,
            0,
            ORDINAL_MARK,
            MONEY_MARK,
            PHONE_MARK,
            ELECTRONIC_MARK,
        ];
        self.families[i]
            && (!matches!(i, 4 | 5 | 7) || self.digit)
            && (REQUIRED[i] == 0 || self.shape & REQUIRED[i] != 0)
    }
}

// Time emits at most nine readings: approximation, two range separators,
// four clock markers, colloquial and plain. Other raw rank sets are explicit
// in their span recognizers.
fn family_rank_possible(i: usize, global: i32) -> bool {
    let raw = global - (i as i32 + 2) * 1000;
    match i {
        0 => (0..=48).contains(&raw),
        2 | 3 | 6 => matches!(raw, 0 | 1),
        5 => matches!(raw, 0 | 1 | 2 | 10),
        _ => raw == 0,
    }
}

struct Union;
impl Union {
    fn direct<C: Classifier>(classifier: &C, offset: i32, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let base = out.len();
        classifier.candidates(text, start, out);
        for candidate in &mut out[base..] {
            candidate.rank += offset;
        }
    }
    fn replay_direct<C: Classifier>(
        classifier: &C,
        offset: i32,
        text: &[char],
        start: usize,
        candidate: &mut Candidate,
        scratch: &mut Vec<Candidate>,
    ) -> bool {
        scratch.clear();
        classifier.candidates(text, start, scratch);
        if scratch
            .iter()
            .any(|c| c.end == candidate.end && c.cost == candidate.cost && c.rank + offset == candidate.rank)
        {
            candidate.rank -= offset;
            classifier.materialize(text, start, candidate);
            candidate.rank += offset;
            true
        } else {
            false
        }
    }
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::direct(&whitelist::WhitelistClassifier, 0, text, start, out);
        Self::direct(&roman::RomanClassifier, 1000, text, start, out);
        if common::possible_token_start(text, start) {
            with_prefix_scratch(|s| {
                let mut live = Live::new(text, start);
                if !live.families.iter().any(|family| *family) {
                    return;
                }
                for end in start + 1..=text.len() {
                    let c = text[end - 1];
                    live.advance(c, s, end - start);
                    if !live.families.iter().any(|v| *v) {
                        break;
                    }
                    s.push(c);
                    if common::possible_token_end(text, end) {
                        for (i, (recognize, _)) in FAMILIES.iter().enumerate() {
                            if live.allows(i) {
                                let base = out.len();
                                if i == 5 && live.plain_digits {
                                    cardinal::span_plain_digits(s, end, out);
                                } else {
                                    recognize(s, end, out);
                                }
                                for candidate in &mut out[base..] {
                                    candidate.rank += (i as i32 + 2) * 1000;
                                }
                            }
                        }
                    }
                    live.after_endpoint(end - start);
                }
            });
        }
        Self::direct(&word::WordClassifier, 12000, text, start, out);
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        MATERIAL_SCRATCH.with(|scratch| {
            let mut scratch = scratch.borrow_mut();
            let whitelist_possible = candidate.rank == -1
                || (candidate.rank >= 0 && whitelist::MAX_RANK.is_some_and(|max| candidate.rank <= max));
            if whitelist_possible
                && Self::replay_direct(&whitelist::WhitelistClassifier, 0, text, start, candidate, &mut scratch)
            {
                return;
            }
            if candidate.rank == 1000
                && Self::replay_direct(&roman::RomanClassifier, 1000, text, start, candidate, &mut scratch)
            {
                return;
            }
            if candidate.rank == 12000
                && candidate.cost == cost(100.0)
                && !(0..FAMILIES.len()).any(|i| family_rank_possible(i, candidate.rank))
            {
                word::WordClassifier.materialize(text, start, candidate);
                return;
            }
            // The actual raw-rank sets above can overlap, so bypass replay
            // only when exactly one structured family can have emitted this
            // global rank, after checking both earlier direct classifiers.
            let mut matching = (0..FAMILIES.len()).filter(|&i| family_rank_possible(i, candidate.rank));
            if let Some(i) = matching.next() {
                if matching.next().is_none() {
                    with_prefix_scratch(|s| {
                        s.extend(text[start..candidate.end].iter());
                        let offset = (i as i32 + 2) * 1000;
                        candidate.rank -= offset;
                        FAMILIES[i].1(s, candidate);
                        candidate.rank += offset;
                    });
                    return;
                }
            }
            with_prefix_scratch(|s| {
                let mut live = Live::new(text, start);
                for end in start + 1..=candidate.end {
                    live.advance(text[end - 1], s, end - start);
                    s.push(text[end - 1]);
                    if end < candidate.end {
                        live.after_endpoint(end - start);
                    }
                }
                for (i, (recognize, render)) in FAMILIES.iter().enumerate() {
                    if live.allows(i) && family_rank_possible(i, candidate.rank) {
                        scratch.clear();
                        recognize(s, candidate.end, &mut scratch);
                        let offset = (i as i32 + 2) * 1000;
                        if scratch.iter().any(|c| c.cost == candidate.cost && c.rank + offset == candidate.rank) {
                            candidate.rank -= offset;
                            render(s, candidate);
                            candidate.rank += offset;
                            return;
                        }
                    }
                }
            });
            if candidate.tag.is_empty() && candidate.rank == 12000 && candidate.cost == cost(100.0) {
                word::WordClassifier.materialize(text, start, candidate);
            }
        });
    }
}
thread_local! {
    static MATERIAL_SCRATCH: std::cell::RefCell<Vec<Candidate>> = const { std::cell::RefCell::new(Vec::new()) };
}

pub struct Russian {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for Russian {
    fn code(&self) -> &'static str {
        "ru"
    }

    fn shape(&self) -> Shape {
        Shape::SeparatedWeighted(cost(1.1))
    }

    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }

    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }

    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 32);
        let append = |out: &mut String, value: &str| {
            for (index, part) in value.split('\u{a0}').enumerate() {
                if index != 0 {
                    out.push(' ');
                }
                out.push_str(part);
            }
        };
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push_str(self.token_join());
            }
            if let [(key, Value::Str(value))] = body.as_slice() {
                if key == "name" && !value.is_empty() && !value.contains(' ') {
                    append(&mut out, value);
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, value)] = fields.as_slice() {
                        if key == "integer" {
                            if let Some(value) = common::str_value(value).filter(|s| !s.is_empty()) {
                                append(&mut out, value);
                                continue;
                            }
                        }
                    }
                }
                if class == "time" {
                    if let [(key, value)] = fields.as_slice() {
                        if key == "hours" {
                            if let Some(value) = common::str_value(value).filter(|s| !s.is_empty()) {
                                append(&mut out, value);
                                continue;
                            }
                        }
                    }
                }
                if class == "measure" {
                    if let [(key, Value::Dict(cardinal))] = fields.as_slice() {
                        if key == "cardinal" {
                            if let [(key, value)] = cardinal.as_slice() {
                                if key == "integer" {
                                    if let Some(value) = common::str_value(value).filter(|s| !s.is_empty()) {
                                        append(&mut out, value);
                                        continue;
                                    }
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

    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(w) = common::verbalize_name(token) {
            return Some(w);
        }

        let (name, value) = common::class(token)?;
        let Value::Dict(fields) = value else { return None };
        let get_str = |k: &str| -> Option<&str> {
            fields.iter().find(|(key, _)| key == k).and_then(|(_, v)| common::str_value(v))
        };

        match name.as_ref() {
            "cardinal" => {
                let neg = get_str("negative");
                let integer = get_str("integer")?;
                let qty = get_str("quantity");
                let mut out = String::new();
                if neg.is_some_and(|s| s == "true") {
                    out.push_str("минус ");
                }
                out.push_str(integer);
                if let Some(q) = qty {
                    out.push(' ');
                    out.push_str(q);
                }
                Some(out.replace('\u{a0}', " "))
            }
            "ordinal" => {
                let integer = get_str("integer")?;
                Some(integer.replace('\u{a0}', " "))
            }
            "decimal" => {
                let neg = get_str("negative");
                let int_part = get_str("integer_part")?;
                let frac_part = get_str("fractional_part")?;
                let qty = get_str("quantity");
                let mut out = String::new();
                if neg.is_some_and(|s| s == "true") {
                    out.push_str("минус ");
                }
                out.push_str(int_part);
                out.push(' ');
                out.push_str(frac_part);
                if let Some(q) = qty {
                    out.push(' ');
                    out.push_str(q);
                }
                Some(out.replace('\u{a0}', " "))
            }
            "fraction" => {
                let neg = get_str("negative");
                let int_part = get_str("integer_part");
                let num = get_str("numerator")?;
                let den = get_str("denominator")?;
                let mut out = String::new();
                if neg.is_some_and(|s| s == "true") {
                    out.push_str("минус ");
                }
                if let Some(int_s) = int_part {
                    out.push_str(int_s);
                    out.push(' ');
                }
                out.push_str(num);
                out.push(' ');
                out.push_str(den);
                Some(out.replace('\u{a0}', " "))
            }
            "date" => {
                let day = get_str("day");
                let month = get_str("month");
                let year = get_str("year");
                let out = match (day, month, year) {
                    (Some(d), Some(m), Some(y)) => format!("{d} {m} {y}"),
                    (Some(d), Some(m), None) => format!("{d} {m}"),
                    (None, None, Some(y)) => y.to_string(),
                    _ => return None,
                };
                Some(out.replace('\u{a0}', " "))
            }
            "time" => {
                let hours = get_str("hours")?;
                Some(hours.replace('\u{a0}', " "))
            }
            "electronic" => {
                let username = get_str("username")?;
                Some(username.replace('\u{a0}', " "))
            }
            "telephone" => {
                let num = get_str("number_part")?;
                Some(num.replace('\u{a0}', " "))
            }
            "measure" => {
                let card = fields.iter().find(|(k, _)| k == "cardinal").and_then(|(_, v)| match v {
                    Value::Dict(d) => Some(d),
                    _ => None,
                })?;
                let int_val = card.iter().find(|(k, _)| k == "integer").and_then(|(_, v)| common::str_value(v))?;
                Some(int_val.replace('\u{a0}', " "))
            }
            "money" => {
                let neg = get_str("negative");
                let curr = get_str("currency");
                let int_part = get_str("integer_part")?;
                let frac_part = get_str("fractional_part");
                let morph = get_str("morphosyntactic_features");
                if let Some(c) = curr {
                    resolve_money(neg, c, int_part, frac_part, morph)
                } else {
                    let mut out = String::new();
                    if let Some(n) = neg {
                        out.push_str(n);
                        out.push(' ');
                    }
                    out.push_str(int_part);
                    if let Some(m) = morph {
                        out.push(' ');
                        out.push_str(m);
                    }
                    Some(out.replace('\u{a0}', " "))
                }
            }
            _ => None,
        }
    }
}

fn resolve_money(
    neg: Option<&str>,
    curr: &str,
    int_part: &str,
    frac_part: Option<&str>,
    morph: Option<&str>,
) -> Option<String> {
    let maj_noun = units::MAJOR_CURRENCIES.iter().find(|m| m.written == curr)?;
    let int_resolved = resolve_marked_phrase(int_part, maj_noun);

    let mut out = String::new();
    if let Some(n) = neg {
        out.push_str(n);
        out.push(' ');
    }
    out.push_str(&int_resolved);

    if let Some(frac) = frac_part {
        let min_noun = units::MINOR_CURRENCIES.iter().find(|m| m.written == curr)?;
        let frac_resolved = resolve_marked_phrase(frac, min_noun);
        out.push(' ');
        out.push_str(&frac_resolved);
    }

    if let Some(m) = morph {
        out.push(' ');
        out.push_str(m);
    }

    Some(out.replace('\u{a0}', " "))
}

fn resolve_marked_phrase(phrase: &str, noun: &units::Noun) -> String {
    if let Some(last_char) = phrase.chars().last() {
        let form_opt = match last_char {
            money::MARK_ONE => Some(units::Form::One),
            money::MARK_FEW => Some(units::Form::Few),
            money::MARK_MANY => Some(units::Form::Many),
            money::MARK_GEN => Some(units::Form::Gen),
            _ => None,
        };
        if let Some(form) = form_opt {
            let stripped = &phrase[..phrase.len() - last_char.len_utf8()];
            return units::agree(stripped, form, noun);
        }
    }
    format!("{phrase} {}", noun.many)
}

static GRAMMAR: LazyLock<Russian> =
    LazyLock::new(|| Russian { classifiers: vec![Box::new(Union)], punct: Punctuation::en(cost(1.1)) });

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

//! Bulgarian native text-normalization grammar.
mod datetime;
mod electronic;
mod graph_utils;
mod lexical;
mod measure;
mod money;
mod numeric;
mod ordinal;

use crate::lang::common::{self, Fields};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

struct Bulgarian {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punct,
}
struct Classes;
struct Punct;

/// Exactly the single-character punctuation alphabet in `bg/taggers/punctuation.py`.
#[inline]
fn punct(c: char) -> bool {
    matches!(
        c,
        '!' | '#'
            | '$'
            | '%'
            | '\''
            | '('
            | ')'
            | '*'
            | '+'
            | ','
            | '-'
            | '.'
            | '/'
            | ':'
            | ';'
            | '<'
            | '='
            | '>'
            | '?'
            | '@'
            | '^'
            | '_'
            | '`'
            | '{'
            | '|'
            | '}'
            | '~'
            | '"'
            | '«'
            | '»'
            | '„'
            | '“'
            | '”'
            | '‘'
            | '’'
            | '–'
            | '—'
            | '…'
            | '['
            | ']'
    )
}

#[inline]
fn boundary(c: char) -> bool {
    is_nemo_space(c) || punct(c)
}

thread_local! {
    static SCAN_BUFFER: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}

struct Scratch(String);
impl Scratch {
    fn new() -> Self {
        Self(SCAN_BUFFER.with_borrow_mut(std::mem::take))
    }
}
impl std::ops::Deref for Scratch {
    type Target = String;
    fn deref(&self) -> &String {
        &self.0
    }
}
impl std::ops::DerefMut for Scratch {
    fn deref_mut(&mut self) -> &mut String {
        &mut self.0
    }
}
impl Drop for Scratch {
    fn drop(&mut self) {
        self.0.clear();
        SCAN_BUFFER.with_borrow_mut(|buffer| {
            if self.0.capacity() > buffer.capacity() {
                *buffer = std::mem::take(&mut self.0);
            }
        });
    }
}

fn name(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 8 + s.bytes().filter(|&c| c == b' ').count());
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}
fn name_owned(mut value: String) -> String {
    if value.contains(' ') {
        return name(&value);
    }
    value.insert_str(0, "name: \"");
    value.push('"');
    value
}
fn push(out: &mut Vec<Candidate>, end: usize, value: String, weight: f64, rank: i32, tagged: bool) {
    out.push(Candidate { end, cost: cost(weight), rank, tag: if tagged { value } else { name_owned(value) } })
}

/// Only same-span token candidates participate: strictly cheaper paths dominate
/// later classes; equal costs must retain their source order and alternatives.
#[inline]
fn cheaper(out: &[Candidate], from: usize, bound: i64) -> bool {
    out[from..].iter().any(|candidate| candidate.cost < bound)
}
fn plain_cardinal(s: &str) -> Option<f64> {
    let bytes = s.as_bytes();
    if bytes.is_empty() || !bytes.iter().all(u8::is_ascii_digit) {
        return None;
    }
    if bytes.len() > 1 && bytes[0] == b'0' {
        return (bytes.len() <= 11).then_some(1.1);
    }
    if bytes.len() > 12 {
        return None;
    }
    let n = s.parse::<u64>().ok()?;
    if n >= 1_000_000_000 && n / 1_000_000 % 1000 == 1 {
        return None;
    }
    Some(1.2)
}
fn simple_clock(s: &str) -> Option<(f64, bool)> {
    let (hours, minutes) = s.split_once(':')?;
    if hours.is_empty()
        || hours.len() > 2
        || minutes.len() != 2
        || !hours.bytes().all(|b| b.is_ascii_digit())
        || !minutes.bytes().all(|b| b.is_ascii_digit())
    {
        return None;
    }
    let h: u8 = hours.parse().ok()?;
    let m: u8 = minutes.parse().ok()?;
    if h > 23 || m > 59 {
        return None;
    }
    Some((if m == 0 { 1.05 } else { 1.0 }, h == 0 && m == 0))
}

fn numeric_start(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '-' | '−' | '~' | '±' | '"')
}

fn money_start(c: char) -> bool {
    numeric_start(c)
        || matches!(
            c,
            '$' | '€'
                | '£'
                | '₽'
                | '¥'
                | '₹'
                | '₩'
                | '₺'
                | '₪'
                | '₴'
                | '₫'
                | '৳'
                | '₦'
                | '₱'
                | '฿'
                | '֏'
                | '¢'
                | 'E'
                | 'U'
                | 'G'
                | 'Р'
                | 'R'
                | 'Ј'
                | 'J'
                | 'C'
                | 'A'
                | 'М'
                | 'M'
                | 'I'
                | 's'
                | 'щ'
                | 'р'
                | 'е'
                | 'в'
        )
}

fn money_marker(c: char) -> bool {
    matches!(
        c,
        '$' | '€'
            | '£'
            | '₽'
            | '¥'
            | '₹'
            | '₩'
            | '₺'
            | '₪'
            | '₴'
            | '₫'
            | '৳'
            | '₦'
            | '₱'
            | '฿'
            | '֏'
            | '¢'
            | '.'
            | ','
            | 'E'
            | 'U'
            | 'G'
            | 'Р'
            | 'R'
            | 'Ј'
            | 'J'
            | 'C'
            | 'A'
            | 'М'
            | 'M'
            | 'I'
            | 'K'
            | 'T'
            | 'V'
            | 'B'
            | 'P'
            | 'H'
            | 'л'
            | 'с'
            | 'щ'
            | 'е'
            | 'в'
            | 'р'
    )
}

fn is_vulgar(c: char) -> bool {
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
            | '⅐'
            | '⅑'
            | '⅒'
    )
}
fn serial_possible(s: &str) -> bool {
    if s.starts_with('v')
        || s.starts_with('V')
        || s.starts_with("COVID-")
        || s.starts_with("Covid-")
        || s.starts_with("covid-")
        || matches!(s, "CO2" | "A4" | "BA249")
    {
        return true;
    }
    let Some((prefix, rest)) = s.split_once('-') else { return false };
    !prefix.is_empty()
        && prefix.chars().count() <= 4
        && rest.contains('-')
        && prefix
            .chars()
            .all(|c| c.is_ascii_alphabetic() || ('А'..='я').contains(&c) || matches!(c, 'ѐ' | 'ќ' | 'Ѐ' | 'Ќ'))
}

impl Classifier for Classes {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start != 0 && !boundary(text[start - 1]) {
            return;
        }
        let first = text[start];
        let numeric_first = numeric_start(first);
        let money_first = money_start(first);
        let roman_first = "ГгЧчТтСБПИКАНХЛЕIVXM C".contains(first);
        // Bulgarian model prefixes admit at most four alphabetic characters.
        let mut bulgarian_prefix = ('А'..='я').contains(&first) || matches!(first, 'ѐ' | 'ќ' | 'Ѐ' | 'Ќ');
        let mut prefix_letters = 0usize;
        let mut s = Scratch::new();
        let mut digit = false;
        let mut vulgar = false;
        let mut has_money_marker = false;
        let mut electronic_separator = false;
        let mut whitelist_alive = true;
        let mut colon = false;
        let mut ratio_alphabet = true;
        let mut cardinal_alphabet = true;
        let mut fraction_alphabet = true;
        let mut ordinal_alphabet = true;
        let mut decimal_alphabet = true;
        let mut decimal_separator = false;
        let mut date_marker = first == 'A';
        let mut date_alphabet = true;
        let mut time_valid = true;
        let mut phone_separators = 0;
        let mut phone_paren = false;
        let mut word = true;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            s.push(c);
            if whitelist_alive {
                whitelist_alive = lexical::whitelist_prefix(&s);
            }
            if bulgarian_prefix {
                if ('А'..='я').contains(&c) || matches!(c, 'ѐ' | 'ќ' | 'Ѐ' | 'Ќ') {
                    prefix_letters += 1;
                } else {
                    bulgarian_prefix = false;
                }
            }
            if bulgarian_prefix && prefix_letters > 4 && !money_first && !roman_first && !whitelist_alive {
                // All remaining source branches require a digit, an ASCII electronic
                // prefix, a known lexical/context prefix, or a <=4-letter model.
                // Preserve every word end before punctuation or whitespace.
                let mut word_end = end;
                loop {
                    if word_end == text.len() || boundary(text[word_end]) {
                        out.push(Candidate { end: word_end, cost: cost(100.0), rank: 13000, tag: String::new() });
                    }
                    if word_end == text.len() || is_nemo_space(text[word_end]) {
                        break;
                    }
                    word_end += 1;
                }
                break;
            }
            digit |= c.is_ascii_digit();
            let vulgar_char = is_vulgar(c);
            vulgar |= vulgar_char;
            if money_first && !has_money_marker {
                has_money_marker = money_marker(c);
            }
            electronic_separator |= matches!(c, '.' | '@');
            colon |= c == ':';
            ratio_alphabet &= c.is_ascii_digit() || matches!(c, ':' | '.' | ',' | ' ' | '\u{a0}');
            cardinal_alphabet &=
                c.is_ascii_digit() || matches!(c, ' ' | '\u{a0}' | '.' | ',' | '-' | '−' | '–' | '—' | ':' | '~' | '±');
            fraction_alphabet &= c.is_ascii_digit() || matches!(c, ' ' | '/' | '-' | '−') || vulgar_char;
            ordinal_alphabet &= ordinal::possible_char(c);
            decimal_alphabet &= c.is_ascii_digit() || matches!(c, ' ' | '\u{a0}' | '.' | ',' | '-' | '−');
            decimal_separator |= matches!(c, '.' | ',');
            date_marker |= matches!(c, '.' | '/' | '-' | '–' | '—' | ' ' | 'г' | 'п' | 'с' | 'о' | 'B' | 'A');
            if date_alphabet && (first.is_ascii_digit() || first == 'A') {
                date_alphabet = datetime::possible_char(c);
            }
            time_valid &= c.is_ascii_digit()
                || matches!(
                    c,
                    ':' | '.'
                        | '-'
                        | '–'
                        | '—'
                        | ' '
                        | 'A'
                        | 'M'
                        | 'P'
                        | 'a'
                        | 'm'
                        | 'p'
                        | 'u'
                        | 't'
                        | 'c'
                        | 'U'
                        | 'T'
                        | 'C'
                        | 'G'
                        | 'E'
                        | 'S'
                );
            phone_separators += usize::from(matches!(c, ' ' | '-' | '.'));
            phone_paren |= c == ')';
            if word && is_nemo_space(c) && !money_first && !matches!(first, '+' | '(') {
                let first_chunk = &s[..s.len() - c.len_utf8()];
                if c != ' ' || !lexical::multiword_prefix(first_chunk) {
                    break;
                }
            }
            word &= !is_nemo_space(c);
            // A digit-initial span containing ':' can only be a clock or ratio.
            // Once whitespace and an incompatible character have ruled out both,
            // extending it cannot produce a word or another classified branch.
            if first.is_ascii_digit() && colon && !time_valid && !ratio_alphabet && !word {
                break;
            }
            if end != text.len() && !boundary(text[end]) {
                continue;
            }
            let span_from = out.len();

            if whitelist_alive {
                for (v, w) in lexical::whitelist(&s) {
                    push(out, end, v, 1.01 + w, 0, false);
                }
            }
            if !digit && roman_first {
                for v in lexical::romans(&s) {
                    push(out, end, v, 1.02, 1000, false);
                }
            }
            if !colon && digit && date_marker && date_alphabet && (first.is_ascii_digit() || first == 'A') {
                for (v, w, t) in datetime::date(&s) {
                    push(out, end, v, 1.03 + w, 2000, t);
                }
            }
            // ISO dates cost 1.03; every remaining token branch costs at least 1.05.
            if cheaper(out, span_from, cost(1.05)) {
                continue;
            }
            if digit && time_valid && first.is_ascii_digit() && colon {
                if let Some((weight, midnight)) = simple_clock(&s) {
                    if midnight {
                        out.push(Candidate { end, cost: cost(1.04 + 0.99), rank: 3000, tag: String::new() });
                    }
                    out.push(Candidate { end, cost: cost(1.04 + weight), rank: 3000, tag: String::new() });
                } else {
                    for (v, w) in datetime::time(&s) {
                        push(out, end, v, 1.04 + w, 3000, false);
                    }
                }
            }
            if first.is_ascii_alphanumeric()
                && (electronic_separator || s.starts_with("http://") || s.starts_with("https://"))
            {
                for (v, w) in electronic::classify(&s) {
                    push(out, end, v, 1.05 + w, 4000, false);
                }
            }
            // The electronic IP branch costs 1.05; phone and later classes start at 1.06.
            if cheaper(out, span_from, cost(1.06)) {
                continue;
            }
            if !colon
                && digit
                && (phone_separators >= 3 || phone_paren && phone_separators >= 1)
                && (first.is_ascii_digit() || matches!(first, '+' | '('))
            {
                if let Some(v) = lexical::telephone(&s) {
                    push(out, end, v, 1.06, 5000, false);
                }
            }
            // Phone costs 1.06; the cheapest remaining fraction path costs 1.08.
            if cheaper(out, span_from, cost(1.08)) {
                continue;
            }
            if !colon && digit && money_first && has_money_marker && money::possible_end(c) {
                if let Some(count) = money::simple_lev(&s) {
                    for _ in 0..count {
                        out.push(Candidate { end, cost: cost(1.07 + 1.05), rank: 6000, tag: String::new() });
                    }
                } else if money::possible_body(&s) {
                    for (i, c) in s.char_indices() {
                        if !matches!(c, '-' | '–' | '—') {
                            continue;
                        }
                        let a = s[..i].trim_end_matches(' ');
                        let b = s[i + c.len_utf8()..].trim_start_matches(' ');
                        if a.is_empty() || b.is_empty() {
                            continue;
                        }
                        for (x, wx) in money::classify(a) {
                            for (y, wy) in money::classify(b) {
                                let tag = format!("{} }} tokens {{ name: \"до\" }} tokens {{ {}", name(&x), name(&y));
                                push(out, end, tag, 1.04 + wx + wy, 7000, true);
                            }
                        }
                    }
                    for (v, w) in money::classify(&s) {
                        push(out, end, v, 1.07 + w, 6000, false);
                    }
                }
            }
            if !colon && digit && numeric_first && measure::possible_end(c) {
                if let Some((weight, count)) = measure::simple_integer(&s) {
                    for _ in 0..count {
                        out.push(Candidate { end, cost: cost(1.08 + weight), rank: 8000, tag: String::new() });
                    }
                } else if measure::possible_body(&s) {
                    for (v, w) in measure::classify(&s) {
                        push(out, end, v, 1.08 + w, 8000, false);
                    }
                }
            }
            if !colon
                && fraction_alphabet
                && (vulgar || digit && s.contains('/'))
                && (numeric_first || is_vulgar(first))
            {
                for (v, w) in numeric::fraction(&s) {
                    push(out, end, v, 1.09 + w, 9000, false);
                }
            }
            // Fractions are at least 1.08; decimal and later classes start at 1.10.
            if cheaper(out, span_from, cost(1.10)) {
                continue;
            }
            if !colon && digit && numeric_first {
                if decimal_alphabet && decimal_separator {
                    if let Some(v) = numeric::decimal(&s) {
                        push(out, end, v, 1.10, 10000, false);
                    }
                }
            }
            // The next possible ordinal costs 2.11 and cardinal costs at least 2.12.
            if cheaper(out, span_from, cost(2.11)) {
                continue;
            }
            if !colon && digit && numeric_first && ordinal_alphabet && ordinal::possible_end(c) {
                for (v, w) in ordinal::classify(&s) {
                    push(out, end, v, 1.11 + w, 11000, false);
                }
            }
            if cheaper(out, span_from, cost(2.12)) {
                continue;
            }
            if (!colon || ratio_alphabet)
                && digit
                && ((numeric_first && (cardinal_alphabet || matches!(s.as_str(), "5G" | "4K")))
                    || (!numeric_first && serial_possible(&s)))
            {
                if s.bytes().all(|b| b.is_ascii_digit()) {
                    if let Some(w) = plain_cardinal(&s) {
                        out.push(Candidate { end, cost: cost(1.12 + w), rank: 12000, tag: String::new() });
                    }
                } else if s
                    .split_once(':')
                    .is_some_and(|(a, b)| plain_cardinal(a) == Some(1.2) && plain_cardinal(b) == Some(1.2))
                {
                    out.push(Candidate { end, cost: cost(1.12 + 1.5), rank: 12000, tag: String::new() });
                } else {
                    for (v, w) in numeric::cardinals(&s) {
                        push(out, end, v, 1.12 + w, 12000, false);
                    }
                }
            }
            if word && !cheaper(out, span_from, cost(100.0)) {
                out.push(Candidate { end, cost: cost(100.0), rank: 13000, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 13000 {
            let chars = &text[start..candidate.end];
            let mut tag = String::with_capacity(8 + chars.iter().map(|c| c.len_utf8()).sum::<usize>());
            tag.push_str("name: \"");
            tag.extend(chars.iter().copied());
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let mut s = Scratch::new();
        s.extend(text[start..candidate.end].iter().copied());
        let value = match candidate.rank {
            12000 => numeric::render_plain(&s),
            8000 => measure::render_simple_integer(&s),
            6000 => money::render_simple_lev(&s),
            3000 if candidate.cost == cost(1.04 + 0.99) => Some("нула часа".to_owned()),
            3000 => datetime::clock(&s).map(|(value, _)| value),
            _ => None,
        };
        if let Some(value) = value {
            candidate.tag = name_owned(value);
        }
    }
}
impl Classifier for Punct {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if let Some(&c) = text.get(start) {
            if punct(c) {
                out.push(Candidate { end: start + 1, cost: cost(1.1), rank: 0, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let c = text[start];
        let mut tag = String::with_capacity(8 + c.len_utf8());
        tag.push_str("name: \"");
        tag.push(c);
        tag.push('"');
        candidate.tag = tag;
    }
}
impl Grammar for Bulgarian {
    fn code(&self) -> &'static str {
        "bg"
    }
    fn shape(&self) -> Shape {
        Shape::SeparatedOrPunct
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(v) = common::verbalize_name(token) {
            return Some(v);
        }
        let (key, value) = common::class(token)?;
        if key != "date" {
            return None;
        }
        let Value::Dict(fields) = value else { return None };
        let mut f = Fields::new(fields);
        let d = f.take("day")?;
        let m = f.take("month")?;
        let y = f.take("year")?;
        if !f.done() || d.is_empty() || m.is_empty() || y.is_empty() {
            return None;
        }
        Some(format!("{d} {m} {y}").replace('\u{a0}', " "))
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        for (index, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if index != 0 {
                out.push(' ');
            }
            if let [(key, Value::Str(value))] = body.as_slice() {
                if key == "name" && !value.contains(' ') {
                    if value.contains('\u{a0}') {
                        out.extend(value.chars().map(|c| if c == '\u{a0}' { ' ' } else { c }));
                    } else {
                        out.push_str(value);
                    }
                    continue;
                }
            }
            out.push_str(&crate::permute::first_ordering(body, &mut |fields: &Dict| self.verbalize(fields))?);
        }
        Some(out)
    }
}
static GRAMMAR: LazyLock<Bulgarian> =
    LazyLock::new(|| Bulgarian { classifiers: vec![Box::new(Classes)], punct: Punct });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

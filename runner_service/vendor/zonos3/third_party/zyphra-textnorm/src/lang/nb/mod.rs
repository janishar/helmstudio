//! Norwegian Bokmål text normalization.
mod basic;
mod date;
mod electronic;
mod measure;
mod money;
mod number;
mod telephone;

use crate::lang::common::{self, Fields, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

struct Norwegian {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
struct Union {
    punct: Punctuation,
}
impl Grammar for Norwegian {
    fn code(&self) -> &'static str {
        "nb"
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
        if let Some(s) = common::verbalize_name(token) {
            return Some(s);
        }
        let mut f = Fields::new(token);
        let d = f.dict("date")?;
        if !f.done() {
            return None;
        }
        let mut d = Fields::new(d);
        let day = d.take("day")?;
        let month = d.take("month")?;
        let year = d.take("year")?;
        if !d.done() || day.is_empty() || month.is_empty() || year.is_empty() {
            return None;
        }
        Some(format!("{} {} {}", day, month, year).replace('\u{a0}', " "))
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
            if let [(key, Value::Str(name))] = body.as_slice() {
                if key == "name" {
                    if name.contains(' ') {
                        return None;
                    }
                    let mut parts = name.split('\u{a0}');
                    out.push_str(parts.next().unwrap_or(""));
                    for part in parts {
                        out.push(' ');
                        out.push_str(part);
                    }
                    continue;
                }
            }
            out.push_str(&crate::permute::first_ordering(body, &mut |d: &Dict| self.verbalize(d))?);
        }
        Some(out)
    }
}
fn phoneme(s: &str, punct: &Punctuation) -> bool {
    let Some(body) = s.strip_prefix('[').and_then(|s| s.strip_suffix(']')) else { return false };
    if body.is_empty() {
        return false;
    }
    // ARPAbet consists of ASCII-letter units with optional trailing digits.
    if body.split(' ').all(|unit| {
        let letters = unit.bytes().take_while(u8::is_ascii_alphabetic).count();
        letters > 0 && unit[letters..].bytes().all(|b| b.is_ascii_digit())
    }) {
        return true;
    }
    // The IPA source accepts repetitions of entries, with stress and punctuation delimiters.
    let symbols = include_str!("../../../data/en/whitelist/ipa_symbols.tsv");
    // Bits: initial, initial stress, inside a unit, delimiter, delimiter then stress.
    let mut reachable = vec![0u8; body.len() + 1];
    reachable[0] = 1;
    for (i, ch) in body.char_indices() {
        let state = reachable[i];
        if state == 0 {
            continue;
        }
        let next = i + ch.len_utf8();
        if matches!(ch, 'ˈ' | 'ˌ' | '\'') {
            if state & 1 != 0 {
                reachable[next] |= 2;
            }
            if state & 4 != 0 {
                reachable[next] |= 4;
            }
            if state & 8 != 0 {
                reachable[next] |= 16;
            }
        }
        if ch == ' ' || punct.is_mark(ch) {
            if state & (4 | 8) != 0 {
                reachable[next] |= 8;
            }
        }
        for entry in symbols.lines() {
            if !entry.is_empty() && body[i..].starts_with(entry) {
                reachable[i + entry.len()] |= 4;
            }
        }
    }
    reachable[body.len()] & (4 | 8) != 0
}
/// Recognize the ungrouped decimal paths without spelling their digits.
/// Returns the number of equal-cost readings in the current branch order.
fn simple_decimal(s: &str) -> Option<usize> {
    let s = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(s);
    let split = s.find(|c| c == ',' || c == '.')?;
    let a = &s[..split];
    let b = &s[split + 1..];
    if a.is_empty()
        || a.len() > 15
        || (a.len() > 1 && a.starts_with('0'))
        || !a.bytes().all(|c| c.is_ascii_digit())
        || b.is_empty()
        || !b.bytes().all(|c| c.is_ascii_digit())
    {
        return None;
    }
    let cardinal_fraction = b.len() <= 15 && (b.len() == 1 || !b.starts_with('0'));
    Some(usize::from(b.len() > 1) + usize::from(cardinal_fraction))
}

fn push_cardinal(out: &mut Vec<Candidate>, end: usize, n: u64) {
    let mut groups = n;
    let mut alternatives = 0u32;
    while groups != 0 {
        if (groups % 1000) / 100 == 1 {
            alternatives += 1;
        }
        groups /= 1000;
    }
    if (n / 1000) % 1000 == 1 {
        alternatives += 1;
    }
    for choice in 0..(1u32 << alternatives) {
        out.push(Candidate {
            end,
            cost: cost(1.07 + 0.1 * f64::from(choice.count_ones())),
            rank: 7000,
            tag: String::new(),
        });
    }
}

thread_local! {
    // Scratch only: candidates and their rendered values never borrow this.
    static SPAN_BUFFER: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if is_nemo_space(text[start]) || !common::possible_token_start(text, start) {
            return;
        }
        if matches!(text[start], '~' | '±') {
            let mut next = start + 1;
            if text.get(next) == Some(&' ') {
                next += 1;
            }
            if next < text.len() && !matches!(text[next], '~' | '±') {
                let mut inner = Vec::new();
                self.candidates(text, next, &mut inner);
                let prefix = if text[start] == '~' { "omtrent" } else { "pluss\u{a0}minus" };
                for mut item in inner {
                    if item.tag.is_empty() {
                        self.materialize(text, next, &mut item);
                    }
                    item.cost += cost(1.05);
                    item.tag = format!("name: \"{}\" }} tokens {{ {}", prefix, item.tag);
                    out.push(item);
                }
            }
        }
        let mut s = SPAN_BUFFER.with(|buffer| std::mem::take(&mut *buffer.borrow_mut()));
        s.clear();
        let mut has_digit = false;
        let mut has_vulgar = false;
        let mut has_electronic_mark = false;
        let mut has_currency_marker = false;
        let mut money_alive = true;
        let mut date_separators = 0u8;
        let mut date_word_or_dash = false;
        let mut has_time_mark = false;
        let mut has_phone_separator = false;
        let mut plain_digits = true;
        let mut has_ascii_space = false;
        let mut word_no_space = true;
        let mut word_default = true;
        let mut word_preferred = true;
        for end in start + 1..=text.len() {
            if text[end - 1] == ' ' && !has_ascii_space && text[start].is_alphabetic() {
                let crossing = matches!(
                    s.as_str(),
                    "f." | "bl."
                        | "nr"
                        | "nr."
                        | "Nr"
                        | "Nr."
                        | "No."
                        | "no."
                        | "den"
                        | "Den"
                        | "det"
                        | "kl"
                        | "kl."
                        | "Kl"
                        | "Kl."
                        | "klokken"
                        | "iOS"
                        | "ca."
                        | "kr"
                        | "kr."
                        | "Rs"
                        | "Rs."
                        | "US$"
                        | "CA$"
                        | "A$"
                        | "R$"
                        | "MX$"
                        | "Harald"
                        | "Olav"
                        | "Haakon"
                        | "Karl"
                        | "Gustav"
                        | "Louis"
                        | "Elizabeth"
                        | "Henrik"
                        | "Fredrik"
                        | "Christian"
                        | "Magnus"
                        | "Sverre"
                        | "Eirik"
                        | "Inge"
                        | "Sigurd"
                        | "Pave"
                        | "Kong"
                        | "Dronning"
                        | "Kapittel"
                        | "kapittel"
                        | "Del"
                        | "del"
                        | "Bind"
                        | "bind"
                        | "Akt"
                        | "akt"
                        | "Scene"
                        | "scene"
                        | "Bok"
                        | "bok"
                        | "Volum"
                        | "volum"
                        | "Vol."
                        | "vol."
                        | "Århundre"
                        | "århundre"
                        | "klasse"
                        | "Klasse"
                ) || (s.len() <= 3 && s.bytes().all(|b| b.is_ascii_uppercase()));
                if !crossing {
                    break;
                }
            }
            s.push(text[end - 1]);
            let ch = text[end - 1];
            has_ascii_space |= ch == ' ';
            plain_digits &= ch.is_ascii_digit();
            has_digit |= ch.is_ascii_digit();
            money_alive = money_alive && money::possible_char(ch);
            if matches!(ch, '.' | '/' | '-') {
                date_separators = date_separators.saturating_add(1);
            }
            date_word_or_dash |= ch.is_ascii_alphabetic() || matches!(ch, '-' | '–' | '—');
            has_time_mark |= ch.is_ascii_alphabetic() || matches!(ch, ':' | '.');
            has_phone_separator |= matches!(ch, ' ' | '-' | '.' | '(' | ')');
            word_no_space &= !is_nemo_space(ch);
            word_default = word_default && !self.punct.is_mark(ch);
            word_preferred &= !ch.is_ascii_digit() && !"$€₩£¥₹₦₽₱₪₺₴₫৳₵¢#%‰".contains(ch);
            has_currency_marker |= ch.is_ascii_uppercase()
                || matches!(
                    ch,
                    '$' | '€'
                        | '£'
                        | '¥'
                        | '₹'
                        | '₽'
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
                );
            if !has_currency_marker {
                has_currency_marker = match ch {
                    'r' => s.ends_with("kr") || s.ends_with("dollar"),
                    'o' => s.ends_with("euro"),
                    'n' => s.ends_with("yen"),
                    'd' => s.ends_with("britiske pund"),
                    _ => false,
                };
            }
            has_vulgar |= matches!(
                ch,
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
            );
            has_electronic_mark |= matches!(ch, '.' | '@' | ':' | '/' | '^');
            if !common::possible_token_end(text, end) {
                continue;
            }
            // Bare digits cannot enter another specialized branch. Recognize
            // all weighted cardinal alternatives from their groups; spell only
            // the selected candidate in `materialize`.
            if plain_digits {
                if s.starts_with('0') && s.len() > 1 {
                    out.push(Candidate { end, cost: cost(1.07), rank: 7000, tag: String::new() });
                } else if s.len() <= 15 {
                    push_cardinal(out, end, s.parse().expect("ASCII digits"));
                } else {
                    // No cardinal path exists beyond the source's 15-digit
                    // limit, so its fallback word remains necessary.
                    out.push(Candidate { end, cost: cost(100.0), rank: 13000, tag: String::new() });
                }
                continue;
            }
            // All branches below are classified-token transitions for this
            // same end. A strictly cheaper accepted path dominates a more
            // expensive branch regardless of its spoken name.
            let cheapest = std::cell::Cell::new(i64::MAX);
            let mut add = |value: String, weight: f64, rank: i32| {
                let c = cost(weight);
                cheapest.set(cheapest.get().min(c));
                let tag = format!("name: \"{}\"", value.replace(' ', "\u{a0}"));
                out.push(Candidate { end, cost: c, rank, tag });
            };
            for (value, w) in basic::whitelist(&s) {
                add(value, 1.01 + w, 0);
            }
            if cheapest.get() >= cost(1.02)
                && has_digit
                && (matches!(text[start], '+' | '(') || has_phone_separator)
                && (text[start].is_ascii_digit() || matches!(text[start], '+' | '('))
                && (ch.is_ascii_digit() || matches!(ch, ' ' | '-' | '.' | '(' | ')'))
            {
                for (value, w) in telephone::parse(&s) {
                    add(value, 1.02 + w, 1000);
                }
            }
            if has_ascii_space && cheapest.get() >= cost(1.03) {
                for value in basic::roman(&s) {
                    add(value, 1.03, 2000);
                }
            }
            let digit_start = text[start].is_ascii_digit();
            let numeric_start = digit_start || matches!(text[start], '-' | '−');
            let decimal_readings = if numeric_start { simple_decimal(&s) } else { None };
            let cardinal_value = if numeric_start && ch.is_ascii_digit() {
                number::parse_cardinal(s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(&s))
            } else {
                None
            };
            let measure_readings =
                if digit_start && !ch.is_ascii_digit() { measure::simple_cardinal(&s) } else { None };
            let money_possible = money_alive
                && has_digit
                && (has_currency_marker || s.ends_with(",-") || s.ends_with(",00") || s.ends_with(".00"))
                && money::possible_end(&s);
            let money_readings =
                if money_possible { money::simple_cardinal(&s).or_else(|| money::simple_decimal(&s)) } else { None };
            if cardinal_value.is_some() {
                cheapest.set(cheapest.get().min(cost(1.07)));
            }
            if decimal_readings.is_some_and(|n| n > 0) {
                cheapest.set(cheapest.get().min(cost(1.08)));
            }
            if money_readings.is_some_and(|n| n > 0) {
                cheapest.set(cheapest.get().min(cost(1.09)));
            }
            if measure_readings.is_some_and(|n| n > 0) {
                cheapest.set(cheapest.get().min(cost(1.10)));
            }
            if has_digit {
                if cheapest.get() >= cost(1.04)
                    && has_time_mark
                    && (digit_start || s.starts_with("kl") || s.starts_with("Kl"))
                    && !is_nemo_space(ch)
                    && basic::time_possible_end(&s)
                {
                    for value in basic::time(&s) {
                        add(value, 1.04, 3000);
                    }
                }
                if cheapest.get() >= cost(1.05)
                    && digit_start
                    && (date_word_or_dash || date_separators >= 2)
                    && !is_nemo_space(ch)
                    && date::possible_end(&s)
                {
                    for (value, w) in date::parse(&s) {
                        add(value, 1.05 + w, 4000);
                    }
                }
                if cheapest.get() >= cost(1.06)
                    && (digit_start || s.starts_with("den ") || s.starts_with("Den "))
                    && matches!(ch, '.' | 'e')
                {
                    for (value, w) in basic::ordinal(&s) {
                        add(value, 1.06 + w, 6000);
                    }
                }
                if cheapest.get() >= cost(1.07) && numeric_start && ch.is_ascii_digit() && cardinal_value.is_none() {
                    for (value, w) in basic::cardinal(&s) {
                        add(value, 1.07 + w, 7000);
                    }
                }
                if cheapest.get() >= cost(1.08)
                    && decimal_readings.is_none()
                    && (numeric_start || text[start] == '±')
                    && (ch.is_ascii_digit() || matches!(ch, '%' | '‰'))
                {
                    for value in basic::decimal(&s) {
                        add(value, 1.08, 8000);
                    }
                }
                if cheapest.get() >= cost(1.09) && money_possible && money_readings.is_none() {
                    for value in money::parse(&s) {
                        add(value, 1.09, 9000);
                    }
                }
            }
            if (has_digit || has_vulgar) && (numeric_start || has_vulgar || text[start] == '~' || s.starts_with("ca."))
            {
                // Two slash-fraction branches in a range can each contribute
                // -0.1, making 0.90 the complete measure minimum.
                if cheapest.get() >= cost(0.90) && !ch.is_ascii_digit() && measure_readings.is_none() {
                    for (value, w) in measure::parse(&s) {
                        add(value, 1.10 + w, 10000);
                    }
                }
                if cheapest.get() >= cost(1.01) && (numeric_start || has_vulgar) && (ch.is_ascii_digit() || has_vulgar)
                {
                    for (value, w) in basic::fraction(&s) {
                        add(value, 1.11 + w, 11000);
                    }
                }
            }
            if cheapest.get() >= cost(1.02)
                && (has_digit || has_electronic_mark)
                && (word_no_space || s.starts_with("iOS "))
            {
                for (value, w) in electronic::parse(&s) {
                    add(value, 1.12 + w, 12000);
                }
            }
            if cheapest.get() >= cost(1.09) {
                if let Some(count) = money_readings {
                    for _ in 0..count {
                        out.push(Candidate { end, cost: cost(1.09), rank: 9000, tag: String::new() });
                    }
                }
            }
            if cheapest.get() >= cost(1.10) {
                if let Some(count) = measure_readings {
                    for _ in 0..count {
                        out.push(Candidate { end, cost: cost(1.10), rank: 10000, tag: String::new() });
                    }
                }
            }
            if cheapest.get() >= cost(1.07) {
                if let Some(n) = cardinal_value {
                    push_cardinal(out, end, n);
                }
            }
            if cheapest.get() >= cost(1.08) {
                if let Some(count) = decimal_readings {
                    for _ in 0..count {
                        out.push(Candidate { end, cost: cost(1.08), rank: 8000, tag: String::new() });
                    }
                }
            }
            if cheapest.get() >= cost(1.05)
                && digit_start
                && s.len() == 10
                && ch.is_ascii_digit()
                && s.as_bytes().get(4) == Some(&b'-')
            {
                for (day, month, year, w) in date::iso(&s) {
                    cheapest.set(cheapest.get().min(cost(1.05 + w)));
                    out.push(Candidate {
                        end,
                        cost: cost(1.05 + w),
                        rank: 5000,
                        tag: format!("date {{ year: \"{}\" month: \"{}\" day: \"{}\" }}", year, month, day),
                    });
                }
            }
            let is_phoneme =
                cheapest.get() >= cost(100.0) && s.starts_with('[') && s.ends_with(']') && phoneme(&s, &self.punct);
            if is_phoneme {
                out.push(Candidate { end, cost: cost(100.0), rank: 13000, tag: String::new() });
            }
            if cheapest.get() >= cost(99.9999) && !is_phoneme && word_no_space {
                if word_default || word_preferred {
                    out.push(Candidate {
                        end,
                        cost: cost(if word_preferred { 99.9999 } else { 100.0 }),
                        rank: 13000,
                        tag: String::new(),
                    });
                }
            }
        }
        SPAN_BUFFER.with(|buffer| *buffer.borrow_mut() = s);
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.tag.is_empty() {
            if candidate.rank == 13000 {
                let mut tag = String::with_capacity(candidate.end - start + 8);
                tag.push_str("name: \"");
                for &ch in &text[start..candidate.end] {
                    tag.push(if ch == ' ' { '\u{a0}' } else { ch });
                }
                tag.push('"');
                candidate.tag = tag;
                return;
            }
            let value: String = text[start..candidate.end].iter().collect();
            let spoken = if candidate.rank == 7000 {
                if value.len() > 1 && value.starts_with('0') {
                    basic::digits(&value).expect("leading-zero candidate")
                } else if candidate.cost == cost(1.07) {
                    if let Some(body) = value.strip_prefix('-').or_else(|| value.strip_prefix('−')) {
                        format!("minus {}", number::cardinal(body, false).expect("cardinal candidate"))
                    } else {
                        number::cardinal(&value, false).expect("cardinal candidate")
                    }
                } else {
                    basic::cardinal(&value)
                        .into_iter()
                        .find(|(_, w)| cost(1.07 + w) == candidate.cost)
                        .expect("cardinal candidate")
                        .0
                }
            } else if candidate.rank == 8000 {
                basic::decimal(&value).into_iter().next().expect("decimal candidate")
            } else if candidate.rank == 9000 {
                money::verbalize_simple(&value).expect("currency candidate")
            } else if candidate.rank == 10000 {
                measure::verbalize_simple(&value).expect("measurement candidate")
            } else {
                value
            };
            let mut tag = String::with_capacity(spoken.len() + 8);
            tag.push_str("name: \"");
            for ch in spoken.chars() {
                tag.push(if ch == ' ' { '\u{a0}' } else { ch });
            }
            tag.push('"');
            candidate.tag = tag;
        }
    }
}
static GRAMMAR: LazyLock<Norwegian> = LazyLock::new(|| Norwegian {
    classifiers: vec![Box::new(Union { punct: Punctuation::en(cost(1.1)) })],
    punct: Punctuation::en(cost(1.1)),
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

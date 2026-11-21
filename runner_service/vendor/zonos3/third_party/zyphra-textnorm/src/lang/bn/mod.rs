//! Bengali text normalization grammar.
mod date;
mod electronic;
mod lazy;
mod measure;
mod money;
mod numbers;
mod numeric;
mod roman;
mod serial;
mod telephone;
mod time;
mod verbalize;
mod whitelist;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, cost, is_nemo_space};
use std::sync::LazyLock;

thread_local! {
    static SPAN_INPUT: std::cell::Cell<String> = const { std::cell::Cell::new(String::new()) };
}

struct Union {
    marks: Punctuation,
}

impl Union {
    fn generate(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let suffix = &text[start..];
        let mut input = SPAN_INPUT.take();
        input.clear();
        input.extend(suffix);

        let first = text[start];
        let first_is_digit = numbers::is_digit(first);
        let phone_possible = telephone::possible_start(&input);
        let date_possible = date::possible_start(&input);
        let money_start = money::possible_start(first);
        let currency_prefix = money_start && money::has_currency_prefix(&input);
        let money_possible =
            money_start && (first_is_digit || currency_prefix || matches!(first, '-' | '−' | '~' | '±'));
        let measure_number_start = first_is_digit || matches!(first, '-' | '−' | '~' | '±');
        let numeric_start = first_is_digit
            || matches!(
                first,
                '-' | '−'
                    | '~'
                    | '±'
                    | '½'
                    | '¼'
                    | '¾'
                    | '⅓'
                    | '⅔'
                    | '⅕'
                    | '⅖'
                    | '⅗'
                    | '⅘'
                    | '⅙'
                    | '⅚'
                    | '⅐'
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
                    | '⅑'
                    | '⅒'
            );
        let whitelist_dynamic = whitelist::is_dynamic_start(&input, first);
        let whitelist_ends = whitelist::ends_with_abbreviation(&input);

        let mut date_live = date_possible;
        let mut time_possible = first_is_digit;
        let mut money_live = money_possible;
        let mut currency_seen = false;
        let mut measure_live = measure_number_start;
        let mut measure_dimension = false;
        let mut measure_structural = true;
        let mut measure_lexical_end: Option<usize> = None;
        let mut phone_live = phone_possible;
        let mut numeric_live = numeric_start;
        let mut numeric_structural = true;
        let mut numeric_lexical_end: Option<usize> = None;
        let mut has_digit = false;
        let mut has_colon = false;
        let mut date_separator = false;
        let mut phone_plain = true;
        let mut byte_end = 0;

        let mut roman_possible = roman::possible_start(&input, first);
        let mut electronic_possible = true;
        let mut serial_possible = serial::possible_prefix(&input);
        let mut serial_marker = false;
        let mut serial_digit = false;
        let mut serial_alpha = false;
        let mut serial_power = false;
        let mut serial_dots = 0u8;
        let mut previous_alpha = false;
        let mut previous_digit = false;

        for end in start + 1..=text.len() {
            let c = text[end - 1];
            byte_end += c.len_utf8();
            let is_digit = numbers::is_digit(c);

            if !has_digit {
                has_digit = is_digit
                    || matches!(
                        c,
                        '½' | '¼'
                            | '¾'
                            | '⅓'
                            | '⅔'
                            | '⅕'
                            | '⅖'
                            | '⅗'
                            | '⅘'
                            | '⅙'
                            | '⅚'
                            | '⅐'
                            | '⅛'
                            | '⅜'
                            | '⅝'
                            | '⅞'
                            | '⅑'
                            | '⅒'
                    );
            }
            if date_live || time_possible {
                has_colon |= c == ':';
            }
            if date_live {
                date_live = date::possible_char(c);
            }
            if time_possible {
                time_possible = is_digit || c.is_ascii_alphabetic() || matches!(c, ':' | '.' | ' ' | '-' | '–');
            }
            if date_live && first_is_digit {
                date_separator |= matches!(c, '-' | '/' | '.' | '–' | ' ' | '\'' | 's');
            }
            if measure_live {
                measure_dimension |= matches!(c, 'x' | 'X' | '*');
                measure_live = measure::number_char(c)
                    || ('\u{0980}'..='\u{09FF}').contains(&c)
                    || c.is_ascii_alphabetic()
                    || matches!(c, '%' | '‰' | '°' | '¢' | '²' | '³' | 'µ' | 'μ' | 'Ω' | 'Ω' | '/' | '₹' | '℃' | '℉');
                if measure_live && measure_structural && !measure::number_char(c) && measure::lexical_bound_safe() {
                    measure_structural = false;
                    measure_lexical_end = measure::last_unit_end(&input, byte_end - c.len_utf8());
                }
                if !measure_structural && measure_lexical_end.is_none_or(|last| byte_end > last) {
                    measure_live = false;
                }
            }
            if money_live {
                money_live = money::possible_char(c);
                if money_live && !currency_seen {
                    currency_seen = money::currency_ending(&input[..byte_end], c);
                }
            }
            if numeric_live && numeric_structural && !numeric::structural_char(c) {
                numeric_structural = false;
                numeric_lexical_end = numeric::last_lexical_end(&input, byte_end - c.len_utf8());
            }
            if numeric_live && !numeric_structural && numeric_lexical_end.is_none_or(|last| byte_end > last) {
                numeric_live = false;
            }
            if phone_live {
                phone_plain = telephone::plain_char(c);
                phone_live = phone_plain;
            }
            if electronic_possible {
                electronic_possible = c.is_ascii_alphanumeric()
                    || is_digit
                    || electronic::is_subscript(c)
                    || ".-_/$\\()+–#?&=%:@ ".contains(c);
            }
            if serial_possible {
                serial_digit |= is_digit;
                serial_alpha |= serial::alpha(c);
                serial_power |= c == '^';
                if c == '.' {
                    serial_dots = serial_dots.saturating_add(1);
                }
                serial_possible = !c.is_whitespace();
                if serial_possible && !serial_marker {
                    let is_a = serial::alpha(c);
                    serial_marker |=
                        matches!(c, '-' | '/' | '.' | '^') || (previous_alpha && is_digit) || (previous_digit && is_a);
                    previous_alpha = is_a;
                    previous_digit = is_digit;
                }
            }
            if roman_possible {
                roman_possible = roman::possible_char(c);
            }

            if !numeric_live
                && !date_live
                && !time_possible
                && !money_live
                && !measure_live
                && !phone_live
                && !serial_possible
                && !electronic_possible
                && !roman_possible
                && !whitelist_dynamic
                && whitelist_ends.iter().all(|&last| last < byte_end)
            {
                break;
            }

            if (end < text.len() && !is_nemo_space(text[end]) && !self.marks.is_mark(text[end]))
                || !crate::lang::common::possible_token_end(text, end)
            {
                continue;
            }

            let s = &input[..byte_end];
            if whitelist_dynamic || whitelist_ends.contains(&byte_end) {
                whitelist::classify(s, end, out);
            }
            if has_digit {
                if numeric_live && (numeric_structural || numeric::lexical_ending(s)) {
                    numeric::classify(s, end, out);
                }
                if date_live
                    && !has_colon
                    && date::possible_end(s, c)
                    && (!first_is_digit || date_separator)
                    && date::might_match(s)
                {
                    date::classify(s, end, out);
                }
                if has_colon && time_possible {
                    time::classify(s, end, out);
                }
                if money_live
                    && currency_seen
                    && (currency_prefix || (money::possible_end(c) && money::matching_suffix(s, c)))
                {
                    money::classify(s, end, out);
                }
                if phone_live {
                    telephone::classify_hinted(s, end, out, phone_plain);
                }
            }
            if measure_live && measure::possible_end(s, c, measure_dimension) {
                measure::classify(s, end, out);
            }
            if electronic_possible {
                electronic::classify(s, end, out);
            }
            if serial_possible && serial_marker && serial_digit && (serial_alpha || serial_power || serial_dots >= 2) {
                serial::classify(s, end, out);
            }
            if roman_possible {
                roman::classify(s, end, out);
            }
        }

        input.clear();
        SPAN_INPUT.set(input);

        // Word graph fallback: accepts any non-space token (except forbidden non-word symbols)
        let mut has_word = false;
        let mut bn_chars = 0i64;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if is_nemo_space(c)
                || matches!(
                    c,
                    '$' | '€'
                        | '₩'
                        | '£'
                        | '¥'
                        | '₹'
                        | '₨'
                        | '₦'
                        | '৳'
                        | '֏'
                        | '₽'
                        | '₫'
                        | '₺'
                        | '₪'
                        | '₴'
                        | '₱'
                        | '฿'
                        | '¢'
                        | '#'
                        | '%'
                        | '‰'
                        | '°'
                        | '℃'
                        | '℉'
                        | 'Ω'
                        | 'Ω'
                        | '²'
                        | '³'
                        | '±'
                        | '©'
                        | '§'
                        | '®'
                        | '™'
                        | '½'
                        | '¼'
                        | '¾'
                        | '⅓'
                        | '⅔'
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
            {
                break;
            }
            let mark = self.marks.is_mark(c);
            if !mark {
                has_word = true;
                if serial::is_bn_char(c) {
                    bn_chars += 1;
                }
            }
            if has_word
                && (end == text.len() || is_nemo_space(text[end]) || self.marks.is_mark(text[end]))
                && crate::lang::common::possible_token_end(text, end)
            {
                out.push(Candidate {
                    end,
                    cost: cost(100.0) + cost(-0.0001) * bn_chars as Cost,
                    rank: 13,
                    tag: lazy::tag(|| {
                        let s: String = text[start..end].iter().collect();
                        format!("name: \"{}\"", s.replace(' ', "\u{a0}"))
                    }),
                });
            }
        }
    }
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if (start > 0 && !is_nemo_space(text[start - 1]) && !self.marks.is_mark(text[start - 1]))
            || !crate::lang::common::possible_token_start(text, start)
        {
            return;
        }
        lazy::with_build(false, || self.generate(text, start, out));
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 13 {
            candidate.tag.reserve(
                8 + text[start..candidate.end]
                    .iter()
                    .map(|c| if *c == ' ' { '\u{a0}'.len_utf8() } else { c.len_utf8() })
                    .sum::<usize>(),
            );
            candidate.tag.push_str("name: \"");
            for &c in &text[start..candidate.end] {
                candidate.tag.push(if c == ' ' { '\u{a0}' } else { c });
            }
            candidate.tag.push('"');
            return;
        }
        if candidate.rank == 1 {
            if let Some(tag) = numeric::materialize_short_cardinal(&text[start..candidate.end], candidate.cost) {
                candidate.tag = tag;
                return;
            }
        }
        if candidate.rank == 1
            && numeric::materialize_cardinal_chars(&text[start..candidate.end], candidate.cost, &mut candidate.tag)
        {
            return;
        }
        if candidate.rank == 5 {
            if let Some(tag) = time::materialize_plain_chars(&text[start..candidate.end], candidate.cost) {
                candidate.tag = tag;
                return;
            }
        }

        let input: String = text[start..candidate.end].iter().collect();
        if candidate.rank == 1 {
            if let Some(tag) = lazy::with_build(true, || numeric::materialize_plain_cardinal(&input, candidate.cost)) {
                candidate.tag = tag;
                return;
            }
        }
        if candidate.rank == 5 {
            if let Some(tag) = time::materialize_plain(&input, candidate.cost) {
                candidate.tag = tag;
                return;
            }
        }
        let mut matches = Vec::new();
        lazy::with_build(true, || match candidate.rank {
            0 => whitelist::classify(&input, candidate.end, &mut matches),
            1 | 2 | 3 | 9 => numeric::classify(&input, candidate.end, &mut matches),
            4 => date::classify(&input, candidate.end, &mut matches),
            5 => time::classify(&input, candidate.end, &mut matches),
            6 => measure::classify(&input, candidate.end, &mut matches),
            7 => money::classify(&input, candidate.end, &mut matches),
            8 => telephone::classify(&input, candidate.end, &mut matches),
            10 => electronic::classify(&input, candidate.end, &mut matches),
            11 => serial::classify(&input, candidate.end, &mut matches),
            12 => roman::classify(&input, candidate.end, &mut matches),
            _ => {}
        });

        if let Some(found) =
            matches.into_iter().find(|c| c.end == candidate.end && c.cost == candidate.cost && c.rank == candidate.rank)
        {
            candidate.tag = found.tag;
        }
    }
}

struct Bn {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for Bn {
    fn code(&self) -> &'static str {
        "bn"
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
        verbalize::verbalize(token)
    }

    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(name, Value::Str(value))] = body.as_slice() {
                if name == "name" {
                    if value.is_empty() || value.contains(' ') {
                        return None;
                    }
                    out.push_str(value);
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, Value::Str(value))] = fields.as_slice() {
                        if key == "integer" && !value.contains('"') {
                            out.push_str(value);
                            continue;
                        }
                    }
                }
                if class == "time" {
                    match fields.as_slice() {
                        [(h_key, Value::Str(hour))] if h_key == "hours" && !hour.contains('"') => {
                            out.push_str(hour);
                            out.push_str("টা");
                            continue;
                        }
                        [(h_key, Value::Str(hour)), (m_key, Value::Str(minute))]
                            if h_key == "hours"
                                && m_key == "minutes"
                                && !hour.contains('"')
                                && !minute.contains('"') =>
                        {
                            out.push_str(hour);
                            out.push_str("টা ");
                            out.push_str(minute);
                            out.push_str(" মিনিট");
                            continue;
                        }
                        _ => {}
                    }
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
}

static GRAMMAR: LazyLock<Bn> = LazyLock::new(|| {
    let make =
        || Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, std::iter::empty::<&'static str>(), cost(2.1));
    Bn { classifiers: vec![Box::new(Union { marks: make() })], punct: make() }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

//! Telugu text normalization grammar.

mod composite;
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

fn is_te_char(c: char) -> bool {
    (('\u{0c00}'..='\u{0c7f}').contains(&c) && !('\u{0c66}'..='\u{0c6f}').contains(&c))
        || c == '\u{200c}'
        || c == '\u{200d}'
}

fn prefixed_numeric(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (modifier, rest) = if let Some(rest) = s.strip_prefix('~') {
        ("సుమారు", rest)
    } else if let Some(rest) = s.strip_prefix('±') {
        ("ప్లస్\u{a0}మైనస్", rest)
    } else {
        return;
    };
    let rest = rest.strip_prefix(' ').unwrap_or(rest);
    if rest.is_empty() {
        return;
    }
    let mut inner = Vec::new();
    numeric::classify_cardinal(rest, end, &mut inner);
    numeric::classify_decimal(rest, end, &mut inner);
    numeric::classify_fraction(rest, end, &mut inner);
    measure::classify(rest, end, &mut inner);
    money::classify(rest, end, &mut inner);
    for candidate in inner {
        out.push(Candidate {
            end,
            cost: candidate.cost + cost(-2.0),
            rank: candidate.rank + 20,
            tag: lazy::tag(|| format!("name: \"{modifier}\" }} tokens {{ {}", candidate.tag)),
        });
    }
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
        let phone_possible = telephone::possible_start(&input, first);
        let date_possible = date::possible_start(&input);
        let money_start = money::possible_start(first);
        let currency_prefix = money_start && money::has_currency_prefix(&input);
        let money_possible = money_start
            && (first_is_digit || currency_prefix || matches!(first, '-' | '−' | '~' | '±'))
            && money::possible_currency(&input);
        let measure_number_start = first_is_digit || matches!(first, '-' | '−' | '~' | '±');
        let measure_end = if measure_number_start || matches!(first, '½' | '¼' | '¾') {
            measure::last_possible_end(&input)
        } else {
            None
        };
        let modifier_rest = input.strip_prefix('~').or_else(|| input.strip_prefix('±'));
        let modifier_rest = modifier_rest.map(|rest| rest.strip_prefix(' ').unwrap_or(rest));
        let modifier_offset = modifier_rest.map_or(0, |rest| input.len() - rest.len());
        let modifier_measure_end = modifier_rest.and_then(measure::last_possible_end).map(|end| modifier_offset + end);
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
        let whitelist_end = whitelist::last_abbreviation_end(&input);
        let mut composite_live = composite::possible_start(first);

        let mut date_live = date_possible;
        let mut time_possible = first_is_digit;
        let mut money_live = money_possible;
        let mut measure_live = (measure_number_start || matches!(first, '½' | '¼' | '¾')) && measure_end.is_some();
        let mut phone_live = phone_possible;
        let mut numeric_live = numeric_start;
        let mut numeric_structural = true;
        let mut numeric_lexical_end = None;
        let modifier = matches!(first, '~' | '±');
        let mut modifier_measure = modifier_measure_end.is_some();
        let mut modifier_money = modifier_rest.is_some_and(money::possible_currency);
        let mut modifier_numeric = modifier;
        let mut phone_plain = phone_possible && (first_is_digit || matches!(first, '+' | '('));
        let mut phone_context = phone_possible && !matches!(first, '+' | '(');
        let mut phone_number_run = first_is_digit;
        let mut has_digit = false;
        let mut date_marker = false;
        let mut has_colon = false;
        let mut byte_end = 0;

        let mut roman_possible = input.bytes().any(|b| matches!(b, b'I' | b'V' | b'X' | b'L' | b'C' | b'D' | b'M'))
            || roman::CARDINAL_KEYS.iter().any(|k| input.starts_with(k));
        let mut electronic_possible = true;
        let mut electronic_marker = false;
        let mut serial_possible = true;
        let mut serial_marker = false;
        let mut serial_prev_digit = false;
        let mut serial_prev_alpha = false;

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
            date_marker |= !is_digit;
            if date_live || time_possible {
                has_colon |= c == ':';
            }
            if date_live {
                date_live =
                    date::possible_char(c) && (!first_is_digit || date::possible_numeric_prefix(&input[..byte_end]));
            }
            if time_possible {
                time_possible = is_digit || c.is_ascii_alphabetic() || matches!(c, ':' | '.' | ' ' | '-' | '–');
            }
            if money_live {
                money_live = money::possible_char(c);
            }
            if measure_live {
                measure_live = measure::possible_char(c) && measure_end.is_some_and(|last| byte_end <= last);
            }
            if numeric_live && numeric_structural && !(numeric::structural_char(c) || (end == start + 1 && c == '-')) {
                numeric_structural = false;
                numeric_lexical_end = numeric::last_lexical_end(&input, byte_end - c.len_utf8());
            }
            if numeric_live && !numeric_structural {
                numeric_live = numeric_lexical_end.is_some_and(|last| byte_end <= last);
            }
            if modifier {
                modifier_measure &=
                    measure::possible_char(c) && modifier_measure_end.is_some_and(|last| byte_end <= last);
                modifier_money &= money::possible_char(c);
                let rest = &input[first.len_utf8()..byte_end];
                let rest = rest.strip_prefix(' ').unwrap_or(rest);
                modifier_numeric &= numeric::possible_prefix(rest);
            }
            if phone_live {
                if phone_number_run && !is_digit {
                    phone_number_run = false;
                    phone_context = c == ' ' && telephone::possible_context_after(&input[byte_end..]);
                }
                phone_plain &= is_digit || matches!(c, '+' | '(' | ')' | ' ' | '-' | '.');
                phone_live = phone_plain || phone_context;
            }
            composite_live &= is_digit || matches!(c, '©' | '§' | ':' | '-' | '–' | '—' | ' ');
            serial_possible &= !is_nemo_space(c);
            if serial_possible && !serial_marker {
                let alpha = c.is_ascii_alphabetic() || serial::is_telugu_char(c);
                serial_marker = matches!(c, '-' | '/' | '.' | '^')
                    || (serial_prev_alpha && is_digit)
                    || (serial_prev_digit && alpha);
                serial_prev_alpha = alpha;
                serial_prev_digit = is_digit;
            }
            if electronic_possible {
                electronic_marker |= matches!(c, '.' | '@' | '/' | '\\') || electronic::is_subscript(c);
                electronic_possible = c.is_ascii_alphanumeric()
                    || is_digit
                    || electronic::is_subscript(c)
                    || ".-_/$\\()+–#?&=%:@ ".contains(c);
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
                && !composite_live
                && !modifier_measure
                && !modifier_money
                && !modifier_numeric
                && !whitelist_dynamic
                && whitelist_end < byte_end
            {
                break;
            }

            if (end < text.len() && !is_nemo_space(text[end]) && !self.marks.is_mark(text[end]))
                || !crate::lang::common::possible_token_end(text, end)
            {
                continue;
            }

            let s = &input[..byte_end];
            if modifier && (modifier_measure || modifier_money || modifier_numeric) {
                prefixed_numeric(s, end, out);
            }
            if whitelist_dynamic || (byte_end <= whitelist_end && whitelist::is_abbreviation(s)) {
                whitelist::classify(s, end, out);
            }
            if composite_live {
                composite::classify(s, end, out);
            }
            if has_digit {
                if numeric_live && numeric::possible_end(c) {
                    numeric::classify(s, end, out);
                }
                if date_live && date_marker && !has_colon {
                    date::classify(s, end, out);
                }
                if time_possible && (has_colon || c.is_ascii_alphabetic() || c == '.') {
                    time::classify(s, end, out);
                }
                if money_live {
                    money::classify(s, end, out);
                }
                if phone_live && telephone::possible_end(c) {
                    telephone::classify(s, end, out);
                }
            }
            if measure_live {
                measure::classify(s, end, out);
            }
            if electronic_possible && electronic_marker {
                electronic::classify(s, end, out);
            }
            if serial_possible && serial_marker {
                serial::classify(s, end, out);
            }
            if roman_possible {
                roman::classify(s, end, out);
            }
        }

        input.clear();
        SPAN_INPUT.set(input);

        // Word graph fallback
        let mut has_word = false;
        let mut te_segments = 0i64;
        let mut segment_has_chars = false;
        let mut segment_is_te = true;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            let mark = self.marks.is_mark(c);
            if is_nemo_space(c)
                || (!mark
                    && matches!(
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
                            | '₵'
                            | '₸'
                            | '₼'
                            | '₾'
                            | '‰'
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
                    ))
            {
                break;
            }
            if mark {
                if segment_has_chars && segment_is_te {
                    te_segments += 1;
                }
                segment_has_chars = false;
                segment_is_te = true;
            } else {
                has_word = true;
                segment_has_chars = true;
                segment_is_te &= is_te_char(c);
            }
            if has_word
                && (end == text.len() || is_nemo_space(text[end]) || self.marks.is_mark(text[end]))
                && crate::lang::common::possible_token_end(text, end)
            {
                out.push(Candidate {
                    end,
                    cost: cost(100.0)
                        + cost(-0.0001) * (te_segments + i64::from(segment_has_chars && segment_is_te)) as Cost,
                    rank: 13,
                    tag: lazy::tag(|| {
                        let s: String = text[start..end].iter().collect();
                        format!("name: \"{}\"", s.replace(' ', "\u{a0}"))
                    }),
                });
            }
        }
        // WordFst also has a high-cost residual non-space fallback.
        for end in start + 1..=text.len() {
            if is_nemo_space(text[end - 1]) {
                break;
            }
            if end == text.len() || is_nemo_space(text[end]) {
                out.push(Candidate {
                    end,
                    cost: cost(200.0),
                    rank: 13,
                    tag: lazy::tag(|| {
                        let s: String = text[start..end].iter().collect();
                        format!("name: \"{s}\"")
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
            let s: String = text[start..candidate.end].iter().collect();
            candidate.tag = format!("name: \"{}\"", s.replace(' ', "\u{a0}"));
            return;
        }

        let input: String = text[start..candidate.end].iter().collect();
        if candidate.rank == 1 {
            if let Some(tag) = numeric::materialize_cardinal(&input) {
                candidate.tag = tag;
                return;
            }
        }
        let mut matches = Vec::new();
        lazy::with_build(true, || match candidate.rank {
            0 => whitelist::classify(&input, candidate.end, &mut matches),
            1 | 2 | 3 | 4 => numeric::classify(&input, candidate.end, &mut matches),
            5 => date::classify(&input, candidate.end, &mut matches),
            6 => time::classify(&input, candidate.end, &mut matches),
            7 => measure::classify(&input, candidate.end, &mut matches),
            8 => money::classify(&input, candidate.end, &mut matches),
            9 => telephone::classify(&input, candidate.end, &mut matches),
            10 => electronic::classify(&input, candidate.end, &mut matches),
            11 => serial::classify(&input, candidate.end, &mut matches),
            12 => roman::classify(&input, candidate.end, &mut matches),
            14 => composite::classify(&input, candidate.end, &mut matches),
            21..=28 => prefixed_numeric(&input, candidate.end, &mut matches),
            _ => {}
        });

        if let Some(found) =
            matches.into_iter().find(|c| c.end == candidate.end && c.cost == candidate.cost && c.rank == candidate.rank)
        {
            candidate.tag = found.tag;
        }
    }
}

struct Te {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for Te {
    fn code(&self) -> &'static str {
        "te"
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

    fn token_join(&self) -> &'static str {
        " "
    }

    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        // Most Telugu letters occupy three UTF-8 bytes.
        let mut out = String::with_capacity(48 * split.len());
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push_str(self.token_join());
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
                            for c in value.chars() {
                                out.push(if c == '\u{a0}' { ' ' } else { c });
                            }
                            continue;
                        }
                    }
                }
                if class == "time" && verbalize::append_time_hm(&mut out, fields) {
                    continue;
                }
                if class == "measure" && verbalize::append_plain_measure(&mut out, fields) {
                    continue;
                }
                if class == "money" && verbalize::append_plain_money(&mut out, fields) {
                    continue;
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
}

static GRAMMAR: LazyLock<Te> = LazyLock::new(|| {
    let punct = Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], cost(2.1));
    let union_classifier = Union { marks: Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], cost(2.1)) };
    Te { classifiers: vec![Box::new(union_classifier)], punct }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

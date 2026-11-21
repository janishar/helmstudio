//! Czech text normalization, ported from the production NeMo grammar.
mod alternatives;
mod datetime;
mod graph_utils;
mod measure;
mod money;
mod numbers;
mod ordinal;
mod percent;
mod technical;
mod whitelist;

use crate::lang::common::{self, Fields, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::cell::RefCell;
use std::sync::LazyLock;

struct Czech {
    classifiers: Vec<Box<dyn Classifier>>,
}
static PUNCT: LazyLock<Punctuation> = LazyLock::new(|| Punctuation::en(cost(1.1)));
fn boundary(c: char) -> bool {
    if c.is_ascii() {
        matches!(
            c,
            ' ' | '\t'
                | '\n'
                | '\r'
                | '!'
                | '"'
                | '\''
                | '('
                | ')'
                | ','
                | '-'
                | '.'
                | ':'
                | ';'
                | '<'
                | '>'
                | '?'
                | '['
                | '\\'
                | ']'
                | '`'
                | '{'
                | '}'
        )
    } else {
        is_nemo_space(c) || PUNCT.is_mark(c)
    }
}
impl Grammar for Czech {
    fn code(&self) -> &'static str {
        "cs"
    }
    fn shape(&self) -> Shape {
        Shape::En
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&*PUNCT)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(v) = common::verbalize_name(token) {
            return Some(v);
        }
        let mut outer = Fields::new(token);
        let d = outer.dict("date")?;
        if !outer.done() {
            return None;
        }
        let mut f = Fields::new(d);
        let day = f.take("day")?;
        let month = f.take("month")?;
        let year = f.take("year")?;
        if !f.done() || day.is_empty() || month.is_empty() || year.is_empty() {
            return None;
        }
        Some(format!("{day} {month} {year}").replace('\u{a0}', " "))
    }
}
struct Union;
#[derive(Clone, Copy)]
enum SimpleReading {
    Money(money::SimpleReading),
    Measure(measure::SimpleReading),
}
thread_local! {
    static READINGS: RefCell<Vec<(usize, usize, SimpleReading)>> = const { RefCell::new(Vec::new()) };
}

fn record_reading(start: usize, end: usize, reading: SimpleReading) {
    READINGS.with_borrow_mut(|rows| rows.push((start, end, reading)));
}

fn recall_reading(start: usize, end: usize, rank: i32) -> Option<SimpleReading> {
    READINGS.with_borrow(|rows| {
        rows.iter().rev().find_map(|&(a, b, reading)| {
            if a != start || b != end {
                return None;
            }
            match (rank, reading) {
                (6, SimpleReading::Money(_)) | (7, SimpleReading::Measure(_)) => Some(reading),
                _ => None,
            }
        })
    })
}
fn tag(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 8 + s.bytes().filter(|b| *b == b' ').count());
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}
fn offer(out: &mut Vec<Candidate>, end: usize, value: String, weight: f64, rank: i32) {
    if !value.contains("jeden milion") && !value.contains("jedna miliarda") {
        out.push(Candidate { end, cost: cost(weight), rank, tag: tag(&value) });
    } else {
        for (value, extra) in alternatives::scale_paths(&value) {
            out.push(Candidate { end, cost: cost(weight + extra), rank, tag: tag(&value) })
        }
    }
}
/// An alphabetic whitespace-delimited word with no matching lexical head
/// cannot start any semiotic branch, including a multiword contextual one.
fn ordinary_word_end(text: &[char], start: usize) -> Option<usize> {
    let mut bytes = [0u8; 128];
    let mut len = 0;
    let mut end = start;
    while let Some(&c) = text.get(end) {
        if is_nemo_space(c) {
            break;
        }
        if !c.is_alphabetic() || boundary(c) {
            return None;
        }
        let mut encoded = [0u8; 4];
        let encoded = c.encode_utf8(&mut encoded).as_bytes();
        if len + encoded.len() > bytes.len() {
            return None;
        }
        bytes[len..len + encoded.len()].copy_from_slice(encoded);
        len += encoded.len();
        end += 1;
    }
    if end == start {
        return None;
    }
    let word = std::str::from_utf8(&bytes[..len]).ok()?;
    if whitelist::has_word(word)
        || whitelist::may_cross_space(word)
        || ordinal::may_cross_space(word)
        || matches!(word, "XL" | "MIX" | "CD")
        || technical::may_cross_space(word)
        || money::may_cross_space(word)
    {
        return None;
    }
    Some(end)
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            READINGS.with_borrow_mut(Vec::clear);
        }
        if start != 0 && !boundary(text[start - 1]) {
            return;
        }
        if let Some(end) = ordinary_word_end(text, start) {
            out.push(Candidate { end, cost: cost(100.0), rank: 12, tag: String::new() });
        } else {
            self.collect(text, start, out, true);
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if let Some(reading) = recall_reading(start, candidate.end, candidate.rank) {
            let spoken = match reading {
                SimpleReading::Money(reading) => money::simple_render(reading),
                SimpleReading::Measure(reading) => measure::simple_render(reading),
            };
            candidate.tag = tag(&spoken);
            return;
        }
        if candidate.cost == cost(100.0) {
            candidate.tag.reserve(candidate.end - start + 8);
            candidate.tag.push_str("name: \"");
            candidate.tag.extend(&text[start..candidate.end]);
            candidate.tag.push('"');
        } else {
            let digits: String = text[start..candidate.end].iter().collect();
            let spoken = if candidate.rank == 3 {
                datetime::time(&digits).expect("recognized Czech clock")
            } else {
                graph_utils::positive(&digits).expect("recognized Czech cardinal")
            };
            candidate.tag = tag(&spoken);
        }
    }
}
impl Union {
    fn collect(&self, text: &[char], start: usize, out: &mut Vec<Candidate>, allow_prefix: bool) {
        let mut s = String::with_capacity((text.len() - start).min(32));
        let mut has_digit = false;
        let mut has_zero = false;
        let mut has_space = false;
        let mut has_decimal = false;
        let mut has_slash = false;
        let mut has_colon = false;
        let mut has_at = false;
        let mut has_vulgar = false;
        let mut all_digits = true;
        let mut cardinal_alphabet = true;
        let mut digit_count = 0usize;
        let mut digit_run = 0usize;
        let mut run_starts_zero = false;
        let mut has_leading_run = false;
        let mut has_alpha = false;
        let mut has_dash = false;
        let mut has_power = false;
        let mut checked_first_space = false;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if is_nemo_space(c) && !checked_first_space {
                checked_first_space = true;
                if has_colon
                    && (datetime::simple_time(&s) || s == "00:00" || s == "0:00")
                    && (c != ' ' || !datetime::clock_continuation(&text[end..]))
                {
                    break;
                }
                if text[start].is_alphabetic()
                    && !whitelist::may_cross_space(&s)
                    && !ordinal::may_cross_space(&s)
                    && !technical::may_cross_space(&s)
                    && !money::may_cross_space(&s)
                {
                    break;
                }
            }
            let first = out.len();
            s.push(c);
            has_digit |= c.is_ascii_digit();
            all_digits &= c.is_ascii_digit();
            cardinal_alphabet &=
                c.is_ascii_digit() || matches!(c, ' ' | '.' | '\u{a0}' | '\u{202f}' | '-' | '−' | '–' | '—');
            if c.is_ascii_digit() {
                digit_count += 1;
                if digit_run == 0 {
                    run_starts_zero = c == '0';
                }
                digit_run += 1;
                has_leading_run |= run_starts_zero && digit_run >= 3;
            } else {
                digit_run = 0;
            }
            has_alpha |= c.is_ascii_alphabetic();
            has_dash |= matches!(c, '-' | '–' | '—');
            has_power |= c == '^';
            has_zero |= c == '0';
            has_space |= is_nemo_space(c);
            has_decimal |= matches!(c, '.' | ',');
            has_slash |= c == '/';
            has_colon |= c == ':';
            has_at |= c == '@';
            has_vulgar |= matches!(
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
            );
            if end < text.len() && !boundary(text[end]) {
                continue;
            }
            // On these spans the cardinal graph is the only semiotic branch.
            // Defer spelling and word-tag construction until a path wins.
            if allow_prefix && all_digits && s.len() <= 6 && (s.len() == 1 || !s.starts_with('0')) {
                out.push(Candidate { end, cost: cost(2.10), rank: 11, tag: String::new() });
                out.push(Candidate { end, cost: cost(100.0), rank: 12, tag: String::new() });
                continue;
            }
            if allow_prefix && has_colon && !has_space && datetime::simple_time(&s) {
                out.push(Candidate { end, cost: cost(2.03), rank: 3, tag: String::new() });
                out.push(Candidate { end, cost: cost(100.0), rank: 12, tag: String::new() });
                continue;
            }
            if !s.as_bytes()[0].is_ascii_digit() {
                if let Some(v) = whitelist::whitelist(&s) {
                    offer(out, end, v, 2.01, 0)
                }
            }
            if !has_space && (has_decimal || has_colon || has_at) {
                for (v, w) in technical::electronic(&s) {
                    offer(out, end, v, 2.02 + w, 1)
                }
            }
            if digit_count == 9 || (s.starts_with('+') && (7..=15).contains(&digit_count)) {
                if let Some(v) = technical::telephone(&s) {
                    offer(out, end, v, 2.025, 2)
                }
            }
            if has_colon {
                if let Some(v) = datetime::time(&s) {
                    for (i, v) in alternatives::midnight_paths(&v).into_iter().enumerate() {
                        offer(out, end, v, 2.03, 3 + i as i32)
                    }
                }
            }
            if has_digit && (has_decimal || has_slash || has_dash || datetime::possible_era_end(c)) {
                if let Some(v) = datetime::date(&s) {
                    offer(out, end, v, 2.04, 4)
                }
                if has_decimal || has_dash {
                    if let Some((year, month, day)) = datetime::iso(&s) {
                        for (year, w) in alternatives::scale_paths(&year) {
                            out.push(Candidate {
                                end,
                                cost: cost(2.04 + w),
                                rank: 5,
                                tag: format!("date {{ year: \"{year}\" month: \"{month}\" day: \"{day}\" }}"),
                            })
                        }
                    }
                }
            }
            let mut completed_measure = false;
            if has_digit {
                if let Some(reading) = if allow_prefix { money::simple_reading(&s) } else { None } {
                    record_reading(start, end, SimpleReading::Money(reading));
                    out.push(Candidate { end, cost: cost(2.05), rank: 6, tag: String::new() });
                } else if money::may_match(&s) {
                    for (v, w) in money::paths(&s) {
                        offer(out, end, v, 2.05 + w, 6)
                    }
                }
                if let Some(reading) = if allow_prefix { measure::simple_reading(&s) } else { None } {
                    completed_measure = true;
                    let weight = measure::simple_weight(reading);
                    record_reading(start, end, SimpleReading::Measure(reading));
                    out.push(Candidate { end, cost: cost(2.06 + weight), rank: 7, tag: String::new() });
                } else if measure::may_match(&s) {
                    for (v, w) in measure::measure(&s) {
                        completed_measure = true;
                        offer(out, end, v, 2.06 + w, 7)
                    }
                }
            }
            if (has_digit || has_vulgar)
                && (has_decimal || has_slash || has_vulgar || s.ends_with('%') || s.ends_with('‰'))
            {
                if s.ends_with('%') || s.ends_with('‰') {
                    for (v, w) in percent::paths(&s) {
                        offer(out, end, v, 2.07 + w, 8)
                    }
                } else if let Some((v, w)) = numbers::decimal_full(&s) {
                    offer(out, end, v, 2.07 + w, 8)
                }
                if has_decimal {
                    for (v, w) in alternatives::decimal_paths(&s) {
                        offer(out, end, v, 2.07 + w, 9)
                    }
                }
                if has_digit && (has_slash || has_vulgar) {
                    for v in numbers::mixed_fraction_paths(&s) {
                        offer(out, end, v, 2.07, 9)
                    }
                }
            }
            if !has_digit || has_space || s.ends_with('.') {
                if let Some(v) = ordinal::ordinal_full(&s) {
                    offer(out, end, v, 2.08, 9)
                }
            }
            if has_digit {
                if (has_alpha || has_decimal || has_dash || has_power)
                    && (!has_space || !s.as_bytes()[0].is_ascii_digit() || has_power)
                {
                    if let Some(v) = technical::serial(&s) {
                        offer(out, end, v, 2.09, 10)
                    }
                }
                if cardinal_alphabet || s.ends_with('s') || s.ends_with("léta") {
                    if let Some(v) = numbers::cardinal_full(&s) {
                        offer(out, end, v, 2.10, 11)
                    }
                }
                if has_zero {
                    if let Some(v) = alternatives::leading_zero_path(&s) {
                        offer(out, end, v, 2.10, 12)
                    }
                }
            }
            if has_leading_run || (has_digit && has_decimal) {
                let last = out.len();
                for index in first..last {
                    let candidate = out[index].clone();
                    let zero_paths = if has_leading_run {
                        alternatives::nested_leading_zero_paths(&s, &candidate.tag)
                    } else {
                        vec![candidate.tag.clone()]
                    };
                    for zero_tag in zero_paths {
                        if has_decimal {
                            for tag in alternatives::nested_decimal_paths(&s, &zero_tag) {
                                if tag != candidate.tag {
                                    let mut alt = candidate.clone();
                                    alt.rank += 1;
                                    alt.tag = tag;
                                    out.push(alt)
                                }
                            }
                        } else if zero_tag != candidate.tag {
                            let mut alt = candidate.clone();
                            alt.rank += 1;
                            alt.tag = zero_tag;
                            out.push(alt)
                        }
                    }
                }
            }
            if !has_space {
                let tag = if allow_prefix {
                    String::new()
                } else {
                    let raw: String = text[start..end].iter().collect();
                    format!("name: \"{raw}\"")
                };
                out.push(Candidate { end, cost: cost(100.0), rank: 12, tag });
            }
            // A complete measure cannot be extended across whitespace by any
            // Czech semiotic branch; following text starts a separate item.
            if completed_measure && text.get(end).is_some_and(|c| is_nemo_space(*c)) {
                break;
            }
        }
        // Prefix modifiers are separate tokens in the source graph.
        if !allow_prefix {
            return;
        }
        let prefix = match text.get(start..) {
            Some(['~', ..]) => Some((1, "přibližně")),
            Some(['±', ..]) => Some((1, "plus\u{a0}mínus")),
            Some(['+', '/', '-', ..]) => Some((3, "plus\u{a0}mínus")),
            _ => None,
        };
        if let Some((n, spoken)) = prefix {
            let mut next = start + n;
            if text.get(next) == Some(&' ') {
                next += 1;
            }
            if next < text.len() {
                let mut inner = Vec::new();
                self.collect(&text[next..], 0, &mut inner, false);
                for mut c in inner {
                    c.end += next;
                    c.cost += cost(1.05);
                    c.tag = format!("name: \"{spoken}\" }} tokens {{ {}", c.tag);
                    out.push(c);
                }
            }
        }
    }
}
static GRAMMAR: LazyLock<Czech> = LazyLock::new(|| Czech { classifiers: vec![Box::new(Union)] });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

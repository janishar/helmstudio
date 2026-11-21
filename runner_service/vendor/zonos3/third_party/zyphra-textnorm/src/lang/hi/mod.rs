//! Hindi text-normalization grammar.
mod date;
mod electronic;
mod lazy;
mod measure;
mod money;
mod numbers;
mod numeric;
mod post_processing;
mod roman;
mod serial;
mod telephone;
mod time;
mod trie;
mod verbalize;
mod whitelist;
mod word;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::cell::Cell;
use std::sync::LazyLock;

thread_local! {
    static SPAN: Cell<String> = const { Cell::new(String::new()) };
    // Per-input lexical lookahead, reset at lattice position zero.
    static LOOKAHEAD: Cell<(Option<usize>, Option<usize>, Option<usize>)> = const { Cell::new((None, None, None)) };
}

const WORD_EXCLUDED: &str = "$€₩£¥₹₨₦৳֏₽₫₺₪₴₱฿¢#%‰°℃℉ΩΩ²³±©§®™½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒";
const WHITE: u16 = 1 << 0;
const NUMERIC: u16 = 1 << 1;
const DATE: u16 = 1 << 2;
const TIME: u16 = 1 << 3;
const MEASURE: u16 = 1 << 4;
const MONEY: u16 = 1 << 5;
const PHONE: u16 = 1 << 6;
const ELECTRONIC: u16 = 1 << 7;
const SERIAL: u16 = 1 << 8;
const ROMAN: u16 = 1 << 9;
const ALL: u16 = (1 << 10) - 1;

struct HindiPunctuation(Punctuation);
impl Classifier for HindiPunctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let begin = out.len();
        self.0.spans(text, start, out);
        out[begin..].sort_by_key(|c| c.end);
        let mut i = begin;
        while i < out.len() {
            let span = &text[start..out[i].end];
            let emphasis = span.first() == Some(&'<') && span.last() == Some(&'>') && span.len() > 2;
            if !emphasis && span.len() > 1 && span.contains(&'%') {
                out.remove(i);
            } else {
                i += 1;
            }
        }
    }
}
struct Union {
    marks: Punctuation,
}
impl Union {
    fn classify_span(
        s: &str,
        end: usize,
        out: &mut Vec<Candidate>,
        live: u16,
        only_digits: bool,
        serial_trigger: bool,
    ) {
        let Some(first) = s.chars().next() else { return };
        if live & WHITE != 0 && (whitelist::possible_start(s) || first.is_ascii_uppercase()) {
            whitelist::classify(s, end, out);
        }
        if live & NUMERIC != 0 && numeric::structural_char(first) && numeric::possible_end(s) {
            numeric::classify(s, end, out);
        }
        if live & DATE != 0
            && !only_digits
            && (numbers::is_digit(first) || first == '\'' || date::possible_start(s))
            && date::possible_end(s)
        {
            date::classify(s, end, out);
        }
        if live & TIME != 0 && !only_digits && numbers::is_digit(first) {
            time::classify(s, end, out);
        }
        if live & MEASURE != 0 && measure::might_match(s) {
            measure::classify(s, end, out);
        }
        if live & MONEY != 0 && !only_digits && money::possible_start(s) {
            money::classify(s, end, out);
        }
        if live & PHONE != 0 && telephone::possible_start(s) {
            telephone::classify(s, end, out);
        }
        if live & ELECTRONIC != 0
            && !only_digits
            && (first.is_ascii_alphanumeric()
                || numbers::is_digit(first)
                || matches!(first, '/' | '\\' | '-' | '+' | '(' | ')' | '–' | '₀'..='₉'))
        {
            electronic::classify(s, end, out);
        }
        if live & SERIAL != 0 && !only_digits && serial_trigger {
            serial::classify(s, end, out);
        }
        if live & ROMAN != 0 {
            roman::classify(s, end, out);
        }
    }
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() || (start > 0 && !is_nemo_space(text[start - 1]) && !self.marks.is_mark(text[start - 1]))
        {
            return;
        }
        SPAN.with(|scratch| {
            let mut span = scratch.take();
            span.clear();
            if start == 0 {
                span.extend(text.iter().copied());
                let address = measure::last_address_start(&span);
                let phone = telephone::last_context_start(&span);
                let roman = text.iter().rposition(|&c| matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M'));
                LOOKAHEAD.with(|v| v.set((address, phone, roman)));
                span.clear();
            }
            let (address, phone, roman) = LOOKAHEAD.with(Cell::get);
            let first = text[start];
            let numeric_start = numbers::is_digit(first);
            let ordinary_devanagari = ('\u{0900}'..='\u{097f}').contains(&first) && !numeric_start;
            let address_future = address.is_some_and(|pos| pos >= start);
            let phone_context = phone.is_some_and(|pos| pos >= start);
            let can_stop_at_space = ordinary_devanagari && !address_future && !roman.is_some_and(|pos| pos >= start);
            let mut live = if numeric_start { ALL & !ROMAN } else { ALL };
            let mut only_digits = true;
            let mut serial_trigger = false;
            let mut measure_number = numeric_start;
            let mut word_live = !self.marks.is_mark(first);
            let mut word_count = 0usize;
            let mut mark_run = 0usize;
            let mut percent = false;
            lazy::with_build(false, || {
                for end in start + 1..=text.len() {
                    let c = text[end - 1];
                    only_digits &= numbers::is_digit(c);
                    serial_trigger |= numbers::is_digit(c) || matches!(c, '-' | '/' | '^');
                    if is_nemo_space(c)
                        && can_stop_at_space
                        && !date::possible_start(&span)
                        && !telephone::context_start(&span)
                        && !whitelist::continues_after_space(&span, c)
                    {
                        break;
                    }
                    span.push(c);
                    if numeric_start {
                        if !numbers::is_digit(c) && c != ':' {
                            live &= !TIME;
                        }
                        if is_nemo_space(c) {
                            live &= !(ELECTRONIC | SERIAL);
                        }
                        if !phone_context
                            && !numbers::is_digit(c)
                            && !is_nemo_space(c)
                            && !matches!(c, '+' | '(' | ')' | '-' | '.')
                        {
                            live &= !PHONE;
                        }
                    }
                    let mark = self.marks.is_mark(c);
                    if word_live {
                        if is_nemo_space(c) || (WORD_EXCLUDED.contains(c) && !mark) {
                            word_live = false;
                        } else if mark {
                            mark_run += 1;
                            percent |= c == '%';
                            if percent && mark_run > 1 {
                                word_live = false;
                            }
                        } else {
                            mark_run = 0;
                            percent = false;
                        }
                        if word::is_hindi_char(c) {
                            word_count += 1;
                        }
                    }
                    if end != text.len() && !is_nemo_space(text[end]) && !self.marks.is_mark(text[end]) {
                        continue;
                    }
                    if numeric_start {
                        if live & WHITE != 0 && !whitelist::possible_prefix(&span) {
                            live &= !WHITE;
                        }
                        if live & NUMERIC != 0 && !numeric::numeric_prefix(&span) {
                            live &= !NUMERIC;
                        }
                        if live & DATE != 0 && !date::numeric_prefix(&span) {
                            live &= !DATE;
                        }
                        if measure_number && !measure::numeric_prefix(&span) {
                            measure_number = false;
                        }
                        if !measure_number && !address_future {
                            live &= !MEASURE;
                        }
                        if live & MONEY != 0 && !money::numeric_prefix(&span) {
                            live &= !MONEY;
                        }
                    }
                    if live == 0 && !word_live {
                        break;
                    }
                    Self::classify_span(&span, end, out, live, only_digits, serial_trigger);
                    if word_live && !mark {
                        word::classify_count(&span, end, word_count, out);
                    }
                }
            });
            span.clear();
            scratch.set(span);
        });
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 13 {
            let mut tag =
                String::with_capacity(8 + text[start..candidate.end].iter().map(|c| c.len_utf8()).sum::<usize>());
            tag.push_str("name: \"");
            tag.extend(text[start..candidate.end].iter().copied());
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        SPAN.with(|scratch| {
            let mut s = scratch.take();
            s.clear();
            s.extend(text[start..candidate.end].iter().copied());
            if candidate.rank == 1 {
                lazy::with_build(true, || numeric::materialize_cardinal(&s, candidate));
            } else if candidate.rank == 5 {
                time::materialize(&s, candidate);
            } else {
                let mut matches = Vec::new();
                lazy::with_build(true, || match candidate.rank {
                    0 => whitelist::classify(&s, candidate.end, &mut matches),
                    2 | 3 | 9 => numeric::classify(&s, candidate.end, &mut matches),
                    4 => date::classify(&s, candidate.end, &mut matches),
                    6 => measure::classify(&s, candidate.end, &mut matches),
                    7 => money::classify(&s, candidate.end, &mut matches),
                    8 => telephone::classify(&s, candidate.end, &mut matches),
                    10 => electronic::classify(&s, candidate.end, &mut matches),
                    11 => serial::classify(&s, candidate.end, &mut matches),
                    12 => roman::classify(&s, candidate.end, &mut matches),
                    _ => (),
                });
                if let Some(found) = matches.into_iter().find(|c| c.cost == candidate.cost && c.rank == candidate.rank)
                {
                    candidate.tag = found.tag;
                }
            }
            s.clear();
            scratch.set(s);
        });
    }
}
struct Hindi {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: HindiPunctuation,
}
impl Grammar for Hindi {
    fn code(&self) -> &'static str {
        "hi"
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
    fn post_process(&self, text: String) -> String {
        post_processing::apply(&text)
    }
}
static GRAMMAR: LazyLock<Hindi> = LazyLock::new(|| {
    let marks = Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], cost(2.1));
    let punct = Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], cost(2.1));
    Hindi { classifiers: vec![Box::new(Union { marks })], punct: HindiPunctuation(punct) }
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

//! Danish text normalization, based on the Zyphra Danish NeMo grammar.
mod datetime;
mod graph_utils;
mod lexical;
mod numeric;
mod quantities;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;
struct Danish {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Danish {
    fn code(&self) -> &'static str {
        "da"
    }
    fn shape(&self) -> Shape {
        Shape::SeparatedWithPunct
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        common::verbalize_name(token)
    }
}
static GRAMMAR: LazyLock<Danish> =
    LazyLock::new(|| Danish { classifiers: vec![Box::new(Union)], punct: Punctuation::en(cost(1.1)) });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

struct Union;
fn spoken_tag(value: &str) -> String {
    let mut tag = String::with_capacity(value.len() + value.bytes().filter(|b| *b == b' ').count() + 8);
    tag.push_str("name: \"");
    for c in value.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}
fn readings(s: &str) -> Vec<(String, f64, i32)> {
    let mut r = Vec::new();
    if !s.bytes().all(|b| b.is_ascii_digit()) {
        for x in lexical::whitelist(s) {
            let w = if x.contains(" til ") { 1.11 } else { 1.01 };
            r.push((x, w, 0));
        }
    }
    let has_digit = s.bytes().any(|b| b.is_ascii_digit());
    if s.bytes().any(|b| matches!(b, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')) {
        for x in lexical::roman(s) {
            r.push((x, 1.04, 1));
        }
    }
    if has_digit {
        if s.contains(':') || s.contains(['-', '–', '—']) || s.starts_with("kl") || s.starts_with("Kl") {
            for x in datetime::time(s) {
                r.push((x, 1.05, 2));
            }
        }
        if s.chars().any(|c| {
            c.is_alphabetic() || is_nemo_space(c) || matches!(c, '.' | '/' | '-' | '–' | '—' | '\'' | '’' | '‘')
        }) {
            for (x, c) in datetime::date(s) {
                r.push((x, 1.08 + c, 3));
            }
        }
        if !s.as_bytes().last().is_some_and(u8::is_ascii_digit) {
            if let Some((x, c)) = quantities::measure(s) {
                r.push((x, 1.05 + c, 4));
            }
        }
        if s.chars().any(|c| c.is_alphabetic() || "$€£¥₹₽₩₺₪₴₫৳₦₱฿֏".contains(c)) {
            if let Some((x, c)) = quantities::money(s) {
                r.push((x, 1.05 + c, 5));
            }
        }
    }
    if s.contains('/')
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
                    | '⅐'
                    | '⅑'
                    | '⅒'
            )
        })
    {
        if let Some(x) = quantities::fraction(s) {
            r.push((x, 1.05, 6));
        }
    }
    if has_digit {
        if s.contains(',') || s.contains('.') || s.chars().any(char::is_alphabetic) {
            if let Some((x, c)) = numeric::decimal_full(s) {
                r.push((x, 1.10 + c, 7));
            }
            for (x, c) in numeric::decimal_alternatives(s) {
                r.push((x, 1.10 + c, 7));
            }
        }
        if s.chars().last().is_some_and(|c| c == '.' || c.is_alphabetic()) {
            if let Some(x) = numeric::ordinal_tag(s) {
                r.push((x, 1.10, 8));
            }
            for (x, c) in numeric::ordinal_alternatives(s) {
                r.push((x, 1.10 + c, 8));
            }
        }
        if s.starts_with('+') || s.starts_with('(') || s.starts_with("0045") || s.contains(' ') || s.contains('-') {
            for x in lexical::telephone(s) {
                r.push((x, 1.10, 9));
            }
        }
        for (x, c) in numeric::cardinal(s) {
            r.push((x, 1.10 + c, 10));
        }
    }
    if s.contains('.') || s.contains('@') || s.contains("://") {
        if let Some((x, c)) = lexical::electronic(s) {
            r.push((x, 1.11 + c, 11));
        }
    }
    if !s.is_empty() && !s.chars().any(is_nemo_space) {
        r.push((s.to_owned(), 100.0, 12));
    }
    r
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !common::possible_token_start(text, start) {
            return;
        }
        // No Danish semantic graph starts with these marks. They can still be
        // consumed by the unrestricted word graph, including embedded marks.
        if matches!(text[start], ':' | ',' | ';' | '!' | '?' | ')' | ']' | '}' | '"') {
            for end in start + 1..=text.len() {
                if is_nemo_space(text[end - 1]) {
                    break;
                }
                if common::possible_token_end(text, end) {
                    out.push(Candidate { end, cost: cost(100.0), rank: 12000, tag: String::new() });
                }
            }
            return;
        }
        let mut span = String::new();
        let mut may_span_spaces = !text[start].is_alphabetic();
        let mut checked_first_space = may_span_spaces;
        let mut saw_space = false;
        let mut piece_start = 0usize;
        let mut all_digits = true;
        let mut all_alphabetic = true;
        let mut has_roman_letter = false;
        for end in start + 1..=text.len() {
            if is_nemo_space(text[end - 1]) {
                if checked_first_space {
                    let piece = &span[piece_start..];
                    if !lexical::internal_word(piece) {
                        break;
                    }
                }
                if !checked_first_space {
                    may_span_spaces = lexical::multiword_start(&span) || quantities::currency_prefix(&span);
                    checked_first_space = true;
                }
                if !may_span_spaces {
                    break;
                }
                saw_space = true;
                piece_start = span.len() + text[end - 1].len_utf8();
            }
            let c = text[end - 1];
            span.push(c);
            all_digits &= c.is_ascii_digit();
            all_alphabetic &= c.is_alphabetic();
            has_roman_letter |= matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'M');
            if saw_space && !is_nemo_space(c) {
                let piece = &span[piece_start..];
                if !lexical::internal_prefix(piece) {
                    break;
                }
            }
            if !common::possible_token_end(text, end) {
                continue;
            }
            if !saw_space {
                if let Some((count, ratio)) = datetime::simple_clock(&span) {
                    for i in 0..count {
                        out.push(Candidate { end, cost: cost(1.05), rank: 2000 + i as i32, tag: String::new() });
                    }
                    let mut index = count as i32;
                    if ratio {
                        out.push(Candidate { end, cost: cost(1.15), rank: 10000 + index, tag: String::new() });
                        index += 1;
                    }
                    out.push(Candidate { end, cost: cost(100.0), rank: 12000 + index, tag: String::new() });
                    continue;
                }
                if let Some(count) = numeric::simple_decimal_count(&span) {
                    for i in 0..count {
                        out.push(Candidate { end, cost: cost(1.10), rank: 7000 + i as i32, tag: String::new() });
                    }
                    out.push(Candidate { end, cost: cost(100.0), rank: 12000 + count as i32, tag: String::new() });
                    continue;
                }
            }
            if saw_space {
                if let Some(count) = datetime::simple_prefixed_clock(&span) {
                    for i in 0..count {
                        out.push(Candidate { end, cost: cost(1.05), rank: 2000 + i as i32, tag: String::new() });
                    }
                    continue;
                }
            }
            if saw_space {
                if let Some((weight, branch)) = quantities::simple_space_value(&span) {
                    out.push(Candidate { end, cost: cost(weight), rank: branch * 1000, tag: String::new() });
                    continue;
                }
            }
            // The plain digit language is independent of lexical tables. Its
            // branches are the integer graph and the two digit-sequence graphs.
            // Country-code telephones are left to the general recognizer.
            if all_digits && !span.starts_with("0045") {
                let len = span.len();
                let integer = len <= 15 && (len == 1 || !span.starts_with('0'));
                let mut i = 0;
                if integer {
                    out.push(Candidate { end, cost: cost(1.1), rank: 10000, tag: String::new() });
                    i += 1;
                }
                if len >= 2 {
                    out.push(Candidate { end, cost: cost(2.6), rank: 10000 + i, tag: String::new() });
                    i += 1;
                    let zeros = span.bytes().take_while(|b| *b == b'0').count();
                    if zeros > 0 && zeros < len && len - zeros <= 15 {
                        out.push(Candidate { end, cost: cost(2.6), rank: 10000 + i, tag: String::new() });
                        i += 1;
                    }
                }
                out.push(Candidate { end, cost: cost(100.0), rank: 12000 + i, tag: String::new() });
                continue;
            }
            // Alphabetic words with no Roman letters cannot enter any productive
            // numeric, electronic, or Roman graph. Recognize these without
            // constructing either a spoken string or an intermediate readings list.
            if all_alphabetic && !has_roman_letter {
                let whitelisted = lexical::plain_whitelist(&span);
                if whitelisted {
                    out.push(Candidate { end, cost: cost(1.01), rank: 0, tag: String::new() });
                }
                out.push(Candidate {
                    end,
                    cost: cost(100.0),
                    rank: 12000 + i32::from(whitelisted),
                    tag: String::new(),
                });
                continue;
            }
            for (i, (_, weight, branch)) in readings(&span).into_iter().enumerate() {
                out.push(Candidate { end, cost: cost(weight), rank: branch * 1000 + i as i32, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.cost == cost(100.0) {
            let mut tag = String::with_capacity((candidate.end - start) * 4 + 8);
            tag.push_str("name: \"");
            tag.extend(&text[start..candidate.end]);
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let span: String = text[start..candidate.end].iter().collect();
        if candidate.rank == 0 && candidate.cost == cost(1.01) {
            if let Some(value) = lexical::whitelist_value(&span) {
                candidate.tag = spoken_tag(value);
                return;
            }
        }
        if candidate.rank >= 2000
            && candidate.rank < 3000
            && (datetime::simple_clock(&span).is_some() || datetime::simple_prefixed_clock(&span).is_some())
        {
            if let Some(value) = datetime::simple_clock_reading(&span, (candidate.rank - 2000) as usize) {
                candidate.tag = spoken_tag(&value);
                return;
            }
        }
        if candidate.rank == 4000 || candidate.rank == 5000 {
            if let Some(value) = quantities::simple_space_reading(&span, candidate.rank / 1000) {
                candidate.tag = spoken_tag(&value);
                return;
            }
        }
        if (7000..7003).contains(&candidate.rank) && candidate.cost == cost(1.1) {
            if let Some(value) = numeric::simple_decimal_reading(&span, (candidate.rank - 7000) as usize) {
                candidate.tag = spoken_tag(&value);
                return;
            }
        }
        if candidate.rank >= 10000
            && candidate.rank < 11000
            && span.bytes().all(|b| b.is_ascii_digit())
            && !span.starts_with("0045")
        {
            if candidate.cost == cost(1.1) {
                if let Some(value) = numeric::number(&span, false) {
                    candidate.tag = spoken_tag(&value);
                    return;
                }
            }
            if let Some((value, _)) = numeric::cardinal(&span).into_iter().nth((candidate.rank - 10000) as usize) {
                candidate.tag = spoken_tag(&value);
                return;
            }
        }
        for (i, (value, weight, branch)) in readings(&span).into_iter().enumerate() {
            if candidate.rank == branch * 1000 + i as i32 && candidate.cost == cost(weight) {
                candidate.tag = spoken_tag(&value);
                return;
            }
        }
    }
}

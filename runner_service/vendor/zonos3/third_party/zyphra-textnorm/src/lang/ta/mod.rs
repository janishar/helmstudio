//! Tamil text-normalization grammar.
mod date_time;
mod fraction;
mod identifiers;
mod number;
mod ordinal;
mod quantity;
mod verbalize;

use crate::lang::common::{Punctuation, possible_token_end, possible_token_start};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

/// Literal Tamil-leading alternatives are sparse; index them once rather than
/// trying every grammar on every prefix of an ordinary Tamil word.
static TAMIL_STARTS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for table in [
        include_str!("../../../data/ta/whitelist/abbreviations.tsv"),
        include_str!("../../../data/ta/date/month_names.tsv"),
        include_str!("../../../data/ta/telephone/credit_context.tsv"),
        include_str!("../../../data/ta/telephone/pincode_context.tsv"),
        include_str!("../../../data/ta/money/currency.tsv"),
        include_str!("../../../data/ta/money/currency_singular.tsv"),
    ] {
        for line in table.lines() {
            let key = line.split('\t').next().unwrap_or("");
            if let Some(first) = key.chars().next() {
                index.entry(first).or_default().push(key);
            }
        }
    }
    for key in identifiers::ROMAN_KEYS {
        if let Some(first) = key.chars().next() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});
static TAMIL_CONTINUATIONS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = (*TAMIL_STARTS).clone();
    for table in [
        include_str!("../../../data/ta/date/year_suffix.tsv"),
        include_str!("../../../data/ta/numbers/quantities.tsv"),
        include_str!("../../../data/ta/measure/unit_one.tsv"),
        include_str!("../../../data/ta/measure/unit_other.tsv"),
        include_str!("../../../data/ta/measure/unit_prefixed_one.tsv"),
        include_str!("../../../data/ta/measure/unit_prefixed_other.tsv"),
    ] {
        for line in table.lines() {
            let key = line.split('\t').next().unwrap_or("");
            if let Some(first) = key.chars().next() {
                index.entry(first).or_default().push(key);
            }
        }
    }
    index
});

fn tamil_continuation_matches(text: &[char], start: usize) -> bool {
    TAMIL_CONTINUATIONS.get(&text[start]).is_some_and(|keys| {
        keys.iter().any(|key| {
            let mut chars = text[start..].iter().copied();
            key.chars().all(|c| chars.next() == Some(c))
        })
    })
}

fn tamil_key_matches(text: &[char], start: usize) -> bool {
    TAMIL_STARTS.get(&text[start]).is_some_and(|keys| {
        keys.iter().any(|key| {
            let mut chars = text[start..].iter().copied();
            key.chars().all(|c| chars.next() == Some(c))
        })
    })
}

fn tamil_start_possible(text: &[char], start: usize) -> bool {
    let first = text[start];
    if !('\u{b80}'..='\u{bff}').contains(&first) || number::digit(first).is_some() {
        return true;
    }
    if text[start..]
        .iter()
        .take_while(|&&c| !is_nemo_space(c))
        .any(|&c| number::digit(c).is_some() || matches!(c, '-' | '/' | '^' | '#' | '%' | '&' | '@'))
    {
        return true;
    }
    tamil_key_matches(text, start)
}

static CURRENCY_EDGES: LazyLock<(HashSet<char>, HashSet<char>)> = LazyLock::new(|| {
    let mut starts = HashSet::new();
    let mut ends = HashSet::new();
    for table in [
        include_str!("../../../data/ta/money/currency.tsv"),
        include_str!("../../../data/ta/money/currency_singular.tsv"),
    ] {
        for (key, _) in table.lines().filter_map(|line| line.split_once('\t')) {
            if let Some(c) = key.chars().next() {
                starts.insert(c);
            }
            if let Some(c) = key.chars().last() {
                ends.insert(c);
            }
        }
    }
    (starts, ends)
});
static QUANTITY_ENDS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    include_str!("../../../data/ta/numbers/quantities.tsv")
        .lines()
        .filter_map(|line| line.split_once('\t').and_then(|(key, _)| key.chars().last()))
        .collect()
});
static WHITELIST: LazyLock<HashMap<&'static str, Vec<&'static str>>> = LazyLock::new(|| {
    let mut rows: HashMap<&'static str, Vec<&'static str>> = HashMap::new();
    for (key, value) in
        include_str!("../../../data/ta/whitelist/abbreviations.tsv").lines().filter_map(|line| line.split_once('\t'))
    {
        rows.entry(key).or_default().push(value);
    }
    rows
});
static NUMERIC_WHITELIST_START: LazyLock<bool> =
    LazyLock::new(|| WHITELIST.keys().any(|key| key.chars().next().and_then(number::digit).is_some()));

// Both fast tables are derived from the actual punctuation classifier, so
// uncommon Tamil signs retain exactly the same classification as before.
static ASCII_MARKS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut marks = [false; 128];
    for (i, mark) in marks.iter_mut().enumerate() {
        *mark = GRAMMAR.punct.0.is_mark(i as u8 as char);
    }
    marks
});
static TAMIL_MARKS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut marks = [false; 128];
    for (i, mark) in marks.iter_mut().enumerate() {
        *mark = GRAMMAR.punct.0.is_mark(char::from_u32(0x0b80 + i as u32).unwrap());
    }
    marks
});

#[inline]
fn punct_mark(c: char) -> bool {
    if c.is_ascii() {
        ASCII_MARKS[c as usize]
    } else if ('\u{b80}'..='\u{bff}').contains(&c) {
        TAMIL_MARKS[(c as usize) - 0x0b80]
    } else {
        GRAMMAR.punct.0.is_mark(c)
    }
}

#[inline]
fn boundary(c: char) -> bool {
    is_nemo_space(c) || c == '±' || punct_mark(c)
}

struct Tamil {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: TamilPunct,
}
impl Grammar for Tamil {
    fn code(&self) -> &'static str {
        "ta"
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
    fn verbalize(&self, t: &Dict) -> Option<String> {
        verbalize::verbalize(t)
    }
}
static GRAMMAR: LazyLock<Tamil> = LazyLock::new(|| Tamil {
    classifiers: vec![Box::new(Union), Box::new(Word)],
    punct: TamilPunct(Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, std::iter::empty(), cost(2.1))),
});
struct TamilPunct(Punctuation);
impl Classifier for TamilPunct {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let from = out.len();
        self.0.candidates(text, start, out);
        let mut i = from;
        while i < out.len() {
            let end = out[i].end;
            let span = &text[start..end];
            let emphasis = span.first() == Some(&'<') && span.last() == Some(&'>');
            if !emphasis && span.len() > 1 && span.iter().any(|c| "%‰~&§¶".contains(*c)) {
                out.remove(i);
            } else {
                i += 1
            }
        }
    }
}
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}
fn field(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn push(out: &mut Vec<Candidate>, end: usize, weight: f64, rank: i32, tag: String) {
    out.push(Candidate { end, cost: cost(weight), rank, tag })
}
struct Union;
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if (start > 0 && !boundary(text[start - 1]))
            || !possible_token_start(text, start)
            || !tamil_start_possible(text, start)
        {
            return;
        }
        if matches!(text.get(start), Some('~' | '±')) {
            let sign = text[start];
            let mut pos = start + 1;
            if text.get(pos) == Some(&' ') {
                pos += 1
            }
            if pos < text.len() {
                let mut inner = Vec::new();
                self.candidates(text, pos, &mut inner);
                for mut c in inner {
                    if (2..=6).contains(&c.rank) {
                        let spoken = if sign == '~' {
                            "தோராயமாக"
                        } else {
                            "பிளஸ்\u{a0}அல்லது\u{a0}மைனஸ்"
                        };
                        c.cost -= cost(2.0);
                        if !c.tag.is_empty() {
                            c.tag = format!("{} }} tokens {{ {}", field("name", spoken), c.tag);
                        }
                        out.push(c);
                    }
                }
            }
        }
        let mut s = String::with_capacity((text.len() - start).min(32) * 3);
        let mut spaces = 0;
        let mut digits = 0usize;
        let mut fraction = false;
        let mut electronic_mark = false;
        let mut serial_mark = false;
        let first = text[start];
        let first_digit = number::digit(first).is_some();
        let currency_first = if first == '-' { text.get(start + 1).copied() } else { Some(first) };
        let currency_start = currency_first.is_some_and(|ch| CURRENCY_EDGES.0.contains(&ch));
        let numeric_start = first_digit || first == '-' || "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅐⅛⅜⅝⅞⅑⅒".contains(first);
        let mut date_prefix_valid = true;
        let mut context_prefix_valid = true;
        let mut clock_alphabet = true;
        let mut context_tail_valid = true;
        let mut first_chunk_digits = 0usize;
        let mut electronic_alphabet = true;
        let mut latin_or_tamil = false;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            let is_digit = number::digit(c).is_some();
            let is_tamil = ('\u{b80}'..='\u{bff}').contains(&c);
            s.push(c);
            spaces += usize::from(is_nemo_space(c));
            digits += usize::from(is_digit);
            fraction |= matches!(
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
            electronic_mark |= matches!(c, '@' | '.' | '/' | '\\') || ('₀'..='₉').contains(&c);
            serial_mark |= matches!(c, '-' | '/' | '^' | '.' | '#' | '%' | '&' | '@');
            latin_or_tamil |= c.is_ascii_alphabetic() || (is_tamil && !is_digit);
            clock_alphabet &= is_digit || c.is_ascii_alphabetic() || matches!(c, ':' | '.' | ' ');
            electronic_alphabet &= !is_tamil || is_digit;
            if first_digit && spaces > 0 && is_digit {
                context_tail_valid = false;
            }
            if spaces == 0 && first_digit {
                first_chunk_digits += usize::from(is_digit);
                date_prefix_valid &= is_digit || matches!(c, '/' | '-' | '.' | '–' | 's' | '\'') || is_tamil;
                context_prefix_valid &= is_digit;
            }
            // A short initial number cannot enter either contextual-phone
            // graph. At the start of a Tamil second word, the remaining
            // multiword numeric graphs require a literal from the indexed
            // unit/currency/quantity/month/era tables.
            if first_digit
                && first_chunk_digits < 4
                && spaces == 1
                && is_tamil
                && !is_digit
                && end >= 2
                && is_nemo_space(text[end - 2])
                && !*NUMERIC_WHITELIST_START
                && !tamil_continuation_matches(text, end - 1)
            {
                break;
            }
            if first_digit && spaces > 5 && (!context_prefix_valid || !context_tail_valid) {
                break;
            }
            if first_digit
                && spaces > 0
                && !date_prefix_valid
                && !context_prefix_valid
                && !clock_alphabet
                && !electronic_alphabet
            {
                break;
            }
            if (end < text.len() && !boundary(text[end])) || !possible_token_end(text, end) {
                continue;
            }
            for (symbol, spoken) in [("©", "காப்புரிமை"), ("§", "பிரிவு")] {
                if let Some(v) = s.strip_prefix(symbol) {
                    if v.chars().count() <= 6 && v.chars().all(|c| number::digit(c).is_some()) {
                        if let Some((n, leading)) = number::cardinal(v) {
                            let value = format!("{spoken} {n}").replace(' ', "\u{a0}");
                            push(out, end, 1.03 + if leading { 0.5 } else { 0.0 }, 1, field("name", &value));
                        }
                    }
                }
            }
            if let Some((a, b)) = s.split_once('–') {
                let a = a.strip_suffix(' ').unwrap_or(a);
                let b = b.strip_prefix(' ').unwrap_or(b);
                if a.chars().count() <= 6 && b.chars().count() <= 6 {
                    if let (Some((a, al)), Some((b, bl))) = (number::cardinal(a), number::cardinal(b)) {
                        push(
                            out,
                            end,
                            1.03 + if al { 0.5 } else { 0.0 } + if bl { 0.5 } else { 0.0 },
                            1,
                            field("name", &format!("{a} முதல் {b} வரை").replace(' ', "\u{a0}")),
                        );
                    }
                }
            }
            for sep in ['-', '–', '—'] {
                if let Some((a, b)) = s.split_once(sep) {
                    let a = a.strip_suffix(' ').unwrap_or(a);
                    let b = b.strip_prefix(' ').unwrap_or(b);
                    if let (Some((a, aw)), Some((b, bw))) = (date_time::spoken_clock(a), date_time::spoken_clock(b)) {
                        push(
                            out,
                            end,
                            1.03 + aw + bw,
                            1,
                            field("name", &format!("{a} முதல் {b} வரை").replace(' ', "\u{a0}")),
                        );
                    }
                }
            }
            if let Some(values) = WHITELIST.get(s.as_str()) {
                for value in values {
                    push(out, end, 1.01, 0, field("name", &value.replace(' ', "\u{a0}")));
                }
            }
            if s.contains('.') {
                for sep in [".", ". "] {
                    let trimmed = s.strip_suffix('.').unwrap_or(&s);
                    let mut count = 0;
                    let mut acronym = String::new();
                    for part in trimmed.split(sep) {
                        if part.len() != 1 || !part.as_bytes()[0].is_ascii_uppercase() {
                            count = 0;
                            break;
                        }
                        count += 1;
                        acronym.push(part.as_bytes()[0] as char);
                    }
                    if count >= 3 {
                        push(out, end, 1.01, 0, field("name", &acronym));
                    }
                }
            }
            if digits == 0 && !fraction {
                if electronic_mark {
                    for (v, w) in identifiers::electronic(&s) {
                        push(out, end, 1.1 + w, 11, format!("electronic {{ {v} }}"));
                    }
                }
                if serial_mark && spaces == 0 {
                    for (v, w) in identifiers::serial(&s) {
                        push(out, end, 1.11 + w, 12, field("name", &v.replace(' ', "\u{a0}")));
                    }
                }
                for v in identifiers::roman(&s) {
                    push(out, end, 1.1, 13, format!("roman {{ {v} }}"));
                }
                continue;
            }
            let body = s.strip_prefix('-').unwrap_or(&s);
            if let Some(leading) = number::recognize(body) {
                push(out, end, 1.1 + if leading { 0.5 } else { 0.0 }, 2, String::new());
            }
            let (negative, body) =
                s.strip_prefix('-').map(|x| (true, x.strip_prefix(' ').unwrap_or(x))).unwrap_or((false, s.as_str()));
            if let Some((a, b)) = body.split_once('.') {
                if !b.is_empty() && b.chars().all(|c| number::digit(c).is_some()) {
                    if let Some(leading) = number::recognize(a) {
                        push(out, end, 1.1 + if leading { 0.5 } else { 0.0 }, 3, String::new());
                    }
                }
            }
            if numeric_start && spaces <= 2 && QUANTITY_ENDS.contains(&c) {
                for (key, value) in
                    include_str!("../../../data/ta/numbers/quantities.tsv").lines().filter_map(|l| l.split_once('\t'))
                {
                    if let Some(n) = body.strip_suffix(key) {
                        let n = n.strip_suffix(' ').unwrap_or(n);
                        let sign = if negative { "negative: \"true\" " } else { "" };
                        let fields = if let Some((a, b)) = n.split_once('.') {
                            number::final_graph(a).and_then(|(v, leading)| {
                                number::digitwise(b).filter(|x| !x.is_empty()).map(|fr| {
                                    (
                                        format!("{} {}", field("integer_part", &v), field("fractional_part", &fr)),
                                        if leading { 0.5 } else { 0.0 },
                                    )
                                })
                            })
                        } else if n.chars().count() <= 3 && !matches!(n.chars().next(), Some('0' | '௦')) {
                            number::cardinal(n).map(|(v, _)| (field("integer_part", &v), 0.0))
                        } else {
                            None
                        };
                        if let Some((fields, extra)) = fields {
                            push(
                                out,
                                end,
                                1.1 + extra,
                                3,
                                format!("decimal {{ {sign}{fields} {} }}", field("quantity", value)),
                            );
                        }
                    }
                }
            }
            if numeric_start && spaces <= 1 && (fraction || s.contains('/')) {
                fraction::costs(&s, |weight| {
                    push(out, end, 1.05 + weight, 4, String::new());
                });
            }
            if numeric_start && spaces <= 3 {
                quantity::measure_costs(&s, |weight| {
                    push(out, end, 1.08 + weight, 5, String::new());
                });
            }
            if spaces <= 5 && (currency_start || CURRENCY_EDGES.1.contains(&c) || s.contains('/')) {
                quantity::money_costs(&s, |weight| {
                    push(out, end, 1.08 + weight, 6, String::new());
                });
            }
            if date_prefix_valid
                && spaces <= 3
                && (spaces > 0
                    || s.chars().any(|ch| matches!(ch, '/' | '-' | '.' | '–'))
                    || s.ends_with('s')
                    || s.ends_with("கள்"))
            {
                date_time::date_costs(&s, |weight| {
                    push(out, end, 1.08 + weight, 7, String::new());
                });
            }
            if first_digit
                && clock_alphabet
                && spaces <= 2
                && (s.contains(':')
                    || s.ends_with("am")
                    || s.ends_with("AM")
                    || s.ends_with("Am")
                    || s.ends_with("pm")
                    || s.ends_with("PM")
                    || s.ends_with("Pm")
                    || s.ends_with(".m.")
                    || s.ends_with(".M."))
            {
                if let Some((omit, weight)) = date_time::recognize_clock(&s) {
                    if omit {
                        push(out, end, 1.1 + weight, 8, String::new());
                    }
                    push(out, end, 1.1 + weight, 8, String::new());
                }
            }
            if first_digit && matches!(c, 't' | 'd' | 'h' | 'ு') {
                if ordinal::recognize(&s) {
                    push(out, end, 1.1, 10, String::new());
                }
            }
            if digits >= 7 && (first_digit || matches!(first, '+' | '(')) {
                for (v, w) in identifiers::telephone(&s) {
                    push(out, end, 1.1 + w, 9, format!("telephone {{ {v} }}"));
                }
                if let Some(v) = identifiers::international_name(&s) {
                    push(out, end, 1.1, 9, field("name", &v.replace(' ', "\u{a0}")));
                }
            }
            if digits >= 4
                && context_prefix_valid
                && context_tail_valid
                && ((first_digit && first_chunk_digits >= 4) || ('\u{b80}'..='\u{bff}').contains(&first))
            {
                identifiers::telephone_context_costs(&s, |weight| {
                    push(out, end, 1.1 + weight, 9, String::new());
                });
            }
            if electronic_mark {
                for (v, w) in identifiers::electronic(&s) {
                    push(out, end, 1.1 + w, 11, format!("electronic {{ {v} }}"));
                }
            }
            if spaces == 0 && (serial_mark || (digits > 0 && latin_or_tamil)) {
                for (v, w) in identifiers::serial(&s) {
                    push(out, end, 1.11 + w, 12, field("name", &v.replace(' ', "\u{a0}")));
                }
            }
            for v in identifiers::roman(&s) {
                push(out, end, 1.1, 13, format!("roman {{ {v} }}"));
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let mut pos = start;
        let prefix = match text[pos] {
            '~' => Some("தோராயமாக"),
            '±' => Some("பிளஸ்\u{a0}அல்லது\u{a0}மைனஸ்"),
            _ => None,
        };
        if prefix.is_some() {
            pos += 1;
            if text.get(pos) == Some(&' ') {
                pos += 1;
            }
        }
        let input: String = text[pos..candidate.end].iter().collect();
        let (negative, body) = input
            .strip_prefix('-')
            .map(|x| (true, if candidate.rank == 3 { x.strip_prefix(' ').unwrap_or(x) } else { x }))
            .unwrap_or((false, input.as_str()));
        let sign = if negative { "negative: \"true\" " } else { "" };
        let tag = match candidate.rank {
            2 => {
                let mut tag = String::with_capacity(96);
                tag.push_str("cardinal { ");
                tag.push_str(sign);
                tag.push_str("integer: \"");
                number::write_final(&mut tag, body).expect("recognized Tamil cardinal");
                tag.push_str("\" }");
                tag
            }
            3 => {
                let (a, b) = body.split_once('.').expect("recognized Tamil decimal");
                let mut tag = String::with_capacity(128);
                tag.push_str("decimal { ");
                tag.push_str(sign);
                tag.push_str("integer_part: \"");
                number::write_final(&mut tag, a).expect("recognized Tamil decimal integer");
                tag.push_str("\" fractional_part: \"");
                number::write_digits(&mut tag, b).expect("recognized Tamil decimal fraction");
                tag.push_str("\" }");
                tag
            }
            4 => {
                let expected = candidate.cost + if prefix.is_some() { cost(2.0) } else { 0 };
                let (fields, _) = fraction::classify(&input)
                    .into_iter()
                    .find(|(_, w)| cost(1.05 + *w) == expected)
                    .expect("recognized Tamil fraction");
                format!("fraction {{ {fields} }}")
            }
            5 => {
                let expected = candidate.cost + if prefix.is_some() { cost(2.0) } else { 0 };
                let (fields, _) = quantity::measure(&input)
                    .into_iter()
                    .find(|(_, w)| cost(1.08 + *w) == expected)
                    .expect("recognized Tamil measure");
                format!("measure {{ {fields} }}")
            }
            6 => {
                let expected = candidate.cost + if prefix.is_some() { cost(2.0) } else { 0 };
                let (fields, _) = quantity::money(&input)
                    .into_iter()
                    .find(|(_, w)| cost(1.08 + *w) == expected)
                    .expect("recognized Tamil currency");
                format!("money {{ {fields} }}")
            }
            7 => {
                let (fields, _) = date_time::date(&input)
                    .into_iter()
                    .find(|(_, w)| cost(1.08 + *w) == candidate.cost)
                    .expect("recognized Tamil date");
                format!("date {{ {fields} }}")
            }
            8 => {
                let (tag, _) = date_time::clock(body, true)
                    .filter(|(_, w)| cost(1.1 + *w) == candidate.cost)
                    .expect("recognized Tamil clock");
                tag
            }
            9 => {
                let (fields, _) = identifiers::telephone_context(&input)
                    .into_iter()
                    .find(|(_, w)| cost(1.1 + *w) == candidate.cost)
                    .expect("recognized Tamil telephone context");
                format!("telephone {{ {fields} }}")
            }
            10 => {
                let value = ordinal::classify(&input).expect("recognized Tamil ordinal");
                format!("ordinal {{ {} }}", field("integer", &value))
            }
            _ => return,
        };
        candidate.tag =
            if let Some(spoken) = prefix { format!("{} }} tokens {{ {tag}", field("name", spoken)) } else { tag };
    }
}
struct Word;
impl Classifier for Word {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if (start > 0 && !boundary(text[start - 1])) || !possible_token_start(text, start) {
            return;
        }
        let mut end = start;
        let mut valid = true;
        let mut seen_word = false;
        let mut in_word = false;
        let mut tamil = true;
        let mut tamil_runs = 0;
        let mut punct_count = 0;
        let mut isolated = false;
        while end < text.len() && !is_nemo_space(text[end]) {
            let c = text[end];
            end += 1;
            if punct_mark(c) {
                if in_word {
                    if tamil {
                        tamil_runs += 1
                    }
                    in_word = false;
                    tamil = true;
                    punct_count = 0;
                    isolated = false
                }
                punct_count += 1;
                isolated |= "%‰~&§¶".contains(c);
                if isolated && punct_count > 1 {
                    valid = false
                }
            } else {
                if "$¢€₩£¥₹₨₦৳₽₫₱฿₵₪₺₴₸₼₾֏#%‰~±&©®™§¶½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅐⅛⅜⅝⅞⅑⅒".contains(c)
                {
                    valid = false
                }
                in_word = true;
                seen_word = true;
                punct_count = 0;
                isolated = false;
                tamil &= (('\u{b80}'..='\u{bff}').contains(&c) && !('\u{be6}'..='\u{bef}').contains(&c))
                    || matches!(c, '\u{200c}' | '\u{200d}');
            }
            if (end < text.len() && !boundary(text[end])) || !possible_token_end(text, end) {
                continue;
            }
            if valid && seen_word {
                let n = tamil_runs + usize::from(in_word && tamil);
                push(out, end, 100.0 - 0.0001 * n as f64, 0, String::new());
            }
            push(out, end, 200.0, 1, String::new());
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let bytes: usize = text[start..candidate.end].iter().map(|c| c.len_utf8()).sum();
        let mut tag = String::with_capacity(bytes + 8);
        tag.push_str("name: \"");
        tag.extend(&text[start..candidate.end]);
        tag.push('"');
        candidate.tag = tag;
    }
}

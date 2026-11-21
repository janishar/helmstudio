//! Hebrew deterministic text normalization.
mod abbreviations;
mod cardinal;
mod date;
mod decimal;
mod electronic;
mod fraction;
mod measure;
mod money;
mod ordinal;
mod roman;
mod serial;
mod telephone;
mod time;
mod whitelist;

use crate::lang::common::{self, Fields};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

static ABBREV_PREFIXES: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: std::collections::HashMap<char, Vec<&'static str>> = std::collections::HashMap::new();
    for &(word, _) in abbreviations::ABBREVIATIONS {
        if let Some(first) = word.chars().next() {
            index.entry(first).or_default().push(word);
        }
    }
    index
});

const DATE_STARTS: &[&str] = &[
    "שנות ה-",
    "January",
    "Jan",
    "February",
    "Feb",
    "March",
    "Mar",
    "April",
    "Apr",
    "May",
    "June",
    "Jun",
    "July",
    "Jul",
    "August",
    "Aug",
    "September",
    "Sep",
    "Sept",
    "October",
    "Oct",
    "November",
    "Nov",
    "December",
    "Dec",
];
const ROMAN_STARTS: &[&str] = &[
    "פרק",
    "חלק",
    "כרך",
    "סעיף",
    "לוח",
    "נספח",
    "שער",
    "Chapter",
    "Part",
    "Section",
    "Volume",
    "Book",
    "לואי",
    "הנרי",
    "שלמה",
    "דוד",
    "ג'ורג'",
    "אליזבת",
    "ויליאם",
    "פיליפ",
    "קרל",
    "ניקולאי",
    "אלכסנדר",
    "נפוליאון",
    "מלך",
    "המלך",
    "האפיפיור",
    "קיסר",
    "הקיסר",
    "Louis",
    "Henry",
    "George",
    "Elizabeth",
    "William",
    "King",
    "Pope",
    "המאה",
    "במאה",
    "מאה",
];
static CONTEXT_PREFIXES: LazyLock<[std::collections::HashMap<char, Vec<&'static str>>; 2]> = LazyLock::new(|| {
    let mut indices = [std::collections::HashMap::<char, Vec<&'static str>>::new(), std::collections::HashMap::new()];
    for (idx, words) in [DATE_STARTS, ROMAN_STARTS].iter().enumerate() {
        for &word in *words {
            if let Some(first) = word.chars().next() {
                indices[idx].entry(first).or_default().push(word);
            }
        }
    }
    indices
});

struct Hebrew {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Marks,
}
impl Grammar for Hebrew {
    fn code(&self) -> &'static str {
        "he"
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
            return Some(v);
        }
        let mut outer = Fields::new(token);
        let inner = outer.dict("date")?;
        if !outer.done() {
            return None;
        }
        let mut f = Fields::new(inner);
        let day = f.take("day")?;
        let month = f.take("month")?;
        let year = f.take("year")?;
        if !f.done() || day.is_empty() || month.is_empty() || year.is_empty() {
            return None;
        }
        Some(format!("{day} {month} {year}").replace('\u{a0}', " "))
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
            let value = crate::permute::first_ordering(body, &mut |ordering: &Dict| self.verbalize(ordering))?;
            out.push_str(&value);
        }
        Some(out)
    }
}
/// Exact punctuation alphabet of the Hebrew `PunctuationFst`.
const ASCII_MARKS: [bool; 128] = {
    let mut marks = [false; 128];
    let bytes = b"!#$%&'()*+,-./:;<=>?@^_`{|}~\"[]";
    let mut i = 0;
    while i < bytes.len() {
        marks[bytes[i] as usize] = true;
        i += 1;
    }
    marks
};

#[inline(always)]
fn mark(c: char) -> bool {
    if c.is_ascii() {
        return ASCII_MARKS[c as usize];
    }
    matches!(c, '־' | '״' | '׳' | '׀' | '׃' | '«' | '»' | '„' | '“' | '”' | '‘' | '’' | '–' | '—' | '…')
}

#[inline(always)]
fn boundary(c: char) -> bool {
    is_nemo_space(c) || mark(c)
}

#[inline(always)]
fn token_start(text: &[char], start: usize) -> bool {
    start == 0 || boundary(text[start - 1])
}

#[inline(always)]
fn token_end(text: &[char], end: usize) -> bool {
    end == text.len() || boundary(text[end])
}

struct Marks;
impl Classifier for Marks {
    fn candidates(&self, t: &[char], start: usize, out: &mut Vec<Candidate>) {
        if let Some(&c) = t.get(start) {
            if mark(c) {
                out.push(Candidate { end: start + 1, cost: cost(1.1), rank: 0, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        candidate.tag.push_str("name: \"");
        candidate.tag.push(text[start]);
        candidate.tag.push('"');
    }
}

thread_local! {
    /// Reusable UTF-8 span buffer, never a cache of normalization results.
    static SPAN: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}

type Reader = fn(&str) -> Vec<(String, i64)>;
struct Rules {
    read: Reader,
    date: bool,
    kind: u8,
}
impl Rules {
    fn outputs(&self, s: &str) -> Vec<(String, i64)> {
        let mut v = (self.read)(s);
        if !self.date {
            for (name, _) in &mut v {
                let mut tag = String::with_capacity(name.len() + 8);
                tag.push_str("name: \"");
                tag.extend(name.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
                tag.push('"');
                *name = tag;
            }
        }
        if let Some((prefix, rest)) =
            s.strip_prefix('~').map(|s| ("בערך", s)).or_else(|| s.strip_prefix('±').map(|s| ("פלוס\u{a0}מינוס", s)))
        {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            for (name, weight) in (self.read)(rest) {
                let mut tag = String::with_capacity(prefix.len() + name.len() + 32);
                tag.push_str("name: \"");
                tag.push_str(prefix);
                tag.push_str("\" } tokens { ");
                if self.date {
                    tag.push_str(&name);
                } else {
                    tag.push_str("name: \"");
                    tag.extend(name.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
                    tag.push('"');
                }
                v.push((tag, weight - cost(2.0)));
            }
        }
        v
    }
    fn scan(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let mut content = start;
        if matches!(text.get(content), Some('~' | '±')) {
            content += 1;
            if text.get(content) == Some(&' ') {
                content += 1;
            }
        }
        let numeric_whitelist = self.kind == 0 && text.get(content).is_some_and(char::is_ascii_digit);
        let numeric_electronic = self.kind == 6 && text.get(content).is_some_and(char::is_ascii_digit);
        SPAN.with_borrow_mut(|s| {
            s.clear();
            let mut currency_seen = false;
            let mut unit_start = None;
            let mut measurement = false;
            let mut currency_start = None;
            let mut evidence = 0u16;
            for (i, &c) in text.iter().enumerate().skip(start) {
                if i >= content {
                    let allowed = match self.kind {
                        0 if numeric_whitelist => {
                            c.is_ascii_digit()
                                || matches!(
                                    c,
                                    ',' | '.' | ' ' | '%' | '‰' | '¢' | '+' | '=' | '^' | '-' | '–' | '—' | '־'
                                )
                        }
                        0 | 1 | 3 | 4 | 5 => c != ':',
                        2 => {
                            c.is_ascii_digit()
                                || c.is_ascii_uppercase()
                                || matches!(c, ':' | ' ' | '-' | '–' | '—' | '־')
                        }
                        6 if numeric_electronic => c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_' | '@'),
                        6 => {
                            c.is_ascii_alphanumeric()
                                || matches!(c, '.' | '/' | ':' | '?' | '=' | '&' | '-' | '_' | '@')
                        }
                        7 => c.is_ascii_digit() || matches!(c, '+' | ' ' | '-' | '.' | '(' | ')'),
                        8 => c.is_ascii_alphanumeric() || matches!(c, '.' | ' ' | '-' | '–' | '—' | '־' | '^'),
                        9 => {
                            c.is_ascii_digit()
                                || matches!(
                                    c,
                                    ',' | '/'
                                        | ' '
                                        | '-'
                                        | '−'
                                        | '½'
                                        | '⅓'
                                        | '⅔'
                                        | '¼'
                                        | '¾'
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
                                )
                        }
                        10 => {
                            c.is_ascii_digit()
                                || matches!(c, ',' | '.' | ' ' | '-' | '−' | '–' | '—' | '־' | 'k' | 'M' | 'B')
                        }
                        11 => c.is_ascii_digit() || matches!(c, 'ה' | '-' | '.'),
                        12 => c.is_ascii_digit() || matches!(c, ',' | ':' | '-' | '−' | '–' | '—' | '־'),
                        _ => true,
                    };
                    if !allowed {
                        break;
                    }
                }
                if self.kind == 5
                    && i >= content
                    && unit_start.is_none()
                    && !c.is_ascii_digit()
                    && !matches!(c, ',' | '.' | '-' | '−' | '–' | '—' | '־' | ' ')
                {
                    unit_start = Some(s.len());
                }
                if self.kind == 4
                    && i >= content
                    && !currency_seen
                    && currency_start.is_none()
                    && !c.is_ascii_digit()
                    && !matches!(c, ',' | '.' | '-' | '−' | '–' | '—' | '־' | ' ')
                {
                    currency_start = Some(s.len());
                }
                s.push(c);
                if self.kind == 1 && !c.is_ascii_digit() {
                    let date = s.strip_prefix('~').or_else(|| s.strip_prefix('±')).unwrap_or(s.as_str());
                    let date = date.strip_prefix(' ').unwrap_or(date);
                    if !date::may_continue(date) {
                        break;
                    }
                }
                // Monotone per-span evidence: avoid rescanning the accumulated UTF-8 input
                // for every eligible end. Modifier bytes only add conservative evidence.
                evidence |= match self.kind {
                    0 if matches!(c, '%' | '‰' | '¢' | '+' | '=' | '^') => 1,
                    2 if c == ':' => 1,
                    6 if matches!(c, '.' | '@' | ':') => 1,
                    7 if matches!(c, '+' | ' ' | '-' | '(' | ')') => 1,
                    8 => {
                        (c.is_ascii_digit() as u16)
                            | ((c.is_ascii_alphabetic() as u16) << 1)
                            | ((matches!(c, '.' | '^') as u16) << 2)
                    }
                    9 if matches!(
                        c,
                        '/' | '½'
                            | '⅓'
                            | '⅔'
                            | '¼'
                            | '¾'
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
                    ) =>
                    {
                        1
                    }
                    10 if matches!(c, '.' | 'k' | 'M' | 'B') => 1,
                    11 if c == '.' => 1,
                    _ => 0,
                };
                if let Some(unit_start) = unit_start {
                    let Some(complete) = measure::unit_state(&s[unit_start..]) else { break };
                    measurement = complete;
                }
                if self.kind == 4 && !currency_seen {
                    currency_seen = money::currency_ending(s.as_str(), c);
                }
                if self.kind == 4 && !currency_seen {
                    if let Some(currency_start) = currency_start {
                        if !money::currency_prefix(&s[currency_start..]) {
                            break;
                        }
                    }
                }
                if !token_end(text, i + 1) {
                    continue;
                }
                let mut rank = 0;
                let mut push = |weight| {
                    out.push(Candidate { end: i + 1, cost: weight, rank, tag: String::new() });
                    rank += 1;
                };
                if self.kind == 12 || self.kind == 2 && evidence != 0 {
                    let emit = |s: &str, f: &mut dyn FnMut(i64)| {
                        if self.kind == 12 {
                            cardinal::weights(s, f);
                        } else {
                            time::weights(s, f);
                        }
                    };
                    emit(s.as_str(), &mut push);
                    if let Some(rest) = s.strip_prefix('~').or_else(|| s.strip_prefix('±')) {
                        let rest = rest.strip_prefix(' ').unwrap_or(rest);
                        emit(rest, &mut |w| push(w - cost(2.0)));
                    }
                } else if self.kind != 2 {
                    if match self.kind {
                        4 => currency_seen,
                        5 => measurement,
                        _ => may_match(self.kind, s.as_str(), evidence),
                    } {
                        for (_, weight) in (self.read)(s.as_str()) {
                            push(weight);
                        }
                    }
                    if let Some(rest) = s.strip_prefix('~').or_else(|| s.strip_prefix('±')) {
                        let rest = rest.strip_prefix(' ').unwrap_or(rest);
                        if match self.kind {
                            4 => currency_seen,
                            5 => measurement,
                            _ => may_match(self.kind, rest, evidence),
                        } {
                            for (_, weight) in (self.read)(rest) {
                                push(weight - cost(2.0));
                            }
                        }
                    }
                }
            }
        });
    }
}
/// Cheap necessary conditions; these never choose between accepted readings.
fn may_match(kind: u8, s: &str, evidence: u16) -> bool {
    match kind {
        0 if s.as_bytes().first().is_some_and(u8::is_ascii_digit) => evidence != 0,
        1 => date::may_match(s),
        6 | 7 | 9 | 10 => evidence != 0,
        8 => evidence & 4 != 0 || evidence & 3 == 3,
        11 => evidence != 0 || s.starts_with('ה'),
        _ => true,
    }
}

fn starts_with(text: &[char], start: usize, word: &str) -> bool {
    let mut input = text[start..].iter();
    word.chars().all(|c| input.next() == Some(&c))
}

fn starts_any(text: &[char], start: usize, words: &[&str]) -> bool {
    words.iter().any(|word| starts_with(text, start, word))
}

impl Classifier for Rules {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start) {
            return;
        }
        let mut first = start;
        if matches!(text.get(first), Some('~' | '±')) {
            first += 1;
            if text.get(first) == Some(&' ') {
                first += 1;
            }
        }
        let Some(&c) = text.get(first) else {
            // A modifier can itself be a whitelist symbol.
            if self.kind != 0 {
                return;
            }
            return self.scan(text, start, out);
        };
        let digit = c.is_ascii_digit();
        let signed = digit || matches!(c, '-' | '−');
        let valid = match self.kind {
            0 => {
                digit
                    || c.is_ascii_alphabetic()
                    || matches!(c, '&' | '©' | '§' | '®' | '™' | '~' | '±' | '#')
                    || ABBREV_PREFIXES.get(&c).is_some_and(|words| starts_any(text, first, words))
                    || starts_any(
                        text,
                        first,
                        &["מס'", "מס׳", "מס.", "מס", "מספר", "עמ'", "עמ׳", "עמ.", "עמוד", "סע'", "סע׳", "סע.", "סעיף"],
                    )
            }
            1 => digit || CONTEXT_PREFIXES[0].get(&c).is_some_and(|words| starts_any(text, first, words)),
            3 => CONTEXT_PREFIXES[1].get(&c).is_some_and(|words| starts_any(text, first, words)),
            4 => {
                signed
                    || c.is_ascii_alphabetic()
                    || matches!(
                        c,
                        '$' | '₪'
                            | '€'
                            | '£'
                            | '¥'
                            | '₹'
                            | '₽'
                            | '₩'
                            | '₺'
                            | '₴'
                            | '₫'
                            | '৳'
                            | '₦'
                            | '₱'
                            | '฿'
                            | '֏'
                    )
            }
            2 => digit,            // clock
            5 | 10 | 12 => signed, // measure, decimal, cardinal
            7 => digit || matches!(c, '+' | '('),
            8 => c.is_ascii_alphanumeric(),
            9 => {
                signed
                    || matches!(
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
                            | '⅐'
                            | '⅛'
                            | '⅜'
                            | '⅝'
                            | '⅞'
                            | '⅑'
                            | '⅒'
                    )
            }
            11 => digit || c == 'ה',
            6 => c.is_ascii_alphanumeric(),
            _ => true,
        };
        if valid || self.kind == 0 && matches!(text[start], '~' | '±') {
            self.scan(text, start, out);
        }
    }
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        SPAN.with_borrow_mut(|s| {
            s.clear();
            s.extend(text[start..c.end].iter().copied());
            if self.kind == 2 || self.kind == 12 {
                let rank = c.rank as usize;
                let mut prefix = None;
                let mut input = s.as_str();
                // Neither cardinal nor time has a direct path beginning with a modifier.
                if let Some(rest) = input.strip_prefix('~') {
                    prefix = Some("בערך");
                    input = rest.strip_prefix(' ').unwrap_or(rest);
                } else if let Some(rest) = input.strip_prefix('±') {
                    prefix = Some("פלוס\u{a0}מינוס");
                    input = rest.strip_prefix(' ').unwrap_or(rest);
                }
                let name = if self.kind == 2 { time::name_at(input, rank) } else { cardinal::name_at(input, rank) };
                if let Some(name) = name {
                    c.tag.clear();
                    if let Some(prefix) = prefix {
                        c.tag.push_str("name: \"");
                        c.tag.push_str(prefix);
                        c.tag.push_str("\" } tokens { ");
                    }
                    c.tag.push_str("name: \"");
                    c.tag.extend(name.chars().map(|ch| if ch == ' ' { '\u{a0}' } else { ch }));
                    c.tag.push('"');
                }
                return;
            }
            if let Some((tag, _)) = self.outputs(s.as_str()).into_iter().nth(c.rank as usize) {
                c.tag = tag;
            }
        });
    }
}
struct Word;
impl Classifier for Word {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start) {
            return;
        }
        for i in start..text.len() {
            if is_nemo_space(text[i]) {
                break;
            }
            if token_end(text, i + 1) {
                out.push(Candidate { end: i + 1, cost: cost(100.0), rank: 0, tag: String::new() });
            }
        }
        if matches!(text.get(start), Some('~' | '±')) {
            let mut pos = start + 1;
            if text.get(pos) == Some(&' ') {
                pos += 1;
            }
            for i in pos..text.len() {
                if is_nemo_space(text[i]) {
                    break;
                }
                if token_end(text, i + 1) {
                    out.push(Candidate { end: i + 1, cost: cost(98.0), rank: 1, tag: String::new() });
                }
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        let mut pos = start;
        c.tag.clear();
        if c.rank == 1 {
            let prefix = if text[start] == '~' { "בערך" } else { "פלוס\u{a0}מינוס" };
            c.tag.push_str("name: \"");
            c.tag.push_str(prefix);
            c.tag.push_str("\" } tokens { name: \"");
            pos += 1;
            if text.get(pos) == Some(&' ') {
                pos += 1;
            }
        } else {
            c.tag.push_str("name: \"");
        }
        c.tag.extend(text[pos..c.end].iter().copied());
        c.tag.push('"');
    }
}
static GRAMMAR: LazyLock<Hebrew> = LazyLock::new(|| Hebrew {
    classifiers: vec![
        Box::new(Rules { read: whitelist::readings, date: false, kind: 0 }),
        Box::new(Rules { read: date::readings, date: true, kind: 1 }),
        Box::new(Rules { read: time::readings, date: false, kind: 2 }),
        Box::new(Rules { read: roman::readings, date: false, kind: 3 }),
        Box::new(Rules { read: money::readings, date: false, kind: 4 }),
        Box::new(Rules { read: measure::readings, date: false, kind: 5 }),
        Box::new(Rules { read: electronic::readings, date: false, kind: 6 }),
        Box::new(Rules { read: telephone::readings, date: false, kind: 7 }),
        Box::new(Rules { read: serial::readings, date: false, kind: 8 }),
        Box::new(Rules { read: fraction::readings, date: false, kind: 9 }),
        Box::new(Rules { read: decimal::readings, date: false, kind: 10 }),
        Box::new(Rules { read: ordinal::readings, date: false, kind: 11 }),
        Box::new(Rules { read: cardinal::readings, date: false, kind: 12 }),
        Box::new(Word),
    ],
    punct: Marks,
});
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

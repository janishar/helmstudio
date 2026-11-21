//! Malay text normalization grammar.
mod calendar;
mod lazy;
mod number;
mod numeric;
mod other;
mod quantity;
mod serial;
mod verbalize;

use crate::lang::common::{Punctuation, possible_token_end, possible_token_start};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;

pub(crate) fn lookup(table: &'static str, key: &str) -> Option<&'static str> {
    use std::collections::BTreeMap;
    type Rows = [Vec<(&'static str, &'static str)>; 256];
    static TABLES: LazyLock<Vec<(&'static str, Rows)>> = LazyLock::new(|| {
        [
            include_str!("../../../data/ms/months/numbers.tsv"),
            include_str!("../../../data/ms/months/names.tsv"),
            include_str!("../../../data/ms/months/abbr.tsv"),
            include_str!("../../../data/ms/time/time_zone.tsv"),
            include_str!("../../../data/ms/whitelist.tsv"),
            include_str!("../../../data/ms/measure/measurements.tsv"),
            include_str!("../../../data/ms/measure/denominators.tsv"),
            include_str!("../../../data/ms/electronic/symbols.tsv"),
        ]
        .into_iter()
        .map(|table| {
            let mut index = BTreeMap::new();
            for (key, value) in table.lines().filter_map(|line| line.split_once('\t')) {
                index.entry(key).or_insert(value.trim_end_matches('\r'));
            }
            let mut buckets: Rows = std::array::from_fn(|_| Vec::new());
            for (key, value) in index {
                if let Some(first) = key.as_bytes().first() {
                    buckets[*first as usize].push((key, value));
                }
            }
            (table, buckets)
        })
        .collect()
    });
    // Each private caller uses one of the embedded tables. Resolve its length
    // once; content comparison is needed only for same-length source tables.
    static BY_LENGTH: LazyLock<Vec<usize>> = LazyLock::new(|| {
        let max = TABLES.iter().map(|(source, _)| source.len()).max().unwrap_or(0);
        let mut slots = vec![usize::MAX; max + 1];
        for (i, (source, _)) in TABLES.iter().enumerate() {
            let slot = &mut slots[source.len()];
            *slot = if *slot == usize::MAX { i } else { usize::MAX - 1 };
        }
        slots
    });
    let slot = *BY_LENGTH.get(table.len())?;
    let selected = if slot == usize::MAX {
        return None;
    } else if slot == usize::MAX - 1 {
        TABLES.iter().find(|(source, _)| *source == table)?
    } else {
        &TABLES[slot]
    };
    let entries = &selected.1[*key.as_bytes().first()? as usize];
    entries.binary_search_by(|(entry, _)| entry.cmp(&key)).ok().map(|i| entries[i].1)
}
struct Malay {
    classifiers: Vec<Box<dyn Classifier>>,
}
impl Grammar for Malay {
    fn code(&self) -> &'static str {
        "ms"
    }
    fn shape(&self) -> Shape {
        Shape::Separated
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&*MARKS)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalize::verbalize(token)
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i > 0 {
                out.push(' ');
            }
            if let [(name, Value::Str(value))] = body.as_slice() {
                if name == "name" {
                    if value.is_empty() || value.contains(' ') {
                        return None;
                    }
                    for c in value.chars() {
                        out.push(if c == '\u{a0}' { ' ' } else { c });
                    }
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, value)] = fields.as_slice() {
                        if key == "integer" {
                            let value = crate::lang::common::str_value(value)?;
                            if value.is_empty() {
                                return None;
                            }
                            out.push_str(value);
                            continue;
                        }
                    }
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
}
static MARKS: LazyLock<Punctuation> = LazyLock::new(|| Punctuation::en(cost(1.1)));
fn boundary(c: char) -> bool {
    is_nemo_space(c) || MARKS.is_mark(c)
}
/// No rule spanning whitespace can consume an arbitrary ASCII word.  The
/// admissible words are the lexical input rows and the contextual keywords.
fn can_span_space(first: &str) -> bool {
    if first.is_empty() || !first.bytes().all(|b| b.is_ascii_alphabetic()) {
        return true;
    }
    static STARTS: LazyLock<std::collections::HashSet<String>> = LazyLock::new(|| {
        let mut starts = std::collections::HashSet::new();
        for table in [
            include_str!("../../../data/ms/whitelist.tsv"),
            include_str!("../../../data/ms/money/currency.tsv"),
            include_str!("../../../data/ms/months/names.tsv"),
            include_str!("../../../data/ms/months/abbr.tsv"),
            include_str!("../../../data/ms/numbers/quantities.tsv"),
            include_str!("../../../data/ms/money/quantity_abbr.tsv"),
            include_str!("../../../data/ms/measure/measurements.tsv"),
            include_str!("../../../data/ms/measure/denominators.tsv"),
            include_str!("../../../data/ms/time/time_zone.tsv"),
        ] {
            for line in table.lines() {
                if let Some((key, _)) = line.split_once('\t') {
                    for word in key.split(' ') {
                        starts.insert(word.to_ascii_lowercase());
                    }
                }
            }
        }
        for word in [
            "no",
            "tahun",
            "era",
            "abad",
            "pukul",
            "pkl",
            "jam",
            "bab",
            "bahagian",
            "jilid",
            "volum",
            "volume",
            "seksyen",
            "fasal",
            "pasal",
            "ayat",
            "lampiran",
            "buku",
            "siri",
            "peringkat",
            "tahap",
            "fasa",
            "kelas",
            "semester",
            "suku",
            "perang",
            "sultan",
            "raja",
            "permaisuri",
            "yang",
            "paus",
            "kaisar",
            "maharaja",
            "elizabeth",
            "charles",
            "henry",
            "george",
            "edward",
            "william",
            "richard",
            "james",
            "victoria",
            "louis",
            "napoleon",
            "philip",
            "felipe",
            "wilhelm",
            "nicholas",
            "nikolai",
            "alexander",
            "aleksander",
            "rama",
            "hassan",
            "mohammed",
            "abdullah",
            "salman",
            "ramses",
            "hamengkubuwono",
            "pakubuwono",
            "mangkunegara",
            "yohanes",
            "benediktus",
            "fransiskus",
            "pius",
            "leo",
            "paulus",
            "gregorius",
            "agong",
            "dunia",
            "sm",
            "seu",
            "bc",
            "bce",
            "ad",
            "ce",
            "eu",
            "m",
            "h",
            "i",
            "v",
            "x",
            "l",
            "c",
            "d",
        ] {
            starts.insert(word.to_owned());
        }
        starts
    });
    static MAX_LENGTH: LazyLock<usize> = LazyLock::new(|| STARTS.iter().map(String::len).max().unwrap_or(0));
    if first.len() > *MAX_LENGTH {
        return false;
    }
    if first.bytes().all(|c| !c.is_ascii_uppercase()) {
        return STARTS.contains(first);
    }
    let mut stack = [0u8; 64];
    if first.len() <= stack.len() {
        for (dst, src) in stack.iter_mut().zip(first.bytes()) {
            *dst = src.to_ascii_lowercase();
        }
        return std::str::from_utf8(&stack[..first.len()]).ok().is_some_and(|key| STARTS.contains(key));
    }
    STARTS.contains(&first.to_ascii_lowercase())
}
struct Union;
fn emit(out: &mut Vec<Candidate>, end: usize, tag: String, weight: f64, rank: i32) {
    out.push(Candidate { end, tag, cost: cost(weight), rank });
}
#[derive(Default)]
struct Scan {
    digit: bool,
    vulgar: bool,
    power: bool,
    space: bool,
    currency: bool,
    date: bool,
    clock: bool,
    unit: bool,
    decimal: bool,
}
impl Scan {
    fn push(&mut self, c: char) {
        self.digit |= c.is_ascii_digit();
        self.vulgar |= !c.is_ascii() && "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c);
        self.power |= c == '²' || c == '³';
        self.space |= is_nemo_space(c);
        self.currency |= c.is_ascii_alphabetic() || c == '$' || !c.is_ascii();
        self.date |= c.is_ascii_alphabetic() || matches!(c, '-' | '/' | '.' | ' ' | '\'') || !c.is_ascii();
        self.clock |= c.is_ascii_alphabetic() || matches!(c, ':' | '.');
        self.decimal |= c == '.' || c == ',';
        self.unit |= c.is_alphabetic() || matches!(c, '%' | '‰' | '°' | '℃' | '℉' | 'Ω');
    }
}
fn classify(s: &str, end: usize, out: &mut Vec<Candidate>, only: Option<i32>, scan: &Scan) {
    let start = out.len();
    // All candidates produced here share one token transition and one end.
    let cheaper = |out: &Vec<Candidate>, bound| only.is_none() && out[start..].iter().any(|c| c.cost < cost(bound));
    let branch = |rank| only.is_none() || only == Some(rank);
    let has_digit = scan.digit;
    let has_vulgar = scan.vulgar;
    let has_power = scan.power;
    let has_space = scan.space;
    let has_currency = has_digit && scan.currency;
    let has_date_separator = has_digit && scan.date;
    let has_clock_separator = has_digit && scan.clock;
    let has_unit = (has_digit || has_vulgar) && scan.unit;
    if branch(0) {
        for t in other::whitelist(s) {
            emit(out, end, t, 1.01, 0);
        }
    }
    // Whitelist is 1.01; even a two-endpoint money range costs at least 1.06.
    if cheaper(out, 1.06) {
        return;
    }
    if branch(1000)
        && !has_digit
        && s.as_bytes().last().is_some_and(|&c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'D' | b'M'))
    {
        for t in other::roman(s) {
            emit(out, end, t, 1.08, 1000);
        }
    }
    if cheaper(out, 1.09) && !has_digit {
        return;
    }
    if has_digit {
        if has_clock_separator && branch(2000) {
            for t in calendar::time(s) {
                emit(out, end, t, 1.09, 2000);
            }
        }
        if has_date_separator && branch(3000) {
            for t in calendar::date(s) {
                emit(out, end, t, 1.09, 3000);
            }
        }
        if has_currency && branch(4000) {
            for (t, w) in quantity::money(s) {
                emit(out, end, t, 1.09 + w, 4000);
            }
        }
        if has_currency && branch(5000) {
            for dash in ['-', '–'] {
                if let Some((a, b)) = s.split_once(dash) {
                    for (first, wa) in quantity::money(a.strip_suffix(' ').unwrap_or(a)) {
                        for (second, wb) in quantity::money(b.strip_prefix(' ').unwrap_or(b)) {
                            if first.contains("negative:") || second.contains("negative:") {
                                continue;
                            }
                            for word in ["hingga", "sampai"] {
                                let tag = if lazy::recognizing() {
                                    String::new()
                                } else {
                                    format!("{first} }} tokens {{ name: \"{word}\" }} tokens {{ {second}")
                                };
                                emit(out, end, tag, 1.08 + wa + wb, 5000);
                            }
                        }
                    }
                }
            }
        }
        // Time/date cost 1.09, money at least 1.08, and repeated money at
        // least 1.06. All remaining numeric unions have minimum cost 1.1.
        if cheaper(out, 1.1) {
            return;
        }
        if branch(7000) {
            if lazy::recognizing() {
                numeric::cardinal_recognize(s, |w| emit(out, end, String::new(), 1.1 + w, 7000));
            } else {
                for (t, w) in numeric::cardinal_tags(s) {
                    emit(out, end, t, 1.1 + w, 7000);
                }
            }
        }
        if branch(9000) {
            if let Some(t) = numeric::ordinal(s) {
                emit(out, end, t, 1.1, 9000);
            }
        }
        if (scan.decimal || s.as_bytes().last().is_some_and(u8::is_ascii_alphabetic)) && branch(10000) {
            if lazy::recognizing() {
                numeric::decimal_recognize(s, |w| emit(out, end, String::new(), 1.1 + w, 10000));
            } else {
                for (t, w) in numeric::decimal_tags(s) {
                    emit(out, end, t, 1.1 + w, 10000);
                }
            }
        }
        if (s.starts_with('0') || s.starts_with("(0") || s.starts_with('+')) && branch(11000) {
            if let Some(t) = other::telephone(s) {
                emit(out, end, t, 1.1, 11000);
            }
        }
    }
    if has_digit || has_vulgar {
        if has_unit && !cheaper(out, 1.11) && s.chars().last().is_some_and(quantity::possible_end) && branch(6000) {
            for (t, w) in quantity::measure(s) {
                emit(out, end, t, 1.1 + w, 6000);
            }
        }
        if (has_vulgar || s.contains(['/', '⁄'])) && branch(8000) {
            if lazy::recognizing() {
                numeric::fraction_recognize(s, |w| emit(out, end, String::new(), 1.1 + w, 8000));
            } else {
                for (t, w) in numeric::fraction_tags(s) {
                    emit(out, end, t, 1.1 + w, 8000);
                }
            }
        }
    }
    // Electronic/measure start at 1.11; serial starts at 2, word at 100.
    // Equal 1.1 paths above have all been retained.
    if cheaper(out, 1.11) {
        return;
    }
    if !has_space && (s.contains('.') || s.starts_with("http://") || s.starts_with("https://")) && branch(12000) {
        if let Some(t) = other::electronic(s) {
            emit(out, end, t, 1.11, 12000);
        }
    }
    if cheaper(out, 2.0) {
        return;
    }
    if (has_digit || has_power) && branch(13000) {
        for (t, w) in serial::serial(s) {
            emit(out, end, t, 2.0 + w, 13000);
        }
    }
    if cheaper(out, 100.0) {
        return;
    }
    if !has_space && branch(14000) {
        let tag = if lazy::recognizing() { String::new() } else { format!("name: \"{s}\"") };
        emit(out, end, tag, 100.0, 14000);
    }
}

thread_local! {
    static SCAN_BUFFER: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !possible_token_start(text, start) || start > 0 && !boundary(text[start - 1]) {
            return;
        }
        SCAN_BUFFER.with_borrow_mut(|s| {
            s.clear();
            let mut scan = Scan::default();
            lazy::recognize(|| {
                for end in start + 1..=text.len() {
                    let c = text[end - 1];
                    if is_nemo_space(c) && !can_span_space(s.rsplit(is_nemo_space).next().unwrap_or("")) {
                        break;
                    }
                    s.push(c);
                    scan.push(c);
                    if !possible_token_end(text, end) || end < text.len() && !boundary(text[end]) {
                        continue;
                    }
                    let begin = out.len();
                    classify(s.as_str(), end, out, None, &scan);
                    for candidate in &mut out[begin..] {
                        candidate.tag = String::new();
                    }
                }
            });
        });
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.rank == 14000 {
            let mut tag =
                String::with_capacity(8 + text[start..candidate.end].iter().map(|c| c.len_utf8()).sum::<usize>());
            tag.push_str("name: \"");
            tag.extend(text[start..candidate.end].iter());
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let s: String = text[start..candidate.end].iter().collect();
        if candidate.rank == 7000 && candidate.cost == cost(1.1) {
            if let Some(tag) = numeric::cardinal_direct(&s) {
                candidate.tag = tag;
                return;
            }
        }
        if candidate.rank == 10000 {
            if let Some(tag) = numeric::decimal_direct(&s, candidate.cost) {
                candidate.tag = tag;
                return;
            }
        }
        let mut scan = Scan::default();
        for c in s.chars() {
            scan.push(c);
        }
        let mut found = Vec::new();
        classify(&s, candidate.end, &mut found, Some(candidate.rank), &scan);
        if let Some(item) = found.into_iter().find(|item| item.cost == candidate.cost && item.rank == candidate.rank) {
            candidate.tag = item.tag;
        }
    }
}
static GRAMMAR: LazyLock<Malay> = LazyLock::new(|| Malay { classifiers: vec![Box::new(Union)] });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

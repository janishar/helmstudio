//! Indonesian text normalization grammar.
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

struct Indonesian {
    classifiers: Vec<Box<dyn Classifier>>,
}

impl Grammar for Indonesian {
    fn code(&self) -> &'static str {
        "id"
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
                if class == "money" {
                    if let [(first, Value::Str(a)), (second, Value::Str(b))] = fields.as_slice() {
                        // With no preserve_order field, both two-field permutations are allowed.
                        let values = match (first.as_ref(), second.as_ref()) {
                            ("integer_part", "currency_maj") => Some((a, b)),
                            ("currency_maj", "integer_part") => Some((b, a)),
                            _ => None,
                        };
                        if let Some((integer, currency)) = values {
                            if !integer.is_empty() && !currency.is_empty() {
                                for (i, value) in [integer, currency].into_iter().enumerate() {
                                    if i > 0 {
                                        out.push(' ');
                                    }
                                    for c in value.chars() {
                                        out.push(if c == '\u{a0}' { ' ' } else { c });
                                    }
                                }
                                continue;
                            }
                        }
                    }
                }
                if class == "time" {
                    let clock = match fields.as_slice() {
                        [(h, Value::Str(hour)), (p, Value::True)]
                            if h == "hours" && p == "preserve_order" && !hour.is_empty() =>
                        {
                            Some((hour, None))
                        }
                        [(h, Value::Str(hour)), (m, Value::Str(minute)), (p, Value::True)]
                            if h == "hours"
                                && m == "minutes"
                                && p == "preserve_order"
                                && !hour.is_empty()
                                && !minute.is_empty() =>
                        {
                            Some((hour, Some(minute)))
                        }
                        _ => None,
                    };
                    if let Some((hour, minute)) = clock {
                        out.push_str("pukul ");
                        push_spoken(&mut out, hour);
                        if let Some(minute) = minute {
                            out.push_str(" lewat ");
                            push_spoken(&mut out, minute);
                            out.push_str(" menit");
                        }
                        continue;
                    }
                }
                if class == "measure" {
                    if let [(c, Value::Dict(number)), (u, Value::Str(unit)), (p, Value::True)] = fields.as_slice() {
                        if c == "cardinal" && u == "units" && p == "preserve_order" && !unit.is_empty() {
                            if let [(key, Value::Str(integer))] = number.as_slice() {
                                if key == "integer" && !integer.is_empty() {
                                    push_spoken(&mut out, integer);
                                    out.push(' ');
                                    push_spoken(&mut out, unit);
                                    continue;
                                }
                            }
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

fn push_spoken(out: &mut String, value: &str) {
    out.extend(value.chars().map(|c| if c == '\u{a0}' { ' ' } else { c }));
}

static MARKS: LazyLock<Punctuation> = LazyLock::new(|| Punctuation::en(cost(1.1)));

fn boundary(c: char) -> bool {
    is_nemo_space(c) || MARKS.is_mark(c)
}

fn can_span_space(first: &str) -> bool {
    if first.is_empty() || !first.bytes().all(|b| b.is_ascii_alphabetic()) {
        return true;
    }
    static STARTS: LazyLock<std::collections::HashSet<String>> = LazyLock::new(|| {
        let mut starts = std::collections::HashSet::new();
        for table in [
            include_str!("../../../data/id/whitelist.tsv"),
            include_str!("../../../data/id/money/currency.tsv"),
            include_str!("../../../data/id/months/names.tsv"),
            include_str!("../../../data/id/months/abbr.tsv"),
            include_str!("../../../data/id/numbers/quantities.tsv"),
            include_str!("../../../data/id/money/quantity_abbr.tsv"),
            include_str!("../../../data/id/measure/measurements.tsv"),
            include_str!("../../../data/id/measure/denominators.tsv"),
            include_str!("../../../data/id/time/time_zone.tsv"),
        ] {
            for line in table.lines() {
                if let Some((key, _)) = line.split_once('\t') {
                    for word in key.split(' ') {
                        let w = word.trim_matches(|c: char| !c.is_alphabetic()).to_ascii_lowercase();
                        if !w.is_empty() {
                            starts.insert(w);
                        }
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
            "bagian",
            "jilid",
            "volume",
            "pasal",
            "ayat",
            "lampiran",
            "buku",
            "seri",
            "tahap",
            "fase",
            "tingkat",
            "kelas",
            "golongan",
            "eselon",
            "semester",
            "triwulan",
            "kuartal",
            "sila",
            "amendemen",
            "amandemen",
            "perang",
            "paus",
            "raja",
            "ratu",
            "sultan",
            "sri",
            "kaisar",
            "hamengkubuwono",
            "hamengkubuwana",
            "pakubuwono",
            "pakubuwana",
            "mangkunegara",
            "pakualam",
            "paku",
            "yohanes",
            "benediktus",
            "fransiskus",
            "pius",
            "leo",
            "paulus",
            "gregorius",
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
            "ke",
            "sekitar",
            "kurang",
            "lebih",
        ] {
            starts.insert(word.to_string());
        }
        starts
    });
    let mut stack = [0u8; 32];
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
struct Scan(u16);

impl Scan {
    const DIGIT: u16 = 1;
    const VULGAR: u16 = 1 << 1;
    const POWER: u16 = 1 << 2;
    const SPACE: u16 = 1 << 3;
    const CURRENCY: u16 = 1 << 4;
    const DATE: u16 = 1 << 5;
    const CLOCK: u16 = 1 << 6;
    const UNIT: u16 = 1 << 7;
    const DECIMAL: u16 = 1 << 8;
    const MONEY_START: u16 = 1 << 9;
    const DATE_START: u16 = 1 << 10;

    fn has(&self, flag: u16) -> bool {
        self.0 & flag != 0
    }

    fn push(&mut self, c: char) {
        let mut flags = if c.is_ascii() {
            match c {
                '0'..='9' => Self::DIGIT,
                'a'..='z' | 'A'..='Z' => Self::CURRENCY | Self::DATE | Self::CLOCK | Self::UNIT,
                ' ' | '-' | '/' | '\'' => Self::DATE,
                '.' => Self::DATE | Self::CLOCK | Self::DECIMAL,
                ':' => Self::CLOCK,
                ',' => Self::DECIMAL,
                '$' => Self::CURRENCY,
                '%' => Self::UNIT,
                _ => 0,
            }
        } else {
            let mut flags = Self::CURRENCY | Self::DATE;
            if "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c) {
                flags |= Self::VULGAR;
            }
            if matches!(c, '²' | '³') {
                flags |= Self::POWER;
            }
            if c.is_alphabetic() || matches!(c, '‰' | '°' | '℃' | '℉' | 'Ω') {
                flags |= Self::UNIT;
            }
            flags
        };
        if is_nemo_space(c) {
            flags |= Self::SPACE;
        }
        self.0 |= flags;
    }
}

fn classify(s: &str, end: usize, out: &mut Vec<Candidate>, only: Option<i32>, scan: &Scan) {
    let start = out.len();
    let cheaper = |out: &Vec<Candidate>, bound| only.is_none() && out[start..].iter().any(|c| c.cost < cost(bound));
    let branch = |rank| only.is_none() || only == Some(rank);
    let has_digit = scan.has(Scan::DIGIT);
    let has_vulgar = scan.has(Scan::VULGAR);
    let has_power = scan.has(Scan::POWER);
    let has_space = scan.has(Scan::SPACE);
    let has_currency = has_digit && scan.has(Scan::CURRENCY) && scan.has(Scan::MONEY_START);
    let has_date_separator = has_digit && scan.has(Scan::DATE) && scan.has(Scan::DATE_START);
    let has_clock_separator = has_digit && scan.has(Scan::CLOCK);
    let has_unit = (has_digit || has_vulgar) && scan.has(Scan::UNIT);
    let number_first = if has_digit || has_vulgar { numeric::sign(s).0.chars().next() } else { None };
    let number_start = number_first.is_some_and(|c| c.is_ascii_digit());
    let fraction_start = number_start || number_first.is_some_and(|c| "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c));

    if branch(0) {
        for t in other::whitelist(s) {
            emit(out, end, t, 1.01, 0);
        }
    }
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
        if has_clock_separator && branch(2000) && calendar::time_start(s) {
            calendar::time(s, |tag| emit(out, end, tag, 1.09, 2000));
        }
        if has_date_separator && branch(3000) && calendar::date_start(s) {
            for t in calendar::date(s) {
                emit(out, end, t, 1.09, 3000);
            }
        }
        if has_currency && branch(4000) {
            quantity::money(s, |tag, weight| emit(out, end, tag, 1.09 + weight, 4000));
        }
        if has_currency && branch(5000) {
            for dash in ['-', '–'] {
                if let Some((a, b)) = s.split_once(dash) {
                    let a = a.strip_suffix(' ').unwrap_or(a);
                    let b = b.strip_prefix(' ').unwrap_or(b);
                    if a.starts_with(['-', '−', '~', '±']) || b.starts_with(['-', '−', '~', '±']) {
                        continue;
                    }
                    quantity::money(a, |first, wa| {
                        quantity::money(b, |second, wb| {
                            let tag = if lazy::recognizing() {
                                String::new()
                            } else {
                                format!("{first} }} tokens {{ name: \"sampai\" }} tokens {{ {second}")
                            };
                            emit(out, end, tag, 1.08 + wa + wb, 5000);
                        });
                    });
                }
            }
        }
        if cheaper(out, 1.1) {
            return;
        }
        if number_start && s.as_bytes().last().is_some_and(u8::is_ascii_digit) && branch(7000) {
            if lazy::recognizing() {
                numeric::cardinal_recognize(s, |w| emit(out, end, String::new(), 1.1 + w, 7000));
            } else {
                for (t, w) in numeric::cardinal_tags(s) {
                    emit(out, end, t, 1.1 + w, 7000);
                }
            }
        }
        if (s.starts_with("ke") || s.starts_with("Ke")) && branch(9000) {
            if lazy::recognizing() {
                numeric::ordinal_recognize(s, |w| emit(out, end, String::new(), 1.1 + w, 9000));
            } else {
                for (t, w) in numeric::ordinal_tags(s) {
                    emit(out, end, t, 1.1 + w, 9000);
                }
            }
        }
        if number_start
            && (scan.has(Scan::DECIMAL) || s.as_bytes().last().is_some_and(u8::is_ascii_alphabetic))
            && branch(10000)
        {
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
            quantity::measure(s, |tag, weight| emit(out, end, tag, 1.1 + weight, 6000));
        }
        if fraction_start && (has_vulgar || s.contains(['/', '⁄'])) && branch(8000) {
            if lazy::recognizing() {
                numeric::fraction_recognize(s, |w| emit(out, end, String::new(), 1.1 + w, 8000));
            } else {
                for (t, w) in numeric::fraction_tags(s) {
                    emit(out, end, t, 1.1 + w, 8000);
                }
            }
        }
    }
    if cheaper(out, 1.11) {
        return;
    }
    if !has_space
        && (s.contains('.') || s.starts_with("http://") || s.starts_with("https://") || s.contains('@'))
        && branch(12000)
    {
        if let Some(t) = other::electronic(s) {
            emit(out, end, t, 1.11, 12000);
        }
    }
    if cheaper(out, 2.0) {
        return;
    }
    if !has_space && (has_digit || has_power) && branch(13000) {
        if let Some((t, w)) = serial::serial(s) {
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
            let mut scan = Scan(Scan::DATE_START);
            if quantity::money_start_at(text, start) {
                scan.0 |= Scan::MONEY_START;
            }
            lazy::recognize(|| {
                for end in start + 1..=text.len() {
                    let c = text[end - 1];
                    if is_nemo_space(c) && !can_span_space(s.rsplit(is_nemo_space).next().unwrap_or("")) {
                        break;
                    }
                    if is_nemo_space(c) && !scan.has(Scan::SPACE) && !calendar::date_word_start(s) {
                        scan.0 &= !Scan::DATE_START;
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
        if !candidate.tag.is_empty() {
            return;
        }
        if candidate.rank == 14000 {
            let mut tag =
                String::with_capacity(8 + text[start..candidate.end].iter().map(|c| c.len_utf8()).sum::<usize>());
            tag.push_str("name: \"");
            tag.extend(text[start..candidate.end].iter());
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        SCAN_BUFFER.with_borrow_mut(|s| {
            s.clear();
            s.extend(text[start..candidate.end].iter());
            if candidate.rank == 6000 && candidate.cost == cost(1.1 + 0.01) {
                if let Some(tag) = quantity::selected_grouped_measure(s) {
                    candidate.tag = tag;
                    return;
                }
            }
            if candidate.rank == 2000 && candidate.cost == cost(1.09) {
                if let Some(tag) = calendar::selected_time(s) {
                    candidate.tag = tag;
                    return;
                }
            }
            if candidate.rank == 4000 {
                if let Some(tag) = quantity::selected_grouped_money(s, candidate.cost) {
                    candidate.tag = tag;
                    return;
                }
            }
            if candidate.rank == 7000 && candidate.cost == cost(1.1) {
                if let Some(tag) = numeric::cardinal_direct(s) {
                    candidate.tag = tag;
                    return;
                }
            }
            if candidate.rank == 10000 {
                if let Some(tag) = numeric::decimal_direct(s, candidate.cost) {
                    candidate.tag = tag;
                    return;
                }
            }
            let mut scan = Scan(Scan::DATE_START);
            if quantity::money_start_at(text, start) {
                scan.0 |= Scan::MONEY_START;
            }
            for c in s.chars() {
                scan.push(c);
            }
            let mut found = Vec::new();
            classify(s, candidate.end, &mut found, Some(candidate.rank), &scan);
            let item = found
                .into_iter()
                .find(|item| item.cost == candidate.cost && item.rank == candidate.rank)
                .expect("selected Indonesian graph path");
            candidate.tag = item.tag;
        });
    }
}

static GRAMMAR: LazyLock<Indonesian> = LazyLock::new(|| Indonesian { classifiers: vec![Box::new(Union)] });

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

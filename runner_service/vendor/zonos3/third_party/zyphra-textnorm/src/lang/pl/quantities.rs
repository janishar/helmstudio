// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::number_names::{Case, Gender};
use super::numeric::{decimal, fraction, grouped, signed};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;
struct CharRows {
    ascii: [Vec<usize>; 128],
    other: HashMap<char, Vec<usize>>,
}
impl CharRows {
    fn new() -> Self {
        Self { ascii: std::array::from_fn(|_| Vec::new()), other: HashMap::new() }
    }
    fn insert(&mut self, c: char, row: usize) {
        if c.is_ascii() {
            self.ascii[c as usize].push(row);
        } else {
            self.other.entry(c).or_default().push(row);
        }
    }
    fn get(&self, c: char) -> &[usize] {
        if c.is_ascii() { &self.ascii[c as usize] } else { self.other.get(&c).map(Vec::as_slice).unwrap_or(&[]) }
    }
}
static UNITS: LazyLock<Vec<Vec<&'static str>>> = LazyLock::new(|| {
    include_str!("../../../data/pl/measure/measurements.tsv")
        .lines()
        .map(|line| line.split('\t').collect::<Vec<_>>())
        .filter(|row| row.len() >= 4)
        .collect()
});
static UNITS_BY_END: LazyLock<CharRows> = LazyLock::new(|| {
    let mut index = CharRows::new();
    for (i, row) in UNITS.iter().enumerate() {
        if let Some(c) = row[0].chars().last() {
            index.insert(c, i);
        }
    }
    index
});
fn unit_rows(s: &str) -> impl Iterator<Item = &'static Vec<&'static str>> {
    s.chars().last().into_iter().flat_map(|c| UNITS_BY_END.get(c)).map(|&i| &UNITS[i])
}
static CURRENCIES: LazyLock<Vec<Vec<&'static str>>> = LazyLock::new(|| {
    include_str!("../../../data/pl/money/currency.tsv")
        .lines()
        .map(|line| line.split('\t').collect::<Vec<_>>())
        .filter(|row| row.len() >= 7)
        .collect()
});
struct CurrencyIndex {
    first: CharRows,
    last: CharRows,
}
static CURRENCY_INDEX: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let mut index = CurrencyIndex { first: CharRows::new(), last: CharRows::new() };
    for (i, row) in CURRENCIES.iter().enumerate() {
        if let Some(c) = row[0].chars().next() {
            index.first.insert(c, i);
        }
        if let Some(c) = row[0].chars().last() {
            index.last.insert(c, i);
        }
    }
    index
});

// Only a leading or trailing symbol can contribute an outer currency row.
// Merge the two index lists in source order to retain every equal-cost rank.
fn currency_rows(body: &str) -> impl Iterator<Item = &'static Vec<&'static str>> {
    let first = body.chars().next().map(|c| CURRENCY_INDEX.first.get(c)).unwrap_or(&[]);
    let last = body.chars().last().map(|c| CURRENCY_INDEX.last.get(c)).unwrap_or(&[]);
    let (mut a, mut b) = (0, 0);
    std::iter::from_fn(move || {
        let x = first.get(a).copied();
        let y = last.get(b).copied();
        let i = match (x, y) {
            (None, None) => return None,
            (Some(i), None) => {
                a += 1;
                i
            }
            (None, Some(i)) => {
                b += 1;
                i
            }
            (Some(i), Some(j)) if i < j => {
                a += 1;
                i
            }
            (Some(i), Some(j)) if j < i => {
                b += 1;
                j
            }
            (Some(i), Some(_)) => {
                a += 1;
                b += 1;
                i
            }
        };
        Some(&CURRENCIES[i])
    })
}

/// Only currency symbols, the source's magnitude shorthands, or a rate
/// suffix can contain alphabetic input in a money expression.
pub fn money_alpha_prefix(tail: &str, after_slash: bool) -> bool {
    if after_slash {
        return ["h", "godz.", "godz", "kg", "m", "s", "dzień", "miesiąc", "rok", "os.", "osobę", "szt.", "sztukę"]
            .iter()
            .any(|rate| rate.starts_with(tail));
    }
    if let Some(c) = tail.chars().next() {
        for &i in CURRENCY_INDEX.first.get(c) {
            let symbol = CURRENCIES[i][0];
            if symbol.starts_with(tail) || tail.starts_with(symbol) {
                return true;
            }
        }
    }
    for mag in ["k", "tys.", "tys", "M", "mln.", "mln", "B", "mld.", "mld"] {
        if mag.starts_with(tail) {
            return true;
        }
        if let Some(rest) = tail.strip_prefix(mag) {
            if rest.is_empty()
                || rest.starts_with(' ')
                || rest.starts_with('/')
                || rest.chars().next().is_some_and(currency_start)
            {
                return true;
            }
        }
    }
    false
}
static UNIT_PREFIX: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for row in UNITS.iter() {
        let symbol = row[0];
        for (start, c) in symbol.char_indices() {
            if !c.is_alphabetic() {
                continue;
            }
            for end in (start + 1..=symbol.len()).filter(|&i| symbol.is_char_boundary(i)) {
                prefixes.insert(&symbol[start..end]);
            }
        }
    }
    prefixes
});
pub fn unit_prefix(s: &str) -> bool {
    UNIT_PREFIX.contains(s)
}
pub fn currency_start(c: char) -> bool {
    !CURRENCY_INDEX.first.get(c).is_empty()
}
pub fn currency_end(c: char) -> bool {
    c == '¢' || !CURRENCY_INDEX.last.get(c).is_empty()
}
pub fn unit_end(c: char) -> bool {
    !UNITS_BY_END.get(c).is_empty()
}
// Recognize the productive cardinal subset without generating Polish words.
fn integer(s: &str) -> bool {
    if s.is_empty() {
        return false;
    }
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return s.len() <= 12 && (s == "0" || !s.starts_with('0'));
    }
    if s.contains('.') && (s.contains(' ') || s.contains('\u{a0}')) {
        return false;
    }
    let mut groups = s.split([' ', '\u{a0}', '.']);
    let Some(first) = groups.next() else {
        return false;
    };
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return false;
    }
    let mut count = 0;
    for group in groups {
        if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
        count += 1;
    }
    (1..=3).contains(&count)
}

pub fn simple_measure(s: &str) -> bool {
    unit_rows(s).any(|row| {
        let sym = row[0];
        let Some(raw) = s.strip_suffix(sym) else {
            return false;
        };
        if sym == "K" && !raw.ends_with(' ') {
            return false;
        }
        let (body, _) = signed(strip_space(raw));
        integer(body)
    })
}

pub fn simple_money(s: &str) -> bool {
    let (body, _) = signed(s);
    let (body, rate) = rates(body);
    if let Some(n) = body.strip_suffix('¢') {
        if rate.is_empty() && integer(n) {
            return true;
        }
    }
    currency_rows(body).any(|row| {
        let sym = row[0];
        body.strip_prefix(sym).map(|n| n.strip_prefix(' ').unwrap_or(n)).is_some_and(integer)
            || body.strip_suffix(sym).map(strip_space).is_some_and(integer)
    })
}
const M: Gender = Gender::Masculine;
const F: Gender = Gender::Feminine;
const N: Case = Case::Nominative;
const G: Case = Case::Genitive;
fn form(s: &str) -> usize {
    if s == "1" {
        0
    } else {
        let b = s.as_bytes();
        if let Some(&last) = b.last() {
            if (b'2'..=b'4').contains(&last) && (b.len() == 1 || b[b.len() - 2] != b'1') {
                return 1;
            }
        }
        2
    }
}
fn strip_space(s: &str) -> &str {
    s.strip_suffix(' ').unwrap_or(s)
}
fn unit_gender(sym: &str) -> Gender {
    if [
        "s", "sek.", "sek", "min", "min.", "h", "godz.", "godz", "t", "mi", "oz", "ha", "kcal", "cal", "atm", "mAh",
        "Ah", "kWh", "Wh", "MWh",
    ]
    .contains(&sym)
    {
        F
    } else {
        M
    }
}

/// Render the recognized simple whole-number unit path without evaluating
/// decimal, fraction, and range alternatives again.
pub fn simple_measure_words(s: &str) -> Option<String> {
    for row in unit_rows(s) {
        let sym = row[0];
        let Some(raw) = s.strip_suffix(sym) else {
            continue;
        };
        if sym == "K" && !raw.ends_with(' ') {
            continue;
        }
        let (body, sign) = signed(strip_space(raw));
        if !integer(body) {
            continue;
        }
        let n = grouped(body, unit_gender(sym), N, true)?;
        return Some(format!("{sign}{n} {}", row[1 + form(body)]));
    }
    None
}

pub fn simple_money_words(s: &str) -> Option<String> {
    let (body, sign) = signed(s);
    let (body, rate) = rates(body);
    if rate.is_empty() {
        if let Some(n) = body.strip_suffix('¢') {
            if integer(n) {
                return Some(format!("{sign}{} {}", grouped(n, M, N, true)?, ["cent", "centy", "centów"][form(n)]));
            }
        }
    }
    for row in currency_rows(body) {
        let sym = row[0];
        for n in
            [body.strip_prefix(sym).map(|n| n.strip_prefix(' ').unwrap_or(n)), body.strip_suffix(sym).map(strip_space)]
                .into_iter()
                .flatten()
        {
            if integer(n) {
                return Some(format!("{sign}{} {}{rate}", grouped(n, M, N, true)?, row[1 + form(n)]));
            }
        }
    }
    None
}
pub fn measure(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for row in unit_rows(s) {
        let sym = row[0];
        let Some(raw) = s.strip_suffix(sym) else {
            continue;
        };
        if sym == "K" && !raw.ends_with(' ') {
            continue;
        }
        let raw = strip_space(raw);
        let (body, sign) = signed(raw);
        let gender = unit_gender(sym);
        if let Some(n) = grouped(body, gender, N, true) {
            out.push((format!("{sign}{n} {}", row[1 + form(body)]), 0.0));
        }
        for (n, c) in decimal(body) {
            out.push((format!("{sign}{n} {}", row[3]), c));
        }
        for (n, c) in fraction(body) {
            out.push((format!("{sign}{n} {}", row[3]), c));
        }
        for d in ['-', '–', '—'] {
            if let Some((a, b)) = raw.split_once(d) {
                if let (Some(a), Some(b)) = (grouped(a, M, N, true), grouped(b, M, G, true)) {
                    out.push((format!("{a} do {b} {}", row[3]), 0.0));
                    out.push((format!("od {a} do {b} {}", row[3]), 0.0));
                }
            }
        }
    }
    out
}
fn amounts(s: &str, row: &[&str]) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(w) = grouped(s, M, N, true) {
        out.push((format!("{w} {}", row[1 + form(s)]), 0.0));
    }
    for (suf, mag) in [
        ("k", "tysięcy"),
        (" tys.", "tysięcy"),
        (" tys", "tysięcy"),
        ("M", "miliona"),
        (" mln.", "miliona"),
        (" mln", "miliona"),
        ("B", "miliarda"),
        (" mld.", "miliarda"),
        (" mld", "miliarda"),
    ] {
        if let Some(n) = s.strip_suffix(suf) {
            if let Some(n) = grouped(n, M, N, true) {
                out.push((format!("{n} {mag} {}", row[3]), 0.0));
            }
            if mag != "tysięcy" {
                for (n, c) in decimal(n) {
                    out.push((format!("{n} {mag} {}", row[3]), c));
                }
            }
        }
    }
    for sep in [',', '.'] {
        for (idx, c) in s.char_indices() {
            if c != sep {
                continue;
            }
            let a = &s[..idx];
            let b = &s[idx + 1..];
            if b.len() != 2 || !b.bytes().all(|x| x.is_ascii_digit()) {
                continue;
            }
            let b = b.strip_prefix('0').unwrap_or(b);
            let Some(bword) = grouped(b, M, N, true) else {
                continue;
            };
            let minor = format!("{bword} {}", row[4 + form(b)]);
            if a == "0" || a == "00" {
                out.push((minor, -0.2));
                continue;
            }
            if a.starts_with('0') {
                continue;
            }
            if let Some(major) = grouped(a, M, N, true) {
                let major = format!("{major} {}", row[1 + form(a)]);
                if b == "0" {
                    out.push((major.clone(), -0.1));
                }
                out.push((format!("{major} {minor}"), 0.0));
            }
        }
    }
    out
}
fn rates(s: &str) -> (&str, &'static str) {
    if !s.as_bytes().contains(&b'/') {
        return (s, "");
    }
    for (key, val) in [
        ("/h", " za godzinę"),
        ("/godz.", " za godzinę"),
        ("/godz", " za godzinę"),
        ("/kg", " za kilogram"),
        ("/m", " za metr"),
        ("/s", " za sekundę"),
        ("/dzień", " za dzień"),
        ("/miesiąc", " za miesiąc"),
        ("/rok", " za rok"),
        ("/os.", " za osobę"),
        ("/osobę", " za osobę"),
        ("/szt.", " za sztukę"),
        ("/sztukę", " za sztukę"),
    ] {
        if let Some(s) = s.strip_suffix(key) {
            return (s, val);
        }
    }
    (s, "")
}
pub fn money(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (body, sign) = signed(s);
    let (body, rate) = rates(body);
    if let Some(n) = body.strip_suffix('¢') {
        if let Some(w) = grouped(n, M, N, true) {
            out.push((format!("{sign}{w} {}", ["cent", "centy", "centów"][form(n)]), 0.0));
        }
    }
    let ranges =
        if sign.is_empty() && rate.is_empty() { ['-', '–', '—'].map(|d| body.split_once(d)) } else { [None; 3] };
    for row in currency_rows(body) {
        let sym = row[0];
        for n in
            [body.strip_prefix(sym).map(|v| v.strip_prefix(' ').unwrap_or(v)), body.strip_suffix(sym).map(strip_space)]
                .into_iter()
                .flatten()
        {
            for (w, c) in amounts(n, row) {
                out.push((format!("{sign}{w}{rate}"), c));
            }
        }
        for (a, b) in ranges.into_iter().flatten() {
            if let Some(n) = b.strip_suffix(sym).map(strip_space) {
                if let (Some(a), Some(b)) = (grouped(a, M, N, true), grouped(n, M, G, true)) {
                    out.push((format!("{a} do {b} {}", row[3]), 0.0));
                }
            }
            if let (Some(a), Some(b)) = (a.strip_prefix(sym), b.strip_prefix(sym)) {
                let a = a.strip_prefix(' ').unwrap_or(a);
                let b = b.strip_prefix(' ').unwrap_or(b);
                if let (Some(a), Some(b)) = (grouped(a, M, N, true), grouped(b, M, G, true)) {
                    out.push((format!("{a} do {b} {}", row[3]), 0.0));
                }
            }
            if let Some(a) = a.strip_prefix(sym) {
                let a = a.strip_prefix(' ').unwrap_or(a);
                for r2 in CURRENCIES.iter() {
                    if let Some(b) = b.strip_prefix(r2[0]) {
                        let b = b.strip_prefix(' ').unwrap_or(b);
                        if let Some(b) = grouped(b, M, G, true) {
                            for (a, c) in amounts(a, row) {
                                out.push((format!("{a} do {b} {}", r2[3]), c + 0.1));
                            }
                        }
                    }
                }
            }
        }
    }
    out
}

//! `pt/taggers/date.py`: `15 de março de 2024`, `dd<sep>mm<sep>yyyy`, `mm<sep>dd<sep>yyyy` (+0.05, relabelled to
//! day/month order), `yyyy<sep>mm<sep>dd` (relabelled, its `preserve_order` deleted).

use std::sync::LazyLock;

use super::cardinal;
use crate::lang::pt::graph_utils::SpanTagger;
use crate::text::{Cost, cost, is_digit, is_nemo_space};

crate::labels!(MONTHS, "pt/date/months.tsv");
crate::labels!(SEPARATORS, "pt/date/numeric_separators.tsv");

struct Data {
    /// month number (1-12) → name
    by_number: [String; 13],
    /// day number (2-31) → cardinal words
    day_words: Vec<String>,
    /// written month (lowercase name or its capitalized form) → name
    written: Vec<(String, String)>,
    /// `numeric_separators.tsv` (each a single char)
    separators: Vec<char>,
}

static DATA: LazyLock<Data> = LazyLock::new(|| {
    let mut by_number: [String; 13] = Default::default();
    let mut names: Vec<String> = Vec::new();
    for r in MONTHS.iter().filter(|r| r.len() >= 2) {
        if let Ok(n) = r[0].parse::<usize>() {
            if (1..=12).contains(&n) && by_number[n].is_empty() {
                by_number[n] = r[1].clone();
            }
        }
        if !names.contains(&r[1]) {
            names.push(r[1].clone());
        }
    }
    let mut written = Vec::new();
    for name in names {
        written.push((name.clone(), name.clone()));
        let mut cs = name.chars();
        if let Some(f) = cs.next().filter(|c| c.is_lowercase()) {
            let cap: String = f.to_uppercase().chain(cs).collect();
            written.push((cap, name));
        }
    }
    let day_words = (0..32usize)
        .map(|n| {
            let d: Vec<char> = n.to_string().chars().collect();
            cardinal::graph_digits(&d).map(|(w, _)| w).unwrap_or_default()
        })
        .collect();
    let separators = SEPARATORS
        .iter()
        .filter_map(|r| r.first())
        .map(|s| s.trim())
        .filter(|s| s.chars().count() == 1)
        .filter_map(|s| s.chars().next())
        .collect();
    Data { by_number, day_words, written, separators }
});

/// Value of 1-2 ASCII digits.
fn small(d: &[char]) -> Option<usize> {
    if d.is_empty() || d.len() > 2 || !d.iter().all(|c| is_digit(*c)) {
        return None;
    }
    Some(d.iter().fold(0, |a, &c| a * 10 + (c as usize - '0' as usize)))
}

/// `day_inner` on 1-2 digits (`01`/`1` → primeiro, `02`-`09`, `2`-`9`, `10`-`31`).
fn day(d: &[char]) -> Option<&'static str> {
    let n = small(d)?;
    let ok = match (d.len(), n) {
        (_, 1) => return Some("primeiro"),
        (2, _) if d[0] == '0' => (2..=9).contains(&n),
        (2, _) => (10..=31).contains(&n),
        _ => (2..=9).contains(&n),
    };
    ok.then(|| DATA.day_words[n].as_str())
}

/// `month_digits @ month_to_word`: `10`-`12`, `01`-`09`, `1`-`9`.
fn month(d: &[char]) -> Option<&'static str> {
    let n = small(d)?;
    let ok = match d.len() {
        2 if d[0] == '0' => (1..=9).contains(&n),
        2 => (10..=12).contains(&n),
        _ => (1..=9).contains(&n),
    };
    ok.then(|| DATA.by_number[n].as_str())
}

/// `year_num`: four digits, first non-zero.
fn year(d: &[char]) -> Option<String> {
    if d.len() != 4 || d[0] == '0' || !d.iter().all(|c| is_digit(*c)) {
        return None;
    }
    cardinal::graph_digits(d).map(|(w, _)| w)
}

fn is_digits(d: &[char], lo: usize, hi: usize) -> bool {
    (lo..=hi).contains(&d.len()) && d.iter().all(|c| is_digit(*c))
}

/// `delete_space + "de" + delete_space` at `i`: the index after it.
fn delete_de(s: &[char], mut i: usize) -> Option<usize> {
    while i < s.len() && is_nemo_space(s[i]) {
        i += 1;
    }
    if s.get(i) != Some(&'d') || s.get(i + 1) != Some(&'e') {
        return None;
    }
    i += 2;
    while i < s.len() && is_nemo_space(s[i]) {
        i += 1;
    }
    Some(i)
}

fn text_date(s: &[char]) -> Option<String> {
    let nd = s.iter().take_while(|c| is_digit(**c)).count();
    let dw = day(&s[..nd])?;
    let i = delete_de(s, nd)?;
    for (written, name) in &DATA.written {
        let Some(len) = crate::text::match_str(s, i, written) else { continue };
        let Some(j) = delete_de(s, i + len) else { continue };
        if let Some(yw) = year(&s[j..]) {
            return Some(format!("date {{ day: \"{dw}\"month: \"{name}\"year: \"{yw}\" preserve_order: true }}"));
        }
    }
    None
}

fn numeric(s: &[char]) -> Option<(Cost, String)> {
    for &sc in &DATA.separators {
        let mut parts = s.split(|&c| c == sc);
        let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) else {
            continue;
        };
        if is_digits(a, 1, 2) && is_digits(b, 1, 2) {
            // dmy (0) before mdy (+0.05): the same tag either way
            let dm = match (day(a), month(b)) {
                (Some(dw), Some(mw)) => Some((0, dw, mw)),
                _ => month(a).zip(day(b)).map(|(mw, dw)| (cost(0.05), dw, mw)),
            };
            if let Some((cst, dw, mw)) = dm {
                if let Some(yw) = year(c) {
                    let mut t = String::with_capacity(dw.len() + mw.len() + yw.len() + 64);
                    t.push_str("date { day: \"");
                    t.push_str(dw);
                    t.push_str("\" month: \"");
                    t.push_str(mw);
                    t.push_str("\" year: \"");
                    t.push_str(&yw);
                    t.push_str("\" preserve_order: true }");
                    return Some((cst, t));
                }
            }
        }
        if is_digits(b, 1, 2) && is_digits(c, 1, 2) {
            if let (Some(yw), Some(mw), Some(dw)) = (year(a), month(b), day(c)) {
                return Some((0, format!("date {{ day: \"{dw}\" month: \"{mw}\" year: \"{yw}\" }}")));
            }
        }
    }
    None
}

pub struct Date;

impl SpanTagger for Date {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) { 5 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        if self.max_words(text[start]) == 0 {
            return 0;
        }
        // `<day> de <month> de <year>` (the only multi-word form)
        let next = start + super::word_len(text, start);
        let mut i = next;
        while i < text.len() && is_nemo_space(text[i]) {
            i += 1;
        }
        if crate::text::starts_with(text, i, "de") { 5 } else { 1 }
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        if let Some(t) = text_date(span) {
            return Some((0, t));
        }
        numeric(span)
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Shared Vietnamese normalization helpers and exact number transduction.

use crate::text::{Cost, cost};

/// Scratch storage for the source cardinal graph's at-most-21-digit domain.
pub struct Digits {
    bytes: [u8; 21],
    len: usize,
}

impl Digits {
    pub fn new() -> Self {
        Self { bytes: [0; 21], len: 0 }
    }
    pub fn push(&mut self, digit: u8) {
        self.bytes[self.len] = digit;
        self.len += 1;
    }
}

impl std::ops::Deref for Digits {
    type Target = [u8];
    fn deref(&self) -> &[u8] {
        &self.bytes[..self.len]
    }
}

/// A number path whose spoken form is written only when its containing tag wins.
pub struct SpokenNumber<'a> {
    pub digits: &'a [u8],
    pub grouped: bool,
}

impl std::fmt::Display for SpokenNumber<'_> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        if !self.grouped {
            fn two(f: &mut std::fmt::Formatter<'_>, t: u8, u: u8) -> std::fmt::Result {
                if t == b'1' {
                    return f.write_str(TEEN_WORDS[(u - b'0') as usize]);
                }
                f.write_str(TIES_WORDS[(t - b'0') as usize])?;
                if u != b'0' {
                    f.write_str(" ")?;
                    f.write_str(special_digit_word(u))?;
                }
                Ok(())
            }
            match self.digits {
                [d] => return f.write_str(digit_word(*d)),
                [t, u] => return two(f, *t, *u),
                [h, t, u] => {
                    if *h == b'0' {
                        return if *t == b'0' { f.write_str(digit_word(*u)) } else { two(f, *t, *u) };
                    }
                    f.write_str(digit_word(*h))?;
                    f.write_str(" trăm")?;
                    if *t == b'0' && *u != b'0' {
                        f.write_str(" linh ")?;
                        f.write_str(linh_digit_word(*u))?;
                    } else if *t != b'0' {
                        f.write_str(" ")?;
                        two(f, *t, *u)?;
                    }
                    return Ok(());
                }
                _ => {}
            }
        }
        let mut spoken = String::new();
        if self.grouped {
            spell_dot_digits(self.digits, &mut spoken);
        } else {
            spell_cardinal_digits(self.digits, &mut spoken);
        }
        f.write_str(&spoken)
    }
}

pub struct SpokenFraction<'a>(pub &'a [char]);

impl std::fmt::Display for SpokenFraction<'_> {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        for (index, &digit) in self.0.iter().enumerate() {
            if index != 0 {
                f.write_str(" ")?;
            }
            f.write_str(digit_word(digit as u8))?;
        }
        Ok(())
    }
}

pub const WEIGHT_CARDINAL: Cost = cost(1.1);
pub const WEIGHT_DECIMAL: Cost = cost(1.1);
pub const WEIGHT_FRACTION: Cost = cost(1.1);
pub const WEIGHT_DATE: Cost = cost(1.1);
pub const WEIGHT_TIME: Cost = cost(1.1);
pub const WEIGHT_MONEY: Cost = cost(1.1);
pub const WEIGHT_MEASURE: Cost = cost(1.1);
pub const WEIGHT_RANGE: Cost = cost(1.1);
pub const WEIGHT_ORDINAL: Cost = cost(1.1);
pub const WEIGHT_WHITELIST: Cost = cost(1.01);
pub const WEIGHT_APPROX: Cost = cost(1.05);
pub const WEIGHT_POWER: Cost = cost(1.05);
pub const WEIGHT_TELEPHONE: Cost = cost(1.05);
pub const WEIGHT_ELECTRONIC: Cost = cost(1.11);
pub const WEIGHT_WORD: Cost = cost(100.0);
pub const WEIGHT_ROMAN: Cost = cost(101.0);
pub const WEIGHT_PUNCT: Cost = cost(2.1);

pub const DIGIT_WORDS: [&str; 10] = ["không", "một", "hai", "ba", "bốn", "năm", "sáu", "bảy", "tám", "chín"];

pub const TIES_WORDS: [&str; 10] =
    ["", "", "hai mươi", "ba mươi", "bốn mươi", "năm mươi", "sáu mươi", "bảy mươi", "tám mươi", "chín mươi"];

pub const TEEN_WORDS: [&str; 10] = [
    "mười",
    "mười một",
    "mười hai",
    "mười ba",
    "mười bốn",
    "mười lăm",
    "mười sáu",
    "mười bảy",
    "mười tám",
    "mười chín",
];

#[inline]
pub fn digit_word(d: u8) -> &'static str {
    DIGIT_WORDS[(d - b'0') as usize]
}

#[inline]
pub fn linh_digit_word(d: u8) -> &'static str {
    // digit_special: 1 -> std "một", 4 -> std "bốn", 5 -> std "năm"
    digit_word(d)
}

#[inline]
pub fn special_digit_word(d: u8) -> &'static str {
    match d {
        b'1' => "mốt",
        b'4' => "tư",
        b'5' => "lăm",
        _ => digit_word(d),
    }
}

pub fn spell_two_digits(t: u8, u: u8, out: &mut String) {
    if t == b'1' {
        out.push_str(TEEN_WORDS[(u - b'0') as usize]);
    } else if u == b'0' {
        out.push_str(TIES_WORDS[(t - b'0') as usize]);
    } else {
        out.push_str(TIES_WORDS[(t - b'0') as usize]);
        out.push(' ');
        out.push_str(special_digit_word(u));
    }
}

pub fn spell_hundreds_triplet(h: u8, t: u8, u: u8, out: &mut String) {
    if h == b'0' {
        if t == b'0' {
            out.push_str(digit_word(u));
        } else {
            spell_two_digits(t, u, out);
        }
    } else {
        out.push_str(digit_word(h));
        out.push_str(" trăm");
        if t == b'0' && u == b'0' {
            // exact hundred
        } else if t == b'0' {
            out.push_str(" linh ");
            out.push_str(linh_digit_word(u));
        } else {
            out.push(' ');
            spell_two_digits(t, u, out);
        }
    }
}

// Cost-only counterparts of the cardinal transducers. Recognition must not
// construct the spoken form for each prefix in the lattice.
fn small_cost(d: &[u8], zero: bool) -> Option<Cost> {
    match d {
        [a] if *a != b'0' || zero => Some(0),
        [a, _] if *a != b'0' => Some(0),
        [a, b, c] if *a != b'0' || *b != b'0' || *c != b'0' => Some(0),
        _ => None,
    }
}

fn magnitude_cost(d: &[u8]) -> Option<Cost> {
    if !(4..=21).contains(&d.len()) {
        return None;
    }
    let level = (d.len() - 1) / 3;
    let (leading, tail) = d.split_at(d.len() - level * 3);
    small_cost(leading, false)?;
    if tail.iter().all(|&x| x == b'0') {
        return Some(0);
    }
    let mut best = if level > 1 && level <= 4 { magnitude_cost(tail) } else { None };
    let zeros = tail.iter().take_while(|&&x| x == b'0').count();
    let remaining = &tail[zeros..];
    if remaining.len() <= 3 {
        let c = if remaining.len() == 1 { Some(cost(-0.1)) } else { small_cost(remaining, false) };
        if let Some(c) = c {
            best = Some(best.map_or(c, |old| old.min(c)));
        }
    }
    best
}

fn split_cost(d: &[u8]) -> Option<Cost> {
    let (front, back) = d.split_at(d.len().checked_sub(9)?);
    let c = magnitude_cost(front)?;
    if back.iter().all(|&x| x == b'0') { Some(c) } else { Some(c + magnitude_cost(back)?) }
}

pub fn cardinal_cost(d: &[u8]) -> Option<Cost> {
    if d.is_empty() || d.len() > 21 || !d.iter().all(u8::is_ascii_digit) {
        return None;
    }
    if d.len() <= 3 {
        return small_cost(d, true);
    }
    let mut best = magnitude_cost(d);
    if d.len() > 12 {
        if let Some(c) = split_cost(d) {
            let c = c + cost(-0.5);
            best = Some(best.map_or(c, |old| old.min(c)));
        }
    }
    best
}

pub fn dot_cost(d: &[u8]) -> Option<Cost> {
    if d.len() <= 12 { Some(magnitude_cost(d)? + cost(-0.3)) } else { Some(split_cost(d)? + cost(-1.0)) }
}

/// Spell the minimum-weight path through the source cardinal graph.
pub fn spell_cardinal_digits(digits: &[u8], out: &mut String) -> Option<Cost> {
    if digits.is_empty() || digits.len() > 21 || !digits.iter().all(u8::is_ascii_digit) {
        return None;
    }
    let len = digits.len();
    if len <= 3 {
        return spell_small(digits, out, true);
    }
    let mut best = String::new();
    let mut best_cost = spell_magnitude(digits, &mut best);
    if len > 12 {
        let mut split = String::new();
        if let Some(c) = spell_split(digits, &mut split) {
            let c = c + cost(-0.5);
            if best_cost.is_none_or(|old| c < old) {
                best = split;
                best_cost = Some(c);
            }
        }
    }
    out.push_str(&best);
    best_cost
}

fn spell_small(digits: &[u8], out: &mut String, zero: bool) -> Option<Cost> {
    match digits {
        [d] if *d != b'0' || zero => out.push_str(digit_word(*d)),
        [t, u] if *t != b'0' => spell_two_digits(*t, *u, out),
        [h, t, u] if *h != b'0' || *t != b'0' || *u != b'0' => spell_hundreds_triplet(*h, *t, *u, out),
        _ => return None,
    }
    Some(0)
}

fn spell_magnitude(digits: &[u8], out: &mut String) -> Option<Cost> {
    let len = digits.len();
    if !(4..=21).contains(&len) {
        return None;
    }
    let level = (len - 1) / 3;
    let zero_count = level * 3;
    let names = ["", "nghìn", "triệu", "tỷ", "nghìn tỷ", "triệu tỷ", "tỷ tỷ"];
    let (leading, tail) = digits.split_at(len - zero_count);
    let mut prefix = String::new();
    spell_small(leading, &mut prefix, false)?;
    prefix.push(' ');
    prefix.push_str(names[level]);
    if tail.iter().all(|&d| d == b'0') {
        out.push_str(&prefix);
        return Some(0);
    }

    let mut best = String::new();
    let mut best_cost = None;
    if level <= 4 && level > 1 {
        let mut part = String::new();
        if let Some(c) = spell_magnitude(tail, &mut part) {
            best = part;
            best_cost = Some(c);
        }
    }
    let zeros = tail.iter().take_while(|&&d| d == b'0').count();
    let remaining = &tail[zeros..];
    if remaining.len() <= 3 {
        let mut part = String::new();
        let c = if remaining.len() == 1 {
            part.push_str("linh ");
            part.push_str(digit_word(remaining[0]));
            Some(cost(-0.1))
        } else {
            spell_small(remaining, &mut part, false)
        };
        if let Some(c) = c {
            if best_cost.is_none_or(|old| c < old) {
                best = part;
                best_cost = Some(c);
            }
        }
    }
    let c = best_cost?;
    out.push_str(&prefix);
    out.push(' ');
    out.push_str(&best);
    Some(c)
}

fn spell_split(digits: &[u8], out: &mut String) -> Option<Cost> {
    let (front, back) = digits.split_at(digits.len().checked_sub(9)?);
    let front_cost = spell_magnitude(front, out)?;
    out.push_str(" tỷ ");
    if back.iter().all(|&d| d == b'0') { Some(front_cost) } else { Some(front_cost + spell_magnitude(back, out)?) }
}

/// Spell the source's dotted-number branch, including its branch weight.
pub fn spell_dot_digits(digits: &[u8], out: &mut String) -> Option<Cost> {
    if digits.len() <= 12 {
        Some(spell_magnitude(digits, out)? + cost(-0.3))
    } else {
        Some(spell_split(digits, out)? + cost(-1.0))
    }
}

pub fn scan_dot_grouped_numbers(text: &[char], start: usize, allow_zero: bool, mut f: impl FnMut(usize, &[u8])) {
    if start >= text.len() || !text[start].is_ascii_digit() || (!allow_zero && text[start] == '0') {
        return;
    }
    let mut i = start;
    let mut digits = [0u8; 21];
    let mut len = 0;
    while i < text.len() && text[i].is_ascii_digit() && len < 3 {
        digits[len] = text[i] as u8;
        len += 1;
        i += 1;
    }
    for _ in 0..6 {
        if i + 3 >= text.len() || text[i] != '.' || !text[i + 1..i + 4].iter().all(char::is_ascii_digit) {
            break;
        }
        for &c in &text[i + 1..i + 4] {
            digits[len] = c as u8;
            len += 1;
        }
        i += 4;
        f(i, &digits[..len]);
    }
}

/// Check if characters at `start` form a dot-grouped number:
/// `1..3 digits` followed by 1 to 6 groups of `"." + 3 digits`.
pub fn parse_dot_grouped_number(text: &[char], start: usize) -> Option<(usize, Vec<u8>, Cost)> {
    if start >= text.len() || !text[start].is_ascii_digit() || text[start] == '0' {
        return None;
    }
    let mut i = start;
    let mut first_group_len = 0;
    while i < text.len() && text[i].is_ascii_digit() {
        first_group_len += 1;
        i += 1;
    }
    if first_group_len > 3 {
        return None;
    }

    let mut dots = 0;
    let mut raw_digits = Vec::with_capacity(24);
    for k in start..i {
        raw_digits.push(text[k] as u8);
    }

    while i < text.len() && text[i] == '.' {
        if i + 3 >= text.len() {
            break;
        }
        if !text[i + 1].is_ascii_digit() || !text[i + 2].is_ascii_digit() || !text[i + 3].is_ascii_digit() {
            break;
        }
        if i + 4 < text.len() && text[i + 4].is_ascii_digit() {
            break;
        }
        dots += 1;
        raw_digits.push(text[i + 1] as u8);
        raw_digits.push(text[i + 2] as u8);
        raw_digits.push(text[i + 3] as u8);
        i += 4;
        if dots == 6 {
            break;
        }
    }

    if dots == 0 {
        return None;
    }

    let total_digits = raw_digits.len();
    if total_digits > 21 {
        return None;
    }

    let dot_weight = if dots >= 4 { cost(-1.0) } else { cost(-0.3) };
    Some((i, raw_digits, dot_weight))
}

/// Check if a character is a Vietnamese punctuation mark from `PunctuationFst`:
/// `s = "!#%&'()*+,-./:;<=>?@^_`{|}~′″°"`
#[inline]
pub fn is_vi_punct(c: char) -> bool {
    matches!(
        c,
        '!' | '#'
            | '%'
            | '&'
            | '\''
            | '('
            | ')'
            | '*'
            | '+'
            | ','
            | '-'
            | '.'
            | '/'
            | ':'
            | ';'
            | '<'
            | '='
            | '>'
            | '?'
            | '@'
            | '^'
            | '_'
            | '`'
            | '{'
            | '|'
            | '}'
            | '~'
            | '′'
            | '″'
            | '°'
    )
}

/// Convert spaces to U+00A0 (NBSP) as in NeMo `convert_space`.
pub fn space_to_nbsp(s: &str) -> String {
    s.replace(' ', "\u{00a0}")
}

/// Convert U+00A0 (NBSP) back to spaces.
pub fn nbsp_to_space(s: &str) -> String {
    s.replace('\u{00a0}', " ")
}

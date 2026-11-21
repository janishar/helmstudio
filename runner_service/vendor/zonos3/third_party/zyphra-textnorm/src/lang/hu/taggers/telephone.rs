// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.

use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.10);
const AREAS: &str = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/telephone/area_codes.tsv"));
const COUNTRIES: &str = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/telephone/country_codes.tsv"));
const SPECIAL: &str = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/telephone/special_numbers.tsv"));

fn code_index(table: &str) -> [bool; 2000] {
    let mut index = [false; 2000];
    for line in table.lines() {
        let key = line.split('\t').next().unwrap_or("");
        if key.is_empty() || key.len() > 3 || !key.bytes().all(|c| c.is_ascii_digit()) {
            continue;
        }
        let number = key.bytes().fold(1usize, |n, c| n * 10 + usize::from(c - b'0'));
        index[number] = true;
    }
    index
}

static AREA_INDEX: LazyLock<[bool; 2000]> = LazyLock::new(|| code_index(AREAS));
static COUNTRY_INDEX: LazyLock<[bool; 2000]> = LazyLock::new(|| code_index(COUNTRIES));
static SPECIAL_INDEX: LazyLock<[bool; 2000]> = LazyLock::new(|| code_index(SPECIAL));

#[derive(Clone, Copy, Default)]
struct Span {
    start: usize,
    len: usize,
}
#[derive(Clone, Copy)]
struct Groups {
    items: [Span; 6],
    len: usize,
    trunk: bool,
}
impl Groups {
    fn new() -> Self {
        Self { items: [Span::default(); 6], len: 0, trunk: false }
    }
    fn push(mut self, span: Span) -> Self {
        self.items[self.len] = span;
        self.len += 1;
        self
    }
}
#[derive(Clone, Copy)]
struct Country {
    prompt: &'static str,
    spaces: u8,
    plus: &'static str,
    number: Option<Span>,
}
fn starts(text: &[char], pos: usize, s: &str) -> bool {
    text.get(pos..pos + s.chars().count()).is_some_and(|part| part.iter().copied().eq(s.chars()))
}
fn separator(c: char) -> bool {
    c == ' ' || c == '-'
}
fn group(text: &[char], start: usize, len: usize) -> Option<Span> {
    let part = text.get(start..start + len)?;
    if part.iter().all(char::is_ascii_digit) && part.iter().any(|&c| c != '0') {
        Some(Span { start, len })
    } else {
        None
    }
}
fn listed(index: &[bool; 2000], text: &[char], span: Span) -> bool {
    let key = text[span.start..span.start + span.len].iter().fold(1usize, |n, c| n * 10 + (*c as usize - '0' as usize));
    index[key]
}
fn append_group(out: &mut String, text: &[char], span: Span) {
    let digits = &text[span.start..span.start + span.len];
    let zeros = digits.iter().take_while(|&&c| c == '0').count();
    for _ in 0..zeros {
        out.push_str("nulla ");
    }
    let mut value = 0u64;
    for &c in &digits[zeros..] {
        value = value * 10 + (c as u8 - b'0') as u64;
    }
    cardinal::spell(value, out);
}
fn emit(
    text: &[char],
    out: &mut Sink,
    end: usize,
    weight: Cost,
    country: Option<Country>,
    groups: Groups,
    extension: Option<Span>,
    ip: bool,
) {
    out.push(end, weight, 0, || {
        let mut tag = String::from("telephone { ");
        if let Some(cc) = country {
            tag.push_str("country_code: \"");
            tag.push_str(cc.prompt);
            for _ in 0..cc.spaces {
                tag.push(' ');
            }
            tag.push_str(cc.plus);
            if let Some(span) = cc.number {
                append_group(&mut tag, text, span);
            }
            tag.push_str("\" ");
        }
        tag.push_str("number_part: \"");
        if groups.trunk {
            tag.push_str("nulla hat ");
        }
        for (i, &span) in groups.items[..groups.len].iter().enumerate() {
            if i != 0 {
                tag.push_str(if ip { " pont " } else { " " });
            }
            append_group(&mut tag, text, span);
        }
        tag.push('"');
        if let Some(span) = extension {
            tag.push_str(" extension: \"");
            append_group(&mut tag, text, span);
            tag.push('"');
        }
        tag.push_str(" }");
        tag
    });
}

fn base(text: &[char], pos: usize, groups: Groups, mut accept: impl FnMut(usize, Groups)) {
    for pattern in [&[3, 3][..], &[3, 2, 2], &[3, 4], &[2, 4], &[4, 3], &[2, 2, 3]] {
        let mut p = pos;
        let mut result = groups;
        let mut valid = true;
        for (i, &len) in pattern.iter().enumerate() {
            if i != 0 {
                if !text.get(p).is_some_and(|&c| separator(c)) {
                    valid = false;
                    break;
                }
                p += 1;
            }
            let Some(g) = group(text, p, len) else {
                valid = false;
                break;
            };
            result = result.push(g);
            p += len;
        }
        if valid {
            accept(p, result);
        }
    }
}
fn extensions(
    text: &[char],
    out: &mut Sink,
    end: usize,
    weight: Cost,
    cc: Option<Country>,
    groups: Groups,
    allow: bool,
) {
    emit(text, out, end, weight, cc, groups, None, false);
    if !allow || !text.get(end).is_some_and(|&c| separator(c) || c == '/') {
        return;
    }
    for len in 1..=4 {
        if let Some(g) = group(text, end + 1, len) {
            let p = end + 1 + len;
            if starts(text, p, " mellék") {
                emit(text, out, p + " mellék".chars().count(), weight, cc, groups, Some(g), false);
            }
        }
    }
}
fn domestic(
    text: &[char],
    pos: usize,
    out: &mut Sink,
    cc: Option<Country>,
    weight: Cost,
    trunk: bool,
    allow_ext: bool,
) {
    let mut p = pos;
    let mut groups = Groups::new();
    if trunk {
        if starts(text, p, "(06)") {
            p += 4;
        } else if starts(text, p, "06") {
            p += 2;
        } else {
            return;
        }
        if !text.get(p).is_some_and(|&c| separator(c)) {
            return;
        }
        p += 1;
        groups.trunk = true;
    }
    for len in 1..=2 {
        let Some(area) = group(text, p, len) else {
            continue;
        };
        if !listed(&AREA_INDEX, text, area) || !text.get(p + len).is_some_and(|&c| separator(c) || c == '/') {
            continue;
        }
        base(text, p + len + 1, groups.push(area), |end, gs| extensions(text, out, end, weight, cc, gs, allow_ext));
    }
}
fn foreign_groups(
    text: &[char],
    pos: usize,
    groups: Groups,
    first: usize,
    out: &mut Sink,
    cc: Option<Country>,
    weight: Cost,
) {
    let count = groups.len - first;
    if count >= 2 {
        emit(text, out, pos, weight, cc, groups, None, false);
    }
    if count == 4 {
        return;
    }
    let p = if count == 0 {
        pos
    } else if text.get(pos).is_some_and(|&c| separator(c)) {
        pos + 1
    } else {
        return;
    };
    for len in 1..=4 {
        if let Some(g) = group(text, p, len) {
            foreign_groups(text, p + len, groups.push(g), first, out, cc, weight);
        }
    }
}
fn foreign(text: &[char], pos: usize, out: &mut Sink, cc: Option<Country>, weight: Cost) {
    for pattern in [[3, 4], [4, 4]] {
        if let Some(a) = group(text, pos, pattern[0]) {
            let p = pos + pattern[0];
            if text.get(p).is_some_and(|&c| separator(c)) {
                if let Some(b) = group(text, p + 1, pattern[1]) {
                    emit(text, out, p + 1 + pattern[1], weight, cc, Groups::new().push(a).push(b), None, false);
                }
            }
        }
    }
    for open in [false, true] {
        if open && text.get(pos) != Some(&'(') {
            continue;
        }
        let p = pos + usize::from(open);
        for len in 1..=3 {
            let Some(area) = group(text, p, len) else {
                continue;
            };
            if text[p] == '0' {
                continue;
            }
            let q = p + len;
            for close in [false, true] {
                if close && text.get(q) != Some(&')') {
                    continue;
                }
                let r = q + usize::from(close);
                if !text.get(r).is_some_and(|&c| separator(c)) {
                    continue;
                }
                let groups = Groups::new().push(area);
                base(text, r + 1, groups, |end, gs| emit(text, out, end, weight, cc, gs, None, false));
                foreign_groups(text, r + 1, groups, 1, out, cc, weight);
            }
        }
    }
}
fn use_country(text: &[char], p: usize, out: &mut Sink, cc: Country, known: bool, numeric: bool) {
    if known {
        domestic(text, p, out, Some(cc), WEIGHT, false, true);
        domestic(text, p, out, Some(cc), WEIGHT, true, true);
        if let Some(g) = group(text, p, 3) {
            if listed(&SPECIAL_INDEX, text, g) {
                emit(text, out, p + 3, WEIGHT, Some(cc), Groups::new().push(g), None, false);
            }
        }
    }
    if text.get(p).is_some_and(|&c| separator(c)) {
        let weight = if numeric && known { WEIGHT - cost(0.05) } else { WEIGHT };
        domestic(text, p + 1, out, Some(cc), weight, false, true);
        foreign(text, p + 1, out, Some(cc), weight);
    }
}
fn numeric_country(text: &[char], pos: usize, prompt: &'static str, spaces_after_prompt: u8, out: &mut Sink) {
    for (raw, plus) in [("+", "plusz "), ("00", "nulla nulla ")] {
        if !starts(text, pos, raw) {
            continue;
        }
        let origin = pos + raw.len();
        for spaces in 0..=2 {
            if !text.get(origin..origin + spaces).is_some_and(|s| s.iter().all(|&c| c == ' ')) {
                continue;
            }
            for open in [false, true] {
                let q = origin + spaces;
                if open && text.get(q) != Some(&'(') {
                    continue;
                }
                let q = q + usize::from(open);
                for len in 1..=3 {
                    let Some(code) = group(text, q, len) else {
                        continue;
                    };
                    if text[q] == '0' {
                        continue;
                    }
                    for close in [false, true] {
                        let r = q + len;
                        if close && text.get(r) != Some(&')') {
                            continue;
                        }
                        let cc = Country { prompt, spaces: spaces_after_prompt, plus, number: Some(code) };
                        use_country(text, r + usize::from(close), out, cc, listed(&COUNTRY_INDEX, text, code), true);
                    }
                }
            }
        }
    }
}
fn ip(text: &[char], p: usize, count: usize, groups: Groups, country: Option<Country>, out: &mut Sink) {
    if count == 0 && text.get(p..).is_none_or(|rest| rest.iter().take(15).filter(|&&c| c == '.').count() < 3) {
        return;
    }
    if count == 4 {
        emit(text, out, p, WEIGHT, country, groups, None, true);
        return;
    }
    let q = if count == 0 {
        p
    } else if text.get(p) == Some(&'.') {
        p + 1
    } else {
        return;
    };
    for len in 1..=3 {
        if let Some(g) = group(text, q, len) {
            ip(text, q + len, count + 1, groups.push(g), country, out);
        }
    }
}

pub struct Telephone;
impl Lazy for Telephone {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if !text.get(start).is_some_and(|c| c.is_ascii_digit() || matches!(c, '(' | '+' | 'A' | 'h' | 't' | 'f')) {
            return;
        }
        ip(text, start, 0, Groups::new(), None, out);
        for prompt in ["Az IP-címed", "Az IP"] {
            if !starts(text, start, prompt) {
                continue;
            }
            let mut p = start + prompt.chars().count();
            while text.get(p).is_some_and(|&c| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}')) {
                p += 1;
                ip(text, p, 0, Groups::new(), Some(Country { prompt, spaces: 0, plus: "", number: None }), out);
            }
        }
        domestic(text, start, out, None, WEIGHT, false, true);
        domestic(text, start, out, None, WEIGHT, true, true);
        if text.get(start) == Some(&'(') {
            for len in 2..=4 {
                if let Some(area) = group(text, start + 1, len) {
                    if (text[start + 1] == '0' && text[start + 2] != '0') || (len <= 3 && text[start + 1] != '0') {
                        let p = start + 1 + len;
                        if text.get(p) == Some(&')') {
                            for space in [0, 1] {
                                if space == 1 && text.get(p + 1) != Some(&' ') {
                                    continue;
                                }
                                base(text, p + 1 + space, Groups::new().push(area), |end, gs| {
                                    emit(text, out, end, WEIGHT, None, gs, None, false)
                                });
                            }
                        }
                    }
                }
            }
        }
        numeric_country(text, start, "", 0, out);
        for (raw, spoken, abbr) in [
            ("hívjon a", "hívjon a", false),
            ("tel.", "telefon", true),
            ("tel", "telefon", true),
            ("fax", "fax", true),
            ("hívjon a tel.", "hívjon a telefon", true),
            ("hívjon a tel", "hívjon a telefon", true),
            ("hívjon a fax", "hívjon a fax", true),
        ] {
            if !starts(text, start, raw) {
                continue;
            }
            let p = start + raw.chars().count();
            let cc = Country { prompt: spoken, spaces: 0, plus: "", number: None };
            use_country(text, p, out, cc, true, false);
            if text.get(p) == Some(&' ') {
                numeric_country(text, p + 1, spoken, 1, out);
            }
            if abbr {
                for pass in [": ", ":", " "] {
                    if !starts(text, p, pass) {
                        continue;
                    }
                    let q = p + pass.len();
                    let cc = Country { spaces: 1, ..cc };
                    use_country(text, q, out, cc, true, false);
                    if text.get(q) == Some(&' ') {
                        numeric_country(text, q + 1, spoken, 2, out);
                    }
                }
            }
        }
    }
}

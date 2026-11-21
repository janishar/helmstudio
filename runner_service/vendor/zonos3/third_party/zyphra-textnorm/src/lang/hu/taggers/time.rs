// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.

use crate::lang::hu::graph_utils::{inflect_abbreviation, naive_inflector};
use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost, is_nemo_space};
use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::LazyLock;

#[derive(Clone, Copy)]
struct Reading {
    h: u32,
    m: Option<u32>,
    s: Option<u32>,
    suffix: Option<&'static str>,
    zone: Option<&'static str>,
}

thread_local! {
    static READINGS: RefCell<Vec<(usize, usize, Option<Reading>)>> = const { RefCell::new(Vec::new()) };
}

pub(in crate::lang::hu) fn clear_readings() {
    READINGS.with_borrow_mut(Vec::clear);
}

fn record(out: &Sink, end: usize, reading: Option<Reading>) {
    if out.exploring() {
        READINGS.with_borrow_mut(|rows| rows.push((out.start, end, reading)));
    }
}

pub(in crate::lang::hu) fn reading(start: usize, end: usize) -> Option<String> {
    READINGS
        .with_borrow(|rows| rows.iter().find(|&&(s, e, _)| s == start && e == end).and_then(|row| row.2).map(render))
}

fn render(reading: Reading) -> String {
    let Reading { h, m, s, suffix, zone } = reading;
    let mut hour = String::new();
    cardinal::spell(h as u64, &mut hour);
    let mut tag = format!("time {{ hours: \"{hour}\"");
    if let Some(n) = m {
        let mut v = String::new();
        cardinal::spell(n as u64, &mut v);
        tag.push_str(&format!(" minutes: \"{v}\""));
    }
    if let Some(n) = s {
        let mut v = String::new();
        cardinal::spell(n as u64, &mut v);
        tag.push_str(&format!(" seconds: \"{v}\""));
    }
    if let Some(v) = suffix {
        tag.push_str(&format!(" suffix: \"{v}\""));
    }
    if let Some(v) = zone {
        tag.push_str(&format!(" zone: \"{v}\""));
    }
    tag.push_str(" preserve_order: true }");
    tag
}

pub const WEIGHT: Cost = cost(1.09);
crate::tsv!(TIME_ZONE, "hu/time/time_zone.tsv");

static FORMS: LazyLock<Vec<(String, String, u8)>> = LazyLock::new(|| {
    let mut forms = Vec::new();
    for (abbr, word, kind) in [("ó", "óra", 1), ("p", "perc", 2), ("mp", "másodperc", 4)] {
        forms.push((abbr.into(), word.into(), kind));
        forms.push((word.into(), word.into(), kind));
        for (input, output) in naive_inflector(abbr, word, true) {
            forms.push((input, output.clone(), kind));
            forms.push((output.clone(), output, kind));
        }
    }
    forms
});

static RAW_FORMS: LazyLock<Vec<(String, String)>> = LazyLock::new(|| {
    let mut forms = Vec::new();
    for (abbr, word) in [("ó", "óra"), ("p", "perc")] {
        forms.push((abbr.into(), word.into()));
        forms.extend(naive_inflector(abbr, word, true));
    }
    forms
});
static FORM_INDEX: LazyLock<HashMap<char, Vec<usize>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<usize>> = HashMap::new();
    for (i, (key, _, _)) in FORMS.iter().enumerate() {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push(i);
        }
    }
    index
});

static RAW_FORM_INDEX: LazyLock<HashMap<char, Vec<usize>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<usize>> = HashMap::new();
    for (i, (key, _)) in RAW_FORMS.iter().enumerate() {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push(i);
        }
    }
    index
});

fn form_indices<'a>(
    index: &'static HashMap<char, Vec<usize>>,
    text: &'a [char],
    pos: usize,
) -> impl Iterator<Item = usize> + 'a {
    text.get(pos).and_then(|c| index.get(c)).into_iter().flatten().copied()
}

static ZONES: LazyLock<Vec<(String, String)>> = LazyLock::new(|| {
    let mut zones = Vec::new();
    for line in include_str!("../../../../data/hu/time/time_zone.tsv").lines() {
        if let Some((key, _)) = line.split_once('\t') {
            let value = TIME_ZONE.exact(key).next().unwrap().value.as_str();
            zones.push((key.into(), value.replace(' ', "\u{a0}")));
            for (input, _) in inflect_abbreviation(value, "a", true) {
                zones.push((input.replace(' ', ""), input.replace(' ', "\u{a0}")));
            }
        }
    }
    zones
});

fn matches(text: &[char], start: usize, word: &str) -> Option<usize> {
    let mut i = start;
    for c in word.chars() {
        if text.get(i) != Some(&c) {
            return None;
        }
        i += 1;
    }
    Some(i)
}
fn number(
    text: &[char],
    start: usize,
    max: u32,
    exact_two: bool,
    zero: bool,
) -> impl Iterator<Item = (usize, u32)> + '_ {
    let mut len = 0;
    let mut n = 0;
    std::iter::from_fn(move || {
        while len < 2 {
            let c = text.get(start + len).and_then(|c| c.to_digit(10).filter(|_| c.is_ascii_digit()))?;
            len += 1;
            n = n * 10 + c;
            if (!exact_two || len == 2) && n <= max && (zero || n != 0) && (len == 1 || text[start] != '0' || n < 10) {
                return Some((start + len, n));
            }
        }
        None
    })
}
fn push(
    out: &mut Sink,
    end: usize,
    h: u32,
    m: Option<u32>,
    s: Option<u32>,
    suffix: Option<&'static str>,
    zone: Option<&'static str>,
) {
    let reading = Reading { h, m, s, suffix, zone };
    record(out, end, Some(reading));
    out.push(end, WEIGHT, 0, || render(reading));
}
fn zones(
    out: &mut Sink,
    text: &[char],
    pos: usize,
    h: u32,
    m: Option<u32>,
    s: Option<u32>,
    suffix: Option<&'static str>,
) {
    if text.get(pos) != Some(&' ') {
        return;
    }
    for (key, value) in ZONES.iter() {
        if let Some(end) = matches(text, pos + 1, key) {
            push(out, end, h, m, s, suffix, Some(value));
        }
    }
}
fn forms(text: &[char], pos: usize, mask: u8, mut f: impl FnMut(usize, &'static str)) {
    for i in form_indices(&FORM_INDEX, text, pos) {
        let (key, value, kind) = &FORMS[i];
        if kind & mask != 0 {
            if let Some(end) = matches(text, pos, key) {
                f(end, value);
            }
        }
    }
}
fn space(text: &[char], pos: usize) -> bool {
    text.get(pos).is_some_and(|c| is_nemo_space(*c))
}

// A few source paths emit unit text directly into the token rather than a
// field. Keep that output verbatim, even though it can make the token invalid.
fn raw_tag(h: u32, m: Option<u32>, between: &str, raw: &str, zone: Option<&str>) -> String {
    let mut hour = String::new();
    cardinal::spell(h as u64, &mut hour);
    let mut tag = format!("time {{ hours: \"{hour}\"");
    if let Some(n) = m {
        let mut minute = String::new();
        cardinal::spell(n as u64, &mut minute);
        tag.push_str(between);
        tag.push_str(&format!("minutes: \"{minute}\""));
    } else {
        tag.push_str(between);
    }
    tag.push_str(raw);
    if let Some(value) = zone {
        tag.push_str(&format!(" zone: \"{value}\""));
    }
    tag.push_str(" preserve_order: true }");
    tag
}

fn render_raw_path(paths: &[Option<(usize, usize)>], mut offset: usize) -> String {
    let mut indices = Vec::new();
    while offset != 0 {
        let (previous, index) = paths[offset].unwrap();
        indices.push(index);
        offset = previous;
    }
    indices.into_iter().rev().map(|index| RAW_FORMS[index].1.as_str()).collect()
}

fn raw_hm_forms(out: &mut Sink, text: &[char], start: usize, h: u32, m: Option<u32>) {
    // One first witness per input endpoint is sufficient: all arcs have
    // equal weight and lead back to the same closure state.
    if !form_indices(&RAW_FORM_INDEX, text, start).any(|i| matches(text, start, &RAW_FORMS[i].0).is_some()) {
        return;
    }
    let mut paths: Vec<Option<(usize, usize)>> = vec![None; text.len() - start + 1];
    paths[0] = Some((0, 0));
    for offset in 0..paths.len() {
        if paths[offset].is_none() {
            continue;
        }
        let pos = start + offset;
        if offset != 0 {
            record(out, pos, None);
            out.push(pos, WEIGHT, 0, || {
                let raw = render_raw_path(&paths, offset);
                raw_tag(h, m, if m.is_some() { " " } else { "" }, &raw, None)
            });
            if text.get(pos) == Some(&' ') {
                for (key, value) in ZONES.iter() {
                    if let Some(end) = matches(text, pos + 1, key) {
                        record(out, end, None);
                        out.push(end, WEIGHT, 0, || {
                            let raw = render_raw_path(&paths, offset);
                            raw_tag(h, m, if m.is_some() { " " } else { "" }, &raw, Some(value))
                        });
                    }
                }
            }
        }
        for index in form_indices(&RAW_FORM_INDEX, text, pos) {
            let key = &RAW_FORMS[index].0;
            let Some(end) = matches(text, pos, key) else { continue };
            if paths[end - start].is_none() {
                paths[end - start] = Some((offset, index));
            }
        }
    }
}

pub struct Time;
impl Lazy for Time {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        for (he, h) in number(text, start, 24, false, true) {
            if space(text, he) {
                forms(text, he + 1, 1, |end, suffix| push(out, end, h, None, None, Some(suffix), None));
                for word in ["ó", "óra"] {
                    if let Some(end) = matches(text, he + 1, word) {
                        if text.get(end) == Some(&' ') {
                            for (key, value) in ZONES.iter() {
                                if let Some(zone_end) = matches(text, end + 1, key) {
                                    record(out, zone_end, None);
                                    out.push(zone_end, WEIGHT, 0, || {
                                        raw_tag(h, None, &text[he].to_string(), "óra", Some(value))
                                    });
                                }
                            }
                        }
                    }
                }
            }
            if let Some(sep @ (':' | '.')) = text.get(he).copied() {
                for (me, minute) in number(text, he + 1, 59, true, true) {
                    let m = if minute == 0 { None } else { Some(minute) };
                    if sep == ':' || he - start == 2 {
                        push(out, me, h, m, None, None, None);
                    }
                    if sep == ':' {
                        zones(out, text, me, h, m, None, None);
                        raw_hm_forms(out, text, me, h, m);
                        if text.get(me) == Some(&':') {
                            for (se, second) in number(text, me + 1, 59, true, true) {
                                let s = if second == 0 { None } else { Some(second) };
                                push(out, se, h, m, s, None, None);
                                zones(out, text, se, h, m, s, None);
                                forms(text, se, 7, |end, suffix| {
                                    push(out, end, h, m, s, Some(suffix), None);
                                    zones(out, text, end, h, m, s, Some(suffix));
                                });
                            }
                        }
                    }
                    if space(text, me) {
                        for (key, value) in [
                            ("am", "délelőtt"),
                            ("a.m.", "délelőtt"),
                            ("AM", "délelőtt"),
                            ("pm", "délután"),
                            ("p.m.", "délután"),
                            ("PM", "délután"),
                        ] {
                            if let Some(end) = matches(text, me + 1, key) {
                                push(out, end, h, m, None, Some(value), None);
                            }
                        }
                    }
                    if sep == ':' || he - start == 2 {
                        for (key, perc, ora) in [
                            ("-kor", "perckor", "órakor"),
                            ("-ig", "percig", "óráig"),
                            ("-tól", "perctől", "órától"),
                            ("-től", "perctől", "órától"),
                            ("-ra", "percre", "órára"),
                            ("-re", "percre", "órára"),
                            ("-ról", "percről", "óráról"),
                            ("-ről", "percről", "óráról"),
                        ] {
                            if let Some(end) = matches(text, me, key) {
                                push(out, end, h, m, None, Some(if m.is_some() { perc } else { ora }), None);
                            }
                        }
                    }
                }
            }
            for hp in [he, he + 1] {
                if hp != he && !space(text, he) {
                    continue;
                }
                for hour_word in ["ó", "óra"] {
                    let Some(after_hour) = matches(text, hp, hour_word) else { continue };
                    if !space(text, after_hour) {
                        continue;
                    }
                    for (me, minute) in number(text, after_hour + 1, 59, false, false) {
                        for mp in [me, me + 1] {
                            if mp != me && !space(text, me) {
                                continue;
                            }
                            forms(text, mp, 3, |end, suffix| push(out, end, h, Some(minute), None, Some(suffix), None));
                            for word in ["p", "perc"] {
                                if let Some(end) = matches(text, mp, word) {
                                    if text.get(end) == Some(&' ') {
                                        for (key, value) in ZONES.iter() {
                                            if let Some(zone_end) = matches(text, end + 1, key) {
                                                record(out, zone_end, None);
                                                out.push(zone_end, WEIGHT, 0, || {
                                                    let mut between = String::new();
                                                    if hp != he {
                                                        between.push(text[he]);
                                                    }
                                                    between.push(text[after_hour]);
                                                    let mut raw = String::new();
                                                    if mp != me {
                                                        raw.push(text[me]);
                                                    }
                                                    raw.push_str("perc");
                                                    raw_tag(h, Some(minute), &between, &raw, Some(value))
                                                });
                                            }
                                        }
                                    }
                                    if !space(text, end) {
                                        continue;
                                    }
                                    for (se, second) in number(text, end + 1, 59, false, false) {
                                        for sp in [se, se + 1] {
                                            if sp != se && !space(text, se) {
                                                continue;
                                            }
                                            forms(text, sp, 7, |end, suffix| {
                                                push(out, end, h, Some(minute), Some(second), Some(suffix), None)
                                            });
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

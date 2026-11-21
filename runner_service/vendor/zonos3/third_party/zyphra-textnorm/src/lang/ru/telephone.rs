// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Telephone classifier, ported from ru/taggers/telephone.py.

use super::number::{Case, Gender, cardinal};
use crate::lattice::Candidate;
use crate::text::cost;

pub fn spell_group(g: &str) -> Option<String> {
    if g.is_empty() || !g.chars().all(|c| c.is_ascii_digit()) {
        return None;
    }
    match g.len() {
        1 => {
            let d: u64 = g.parse().ok()?;
            Some(cardinal(d, Gender::Masc, Case::Nom))
        }
        2 => {
            if g == "00" {
                Some("ноль ноль".to_string())
            } else if g.starts_with('0') {
                let d: u64 = g[1..].parse().ok()?;
                Some(format!("ноль {}", cardinal(d, Gender::Masc, Case::Nom)))
            } else {
                let n: u64 = g.parse().ok()?;
                Some(cardinal(n, Gender::Masc, Case::Nom))
            }
        }
        3 => {
            if g == "000" {
                Some("ноль ноль ноль".to_string())
            } else if g.starts_with("00") {
                let d: u64 = g[2..].parse().ok()?;
                Some(format!("ноль ноль {}", cardinal(d, Gender::Masc, Case::Nom)))
            } else if g.starts_with('0') {
                let d: u64 = g[1..].parse().ok()?;
                Some(format!("ноль {}", cardinal(d, Gender::Masc, Case::Nom)))
            } else {
                let n: u64 = g.parse().ok()?;
                Some(cardinal(n, Gender::Masc, Case::Nom))
            }
        }
        4 => {
            let g1 = spell_group(&g[0..2])?;
            let g2 = spell_group(&g[2..4])?;
            Some(format!("{g1} {g2}"))
        }
        _ => None,
    }
}

// The source has three finite layouts; separators are single characters and
// parentheses are permitted only around the area group.
fn groups(s: &str) -> Option<(bool, Vec<&str>)> {
    fn sep(s: &str) -> Option<&str> {
        match s.as_bytes().first()? {
            b'-' | b'.' | b' ' => Some(&s[1..]),
            _ => None,
        }
    }
    fn digits(s: &str, min: usize, max: usize) -> Option<(&str, &str)> {
        let n = s.bytes().take_while(u8::is_ascii_digit).count();
        if (min..=max).contains(&n) { Some(s.split_at(n)) } else { None }
    }
    fn area(s: &str) -> Option<(&str, &str)> {
        let s = s.strip_prefix('(')?;
        let (g, rest) = digits(s, 2, 4)?;
        Some((g, rest.strip_prefix(") ")?))
    }
    fn parts<'a>(mut s: &'a str, mut out: Vec<&'a str>) -> Option<Vec<&'a str>> {
        let (g, rest) = digits(s, 2, 4)?;
        out.push(g);
        s = rest;
        for _ in 0..3 {
            let Some(rest) = sep(s) else { break };
            let (g, rest) = digits(rest, 2, 4)?;
            out.push(g);
            s = rest;
        }
        if s.is_empty() && out.len() >= 2 { Some(out) } else { None }
    }
    if let Some(rest) = s.strip_prefix('+') {
        let (country, rest) = digits(rest, 1, 3)?;
        if country.starts_with('0') {
            return None;
        }
        let rest = sep(rest)?;
        let (first, rest) = if rest.starts_with('(') {
            area(rest)?
        } else {
            let (g, rest) = digits(rest, 2, 4)?;
            (g, sep(rest)?)
        };
        let mut out = vec![country, first];
        out.extend(parts(rest, Vec::new())?);
        return Some((true, out));
    }
    let mut rest = s;
    let mut out = Vec::new();
    if rest.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        let (first, r) = digits(rest, 1, 1)?;
        out.push(first);
        rest = sep(r)?;
        if !rest.starts_with('(') {
            if !r.starts_with('-') {
                return None;
            }
            let mut all = vec![first];
            for (idx, n) in [3, 3, 2, 2].into_iter().enumerate() {
                let (g, r) = digits(rest, n, n)?;
                all.push(g);
                rest = if idx < 3 { r.strip_prefix('-')? } else { r };
            }
            return rest.is_empty().then_some((false, all));
        }
    }
    let (a, rest) = area(rest)?;
    out.push(a);
    out.extend(parts(rest, Vec::new())?);
    Some((false, out))
}

pub fn render_telephone(s: &str) -> Option<String> {
    let (international, groups) = groups(s)?;
    let mut words = Vec::new();
    for (i, group) in groups.iter().enumerate() {
        if international && i == 0 {
            words.push(format!("плюс {}", cardinal(group.parse().ok()?, Gender::Masc, Case::Nom)));
        } else {
            words.push(spell_group(group)?);
        }
    }
    Some(words.join(" "))
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if render_telephone(s).is_some() {
        out.push(Candidate { end, cost: cost(1.1), rank: 0, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(spoken) = render_telephone(s) {
        candidate.tag = format!("telephone {{ number_part: \"{spoken}\" }}");
    }
}

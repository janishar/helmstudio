// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Russian measure classifier, based on ru/taggers/measure.py.

use super::cardinal::{number_graph, range_graph};
use super::decimal::{fractional_graph, integer_graph, render_integer_part};
use super::number::{Case, Gender, PluralCategory, cardinal, plural_category};
use super::units::{Form, Noun, QUANTITY_SUFFIXES, UNIT_SUFFIXES, agree, per_unit_phrase};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

const SUFFIXES: &[&str] = &[
    "й", "ый", "ой", "ий", "я", "ая", "ья", "е", "ые", "ое", "ье", "и", "ьи", "го", "ого", "его", "ей", "му", "ому",
    "ему", "м", "ом", "ем", "ым", "им", "х", "ых", "их", "ми", "ыми", "ю", "ую",
];

pub struct ParsedMeasure {
    pub spoken: String,
    pub cost: Cost,
}

fn space(s: &str) -> &str {
    s.strip_suffix(' ').or_else(|| s.strip_suffix('\u{a0}')).unwrap_or(s)
}
fn form(n: u64) -> Form {
    match plural_category(n) {
        PluralCategory::One => Form::One,
        PluralCategory::Few => Form::Few,
        PluralCategory::Many => Form::Many,
    }
}
fn weight(g: super::cardinal::NumberGraph) -> f64 {
    g.cost as f64 / 1_000_000.0
}
fn decimal(s: &str, point: bool) -> Option<(u64, super::decimal::FractionalGraph)> {
    let (a, b) = s.split_once(',').or_else(|| if point { s.split_once('.') } else { None })?;
    if s.contains('.') && (a.is_empty() || a.len() > 3 || !a.bytes().all(|c| c.is_ascii_digit())) {
        return None;
    }
    Some((integer_graph(a)?.value, fractional_graph(b)?))
}
fn render_decimal(d: (u64, super::decimal::FractionalGraph)) -> String {
    format!("{} {}", render_integer_part(d.0), d.1.render())
}
pub(super) fn russian(s: &str) -> bool {
    !s.is_empty() && s.chars().all(|c| ('а'..='я').contains(&c) || ('А'..='Я').contains(&c) || c == 'ё' || c == 'Ё')
}
fn counted(n: u64, noun: &Noun) -> String {
    agree(&cardinal(n, Gender::Masc, Case::Nom), form(n), noun)
}
fn gen_counted(n: u64, noun: &Noun) -> String {
    agree(
        &cardinal(n, Gender::Masc, Case::Gen),
        if plural_category(n) == PluralCategory::One { Form::Gen } else { Form::Many },
        noun,
    )
}
fn range_number(s: &str) -> Option<u64> {
    range_graph(s).map(|g| g.value)
}

fn parse_measure(s: &str, target: Option<Cost>) -> Option<ParsedMeasure> {
    let mut best = parse_measure_core(s, None, target);
    if target.is_some() && best.as_ref().is_some_and(|p| Some(p.cost) == target) {
        return best;
    }
    if s.contains('/') {
        for &unit in UNIT_SUFFIXES.matching(s) {
            let Some(core) = s.strip_suffix(&unit.written).and_then(|s| s.strip_suffix('/')) else { continue };
            if let Some(parsed) = parse_measure_core(core, Some(unit), target) {
                if Some(parsed.cost) == target && target.is_some() {
                    return Some(parsed);
                }
                if best.as_ref().is_none_or(|old| parsed.cost < old.cost) {
                    best = Some(parsed);
                }
            }
        }
    }
    best
}

fn parse_measure_core(core: &str, suffix: Option<&Noun>, target: Option<Cost>) -> Option<ParsedMeasure> {
    let mut best: Option<ParsedMeasure> = None;
    macro_rules! add {
        ($spoken:expr, $weight:expr) => {{
            let c = cost($weight);
            if best.as_ref().map_or(true, |old| c < old.cost) {
                best = Some(ParsedMeasure { spoken: if Some(c) == target { $spoken } else { String::new() }, cost: c });
            }
        }};
    }
    let suffix_text = || suffix.map(|u| format!(" {}", per_unit_phrase(u))).unwrap_or_default();
    let (sign, unsigned) = if let Some(r) = core.strip_prefix('-') {
        ("минус ", r)
    } else if let Some(r) = core.strip_prefix('±') {
        ("плюс-минус ", r.strip_prefix(' ').unwrap_or(r))
    } else if let Some(r) = core.strip_prefix('~') {
        ("приблизительно ", r.strip_prefix(' ').unwrap_or(r))
    } else {
        ("", core)
    };
    if suffix.is_some() {
        if let Some(g) = number_graph(unsigned, false) {
            add!(format!("{sign}{}{}", cardinal(g.value, Gender::Masc, Case::Nom), suffix_text()), 1.08 + weight(g));
        }
    }
    for &u in UNIT_SUFFIXES.matching(unsigned) {
        let Some(prefix) = unsigned.strip_suffix(&u.written) else { continue };
        let nstr = space(prefix);
        if let Some(g) = number_graph(nstr, false) {
            let decade = nstr.len() == 4
                && (nstr.starts_with("18") || nstr.starts_with("19") || nstr.starts_with("20"))
                && nstr.ends_with('0');
            if u.written != "s" || prefix.len() != nstr.len() || !decade {
                add!(
                    if sign.is_empty() && suffix.is_none() {
                        counted(g.value, u)
                    } else {
                        format!("{sign}{}{}", counted(g.value, u), suffix_text())
                    },
                    1.08 + weight(g)
                );
            }
        }
        if let Some(d) = decimal(nstr, true) {
            add!(format!("{sign}{}{}", agree(&render_decimal(d), Form::Gen, u), suffix_text()), 1.08);
        }
        for &q in QUANTITY_SUFFIXES.matching(nstr) {
            let Some(before_q) = nstr.strip_suffix(&q.written) else { continue };
            let before_q = space(before_q);
            if let Some(g) = number_graph(before_q, false) {
                add!(format!("{sign}{} {}{}", counted(g.value, q), u.many, suffix_text()), 1.08 + weight(g));
            }
            if let Some(d) = decimal(before_q, false) {
                add!(format!("{sign}{} {} {}{}", render_decimal(d), q.genitive, u.many, suffix_text()), 1.08);
            }
        }
    }
    let approx = if let Some(r) = core.strip_prefix('~') {
        Some(("около", r.strip_prefix(' ').unwrap_or(r)))
    } else if core.chars().next().is_some_and(super::whitelist::is_ru_alpha) {
        super::cardinal::GENITIVE_PREPOSITIONS
            .iter()
            .find_map(|p| core.strip_prefix(p).and_then(|r| r.strip_prefix(' ')).map(|r| (*p, r)))
    } else {
        None
    };
    if let Some((prep, rest)) = approx {
        for &u in UNIT_SUFFIXES.matching(rest) {
            if let Some(p) = rest.strip_suffix(&u.written) {
                let p = space(p);
                if let Some(g) = number_graph(p, true) {
                    add!(
                        format!("{prep} {}{}", agree(&g.render(Gender::Masc, Case::Gen), g.form, u), suffix_text()),
                        1.03 + g.cost as f64 / 1_000_000.0
                    );
                }
            }
        }
    }
    for sep in ['-', '–'] {
        if let Some((a, b)) = core.split_once(sep) {
            if let Some(n1) = range_number(a) {
                for &u in UNIT_SUFFIXES.matching(b) {
                    if let Some(n2) = b.strip_suffix(&u.written).and_then(|p| range_number(space(p))) {
                        add!(
                            format!(
                                "от {} до {}{}",
                                cardinal(n1, Gender::Masc, Case::Gen),
                                gen_counted(n2, u),
                                suffix_text()
                            ),
                            1.08
                        );
                    }
                }
            }
        }
    }
    if suffix.is_none() && core.chars().any(super::whitelist::is_ru_alpha) {
        for (i, _) in core.char_indices().skip(1) {
            let (a, b) = core.split_at(i);
            let b = b.strip_prefix('-').unwrap_or(b);
            if a.as_bytes().last().is_some_and(u8::is_ascii_digit)
                && b.chars().next().is_some_and(super::whitelist::is_ru_alpha)
                && russian(b)
                && !SUFFIXES.contains(&b)
                && !QUANTITY_SUFFIXES.matching(b).iter().any(|q| q.written == b)
            {
                if let Some(g) = number_graph(a, false) {
                    add!(format!("{} {b}", cardinal(g.value, Gender::Masc, Case::Nom)), 1.18 + weight(g));
                }
                if let Some(d) = decimal(a, false) {
                    add!(format!("{} {b}", render_decimal(d)), 1.18);
                }
            }
            let a = a.strip_suffix('-').unwrap_or(a);
            if b.as_bytes().first().is_some_and(u8::is_ascii_digit) && russian(a) {
                if let Some(g) = number_graph(b, false) {
                    add!(format!("{a} {}", cardinal(g.value, Gender::Masc, Case::Nom)), 1.18 + weight(g));
                }
                if let Some(d) = decimal(b, false) {
                    add!(format!("{a} {}", render_decimal(d)), 1.18);
                }
            }
        }
    }
    best
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(p) = parse_measure(s, None) {
        out.push(Candidate { end, cost: p.cost, rank: 0, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(p) = parse_measure(s, Some(candidate.cost)) {
        candidate.tag = format!("measure {{ cardinal {{ integer: \"{}\" }} }}", p.spoken);
    }
}

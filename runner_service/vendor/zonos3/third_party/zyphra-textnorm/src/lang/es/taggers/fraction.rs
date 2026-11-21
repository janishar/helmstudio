//! `es/taggers/fraction.py`.

use super::{cardinal, ordinal};
use crate::lang::es::{Lazy, Sink};
use crate::text::{Cost, cost, is_digit};

crate::tsv!(ORDINAL_EXCEPTIONS, "es/fractions/ordinal_exceptions.tsv");
crate::tsv!(POWERS_OF_TEN, "es/fractions/powers_of_ten.tsv");
crate::tsv!(FRACTION_SYMBOLS, "es/fractions/fraction_symbols.tsv");

/// `(A " ")? ("mil ")? (A " ")?` with `A` = one_to_one_thousand.
fn power_prefix(p: &str) -> bool {
    fn a(s: &str) -> Vec<&str> {
        s.match_indices(' ').filter(|(i, _)| ordinal::one_to_thousand(&s[..*i])).map(|(i, _)| &s[i + 1..]).collect()
    }
    fn mil(s: &str) -> Vec<&str> {
        s.strip_prefix("mil ").into_iter().collect()
    }
    fn step(v: Vec<&str>, f: fn(&str) -> Vec<&str>) -> Vec<&str> {
        let mut out = v.clone();
        for s in v {
            out.extend(f(s));
        }
        out
    }
    step(step(step(vec![p], a), mil), a).contains(&"")
}

/// Ordinal denominators (`graph_fractions_ordinals`) on digits.
fn ordinal_denominators(d: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    if matches!(d, "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" | "10") {
        if let Some((w, c)) = ordinal::graph_digits(d) {
            let w = ORDINAL_EXCEPTIONS.rows().iter().fold(w, |w, r| w.replace(&r.key, &r.value));
            out.push((w, c));
        }
    }
    if let Some(x) = ordinal::multiples_of_thousand(d) {
        out.push(x);
    }
    if matches!(d, "100" | "200" | "300" | "400" | "500" | "600" | "700" | "800" | "900") {
        if let Some(x) = ordinal::graph_digits(d) {
            out.push(x);
        }
    }
    if let Some((w, c)) = cardinal::number(d) {
        for r in POWERS_OF_TEN.rows() {
            let Some(p) = w.strip_suffix(r.key.as_str()) else {
                continue;
            };
            if !power_prefix(p) {
                continue;
            }
            let mut s = format!("{p}{}", r.value);
            if let Some(rest) = s.strip_prefix("un ") {
                if POWERS_OF_TEN.rows().iter().any(|q| rest.starts_with(q.value.as_str())) {
                    s = rest.to_string();
                }
            }
            out.push((s, c + r.cost));
        }
    }
    out
}

/// `(numerator, denominator digits)` → tag bodies after `numerator: "`.
fn bodies(num: &str, den: &str, out: &mut Vec<(String, Cost, i32)>) {
    let Some((n, nc)) = cardinal::number(num) else {
        return;
    };
    for (w, c) in ordinal_denominators(den) {
        out.push((format!("numerator: \"{n}\" denominator: \"{w}\" morphosyntactic_features: \"ordinal\""), nc + c, 0));
    }
    let blocked = matches!(den, "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" | "10" | "0");
    if den.len() <= 2 && !blocked {
        if let Some((w, c)) = cardinal::number(den) {
            out.push((
                format!("numerator: \"{n}\" denominator: \"{w}\" morphosyntactic_features: \"add_root\""),
                nc + c,
                1,
            ));
        }
    }
    if let Some((w, c)) = cardinal::number(den) {
        out.push((format!("numerator: \"{n}\" denominator: \"{w}\""), nc + c + cost(0.001), 2));
    }
}

/// Spans of `numerator + denominator` at `start` (cardinal spans via filter_punctuation).
fn fraction_at(text: &[char], start: usize) -> Vec<(usize, String, Cost, i32)> {
    let mut out = Vec::new();
    for (row, e) in FRACTION_SYMBOLS.prefix_matches(text, start) {
        let (n, d) = row.value.split_once('/').unwrap();
        let mut b = Vec::new();
        bodies(n, d, &mut b);
        out.extend(b.into_iter().map(|(s, c, r)| (e, s, c + row.cost, r)));
    }
    for ne in cardinal::number_ends(text, start) {
        let ds = if text.get(ne) == Some(&'/') {
            ne + 1
        } else if text.get(ne) == Some(&' ') && text.get(ne + 1) == Some(&'/') && text.get(ne + 2) == Some(&' ') {
            ne + 3
        } else {
            continue;
        };
        if ds >= text.len() || !is_digit(text[ds]) {
            continue;
        }
        let Some(num) = cardinal::filter_punctuation(&text[start..ne]) else {
            continue;
        };
        for de in cardinal::number_ends(text, ds) {
            let Some(den) = cardinal::filter_punctuation(&text[ds..de]) else {
                continue;
            };
            let mut b = Vec::new();
            bodies(&num, &den, &mut b);
            out.extend(b.into_iter().map(|(s, c, r)| (de, s, c, r)));
        }
    }
    out
}

/// `FractionFst.graph` bodies at `start` (without the `fraction { … }` wrapper).
pub fn graph(text: &[char], start: usize) -> Vec<(usize, String, Cost, i32)> {
    let (neg, s) = if text[start] == '-' { ("negative: \"true\" ", start + 1) } else { ("", start) };
    let mut out = Vec::new();
    if s >= text.len() {
        return out;
    }
    for (e, b, c, r) in fraction_at(text, s) {
        out.push((e, format!("{neg}{b}"), c, r));
    }
    if is_digit(text[s]) {
        for ie in cardinal::number_ends(text, s) {
            if text.get(ie) != Some(&' ') || ie + 1 >= text.len() {
                continue;
            }
            let tails = fraction_at(text, ie + 1);
            if tails.is_empty() {
                continue;
            }
            let Some((w, ic)) = cardinal::graph(&text[s..ie]) else {
                continue;
            };
            for (e, b, c, r) in tails {
                out.push((e, format!("{neg}integer_part: \"{w}\" {b}"), ic + c, r));
            }
        }
    }
    out
}

pub struct Fraction;

impl Lazy for Fraction {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let c = text[start];
        if !(is_digit(c) || c == '-' || FRACTION_SYMBOLS.prefix_matches(text, start).next().is_some()) {
            return;
        }
        for (end, b, c, rank) in graph(text, start) {
            out.push(end, cost(1.09) + c, rank, || format!("fraction {{ {b} }}"));
        }
    }
}

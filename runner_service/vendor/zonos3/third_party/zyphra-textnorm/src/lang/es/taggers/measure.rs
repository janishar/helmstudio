//! `es/taggers/measure.py`.

use super::{cardinal, decimals, fraction};
use crate::lang::es::graph_utils::strip_cardinal_apocope;
use crate::lang::es::{FirstChars, Lazy, Sink};
use crate::text::{Cost, Table, cost, is_alpha, is_digit, is_nemo_space, space_to_nbsp};

crate::tsv!(UNIT, "es/measures/measurements.tsv");
crate::tsv!(UNIT_COMPLEX, "es/measures/measurements_complex.tsv");
crate::tsv!(PLURAL_FEM, "es/measures/measurements_plural_fem.tsv");
crate::tsv!(PLURAL_MASC, "es/measures/measurements_plural_masc.tsv");
crate::tsv!(MATH_SYMBOLS, "es/measures/math_symbols.tsv");

type Hit = (usize, String, Cost);

static FRACTION_FIRST: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    fraction::FRACTION_SYMBOLS.rows().iter().filter_map(|r| r.key.chars().next()).collect()
});

/// First chars of every unit, complex unit, math symbol and quantity word, plus `x X - = /`: the chars after a
/// number at which a measure can continue.
static AFTER_NUMBER: std::sync::LazyLock<FirstChars> = std::sync::LazyLock::new(|| {
    let tables: [&Table; 4] = [&UNIT, &UNIT_COMPLEX, &MATH_SYMBOLS, &decimals::QUANTITIES];
    FirstChars::of(
        tables.into_iter().flat_map(|t| t.rows().iter().map(|r| r.key.as_str())).chain(["x", "X", "-", "=", "/"]),
    )
});

/// Whether a unit (simple, complex or a "/unit" denominator) starts at `p`.
fn unit_at(text: &[char], p: usize) -> bool {
    AFTER_NUMBER.at(text, p)
        && (UNIT.prefix_matches(text, p).next().is_some()
            || UNIT_COMPLEX.prefix_matches(text, p).next().is_some()
            || (text.get(p) == Some(&'/') && UNIT.prefix_matches(text, p + 1).next().is_some()))
}

fn skip_ws(text: &[char], mut p: usize) -> usize {
    while p < text.len() && is_nemo_space(text[p]) {
        p += 1;
    }
    p
}

fn plural(s: &str) -> Vec<(String, Cost)> {
    PLURAL_FEM.exact(s).chain(PLURAL_MASC.exact(s)).map(|r| (r.value.clone(), r.cost)).collect()
}

/// Units at `p`: `units: "…"` bodies. `plural`: plural forms; `complex`: complex graph variants;
/// `complex_singular`: use the singular complex graph.
fn units(text: &[char], p: usize, plural_simple: bool, complex: Option<bool>) -> Vec<Hit> {
    let nb = '\u{a0}';
    let mut simple: Vec<Hit> = Vec::new();
    for (r, e) in UNIT.prefix_matches(text, p) {
        if plural_simple {
            for (pl, c) in plural(&r.value) {
                simple.push((e, space_to_nbsp(&pl), r.cost + c));
            }
        } else {
            simple.push((e, space_to_nbsp(&r.value), r.cost));
        }
    }
    let mut out: Vec<Hit> = simple.clone();
    if let Some(pl) = complex {
        // denominator: "/" unit_singular
        let denom = |q: usize| -> Vec<Hit> {
            if text.get(q) != Some(&'/') {
                return Vec::new();
            }
            UNIT.prefix_matches(text, q + 1)
                .map(|(r, e)| (e, format!("por{nb}{}", space_to_nbsp(&r.value)), r.cost))
                .collect()
        };
        for (e, u, c) in &simple {
            for (de, d, dc) in denom(*e) {
                out.push((de, format!("{u}{nb}{d}"), c + dc));
            }
        }
        out.extend(denom(p));
        for (r, e) in UNIT_COMPLEX.prefix_matches(text, p) {
            if pl {
                for (pv, c) in plural(&r.value) {
                    out.push((e, space_to_nbsp(&pv), r.cost + c));
                }
            } else {
                out.push((e, space_to_nbsp(&r.value), r.cost));
            }
        }
    }
    out.into_iter().map(|(e, u, c)| (e, format!("units: \"{u}\""), c)).collect()
}

fn ws_then(text: &[char], p: usize) -> Vec<usize> {
    let mut v = vec![p];
    let mut q = p;
    while q < text.len() && is_nemo_space(text[q]) {
        q += 1;
        v.push(q);
    }
    v
}

fn alpha_run(text: &[char], p: usize) -> Vec<usize> {
    let mut e = p;
    let mut v = Vec::new();
    while e < text.len() && is_alpha(text[e]) {
        e += 1;
        v.push(e);
    }
    v
}

/// Cardinal tag bodies at `p` (with optional "-"): `(end, "cardinal { … }", cost, is_one)`.
fn cardinals(text: &[char], p: usize) -> Vec<(usize, String, Cost, bool)> {
    let (neg, s) = if text.get(p) == Some(&'-') { ("negative: \"true\" ", p + 1) } else { ("", p) };
    let mut out = Vec::new();
    if s >= text.len() || !is_digit(text[s]) {
        return out;
    }
    for e in cardinal::number_ends(text, s) {
        if let Some((w, c)) = cardinal::graph(&text[s..e]) {
            let one = e == s + 1 && text[s] == '1';
            out.push((e, format!("cardinal {{ {neg}integer: \"{w}\" }}"), c, one));
        }
    }
    out
}

fn math_numbers(text: &[char], p: usize) -> Vec<Hit> {
    let mut out = Vec::new();
    if p >= text.len() {
        return out;
    }
    if is_digit(text[p]) {
        for e in cardinal::number_ends(text, p) {
            if let Some((w, c)) = cardinal::graph(&text[p..e]) {
                if &*w == "un" {
                    out.push((e, "uno".into(), c + cost(-1.0)));
                }
                out.push((e, w.to_string(), c));
            }
        }
    }
    if is_alpha(text[p]) {
        out.push((p + 1, text[p].to_string(), 0));
    }
    out
}

/// `delimiter`: " " kept or a space inserted.
fn delim(text: &[char], p: usize) -> Vec<usize> {
    if text.get(p) == Some(&' ') { vec![p, p + 1] } else { vec![p] }
}

fn math_symbol(text: &[char], p: usize) -> Vec<Hit> {
    MATH_SYMBOLS.prefix_matches(text, p).map(|(r, e)| (e, r.value.clone(), r.cost)).collect()
}

/// `(delimiter + sym + delimiter + num)+` from each state; returns all extensions (at least one).
fn chain(text: &[char], seeds: Vec<Hit>) -> Vec<Hit> {
    let mut out = Vec::new();
    let mut frontier = seeds;
    while !frontier.is_empty() {
        let mut next = Vec::new();
        for (e, s, c) in &frontier {
            for d1 in delim(text, *e) {
                for (se, sym, sc) in math_symbol(text, d1) {
                    for d2 in delim(text, se) {
                        for (ne, n, nc) in math_numbers(text, d2) {
                            next.push((ne, format!("{s} {sym} {n}"), c + sc + nc));
                        }
                    }
                }
            }
        }
        out.extend(next.iter().cloned());
        frontier = next;
    }
    out
}

fn math(text: &[char], start: usize) -> Vec<Hit> {
    let mut out = Vec::new();
    // both forms contain "=" inside the expression: operands are digits / separators / one letter, operators come
    // from math_symbols, delimiters are single spaces
    let mut q = start;
    let mut eq = false;
    while q < text.len() {
        let c = text[q];
        if c == '=' {
            eq = true;
            break;
        }
        if is_digit(c) || is_alpha(c) || matches!(c, '.' | ' ') || MATH_SYMBOLS.prefix_matches(text, q).next().is_some()
        {
            q += 1;
        } else {
            break;
        }
    }
    if !eq {
        return out;
    }
    let firsts = math_numbers(text, start);
    // form 1: num (sym num)+ = num
    for (e, s, c) in chain(text, firsts.clone()) {
        for d1 in delim(text, e) {
            if text.get(d1) != Some(&'=') {
                continue;
            }
            for d2 in delim(text, d1 + 1) {
                for (ne, n, nc) in math_numbers(text, d2) {
                    out.push((ne, format!("{s} es igual a {n}"), c + nc));
                }
            }
        }
    }
    // form 2: num = num (sym num)+
    for (e, s, c) in firsts {
        for d1 in delim(text, e) {
            if text.get(d1) != Some(&'=') {
                continue;
            }
            let seeds: Vec<Hit> = math_numbers(text, d1 + 1)
                .into_iter()
                .map(|(ne, n, nc)| (ne, format!("{s} es igual a {n}"), c + nc))
                .collect();
            out.extend(chain(text, seeds));
        }
    }
    out
}

pub struct Measure;

impl Measure {
    fn graph(&self, text: &[char], start: usize) -> Vec<(usize, String, Cost, i32)> {
        let mut out = Vec::new();
        let unit_at = |p: usize| unit_at(text, p);
        let unit_after = |e: usize| (e..=skip_ws(text, e)).any(unit_at);
        // subgraph_decimal: decimal.fst + " "? + (unit_plural | complex_plural)
        let (dneg, ds) = if text[start] == '-' { ("negative: \"true\" ", start + 1) } else { ("", start) };
        let decs =
            if ds < text.len() && is_digit(text[ds]) { decimals::wo_negative(text, ds) } else { Default::default() };
        for d in decs.iter().filter(|d| unit_after(d.end)) {
            let (e, c) = (&d.end, d.cost);
            let dec = format!("decimal {{ {dneg}{} preserve_order: true }}", d.body(text, ds));
            let ps = if text.get(*e) == Some(&' ') { vec![*e, e + 1] } else { vec![*e] };
            for p in ps {
                for (ue, u, uc) in units(text, p, true, Some(true)) {
                    out.push((ue, format!("{dec} {u}"), c + uc, 0));
                }
            }
        }
        // subgraph_cardinal
        let card_unit = cardinal::number_ends(text, if text[start] == '-' { start + 1 } else { start }).any(unit_after);
        for (e, b, c, one) in if card_unit { cardinals(text, start) } else { Vec::new() } {
            for p in ws_then(text, e) {
                for (ue, u, uc) in units(text, p, !one, Some(!one)) {
                    out.push((ue, format!("{b} {u}"), c + uc, 1));
                }
            }
        }
        // cardinal_dash_alpha / cardinal_times
        if is_digit(text[start]) {
            for e in cardinal::number_ends(text, start) {
                if !matches!(text.get(e), Some('-' | 'x' | 'X')) {
                    continue;
                }
                let Some((w, c)) = cardinal::graph(&text[start..e]) else {
                    continue;
                };
                let w = strip_cardinal_apocope(&w);
                if text.get(e) == Some(&'-') {
                    for ae in alpha_run(text, e + 1) {
                        let a: String = text[e + 1..ae].iter().collect();
                        out.push((ae, format!("cardinal {{ integer: \"{w}\" }} units: \"{a}\""), c, 2));
                    }
                }
                if matches!(text.get(e), Some('x') | Some('X')) {
                    out.push((e + 1, format!("cardinal {{ integer: \"{w}\" }} units: \"{}\"", text[e]), c, 7));
                }
            }
            // decimal_dash_alpha / decimal_times
            for d in decimals::wo_negative(text, start).iter() {
                let (e, c) = (d.end, d.cost);
                if text.get(e) == Some(&'-') {
                    let b = d.body(text, start);
                    for ae in alpha_run(text, e + 1) {
                        let a: String = text[e + 1..ae].iter().collect();
                        out.push((ae, format!("decimal {{ {b} }} units: \"{a}\""), c, 4));
                    }
                }
                if matches!(text.get(e), Some('x') | Some('X')) {
                    out.push((e + 1, format!("decimal {{ {} }} units: \"{}\"", d.body(text, start), text[e]), c, 6));
                }
            }
        }
        // alpha_dash_cardinal / alpha_dash_decimal
        for ae in alpha_run(text, start) {
            if text.get(ae) != Some(&'-') {
                continue;
            }
            let a: String = text[start..ae].iter().collect();
            let p = ae + 1;
            if p < text.len() && is_digit(text[p]) {
                for e in cardinal::number_ends(text, p) {
                    if let Some((w, c)) = cardinal::graph(&text[p..e]) {
                        out.push((
                            e,
                            format!("units: \"{a}\" cardinal {{ integer: \"{w}\" }} preserve_order: true"),
                            c,
                            3,
                        ));
                    }
                }
                for d in decimals::wo_negative(text, p).iter() {
                    let b = d.body(text, p);
                    out.push((d.end, format!("units: \"{a}\" decimal {{ {b} }} preserve_order: true"), d.cost, 8));
                }
            }
        }
        // subgraph_fraction (only fractions followed by a unit, a "/unit" denominator or a complex unit)
        // a fraction span is digits, "-", spaces, "/", "." and fraction symbols; a unit must follow somewhere after it
        let mut q = start;
        while q < text.len()
            && (is_digit(text[q]) || matches!(text[q], '-' | ' ' | '/' | '.') || FRACTION_FIRST.contains(&text[q]))
        {
            q += 1;
        }
        let any_unit = (start + 1..=q.min(text.len())).any(unit_at);
        let fractions = if any_unit { fraction::graph(text, start) } else { Vec::new() };
        for (e, b, c, r) in fractions {
            if !ws_then(text, e).into_iter().any(unit_at) {
                continue;
            }
            for p in ws_then(text, e) {
                // complex_unit_singular_graph wins ties over unit_plural (oracle: "3 3/5 m" → "metro")
                for (k, us) in [units(text, p, false, Some(false)), units(text, p, true, None)].into_iter().enumerate()
                {
                    for (ue, u, uc) in us {
                        out.push((ue, format!("fraction {{ {b} }} {u}"), c + uc, 50 + r * 2 + k as i32));
                    }
                }
            }
        }
        // math
        for (e, m, c) in math(text, start) {
            out.push((e, format!("units: \"math\" cardinal {{ integer: \"{m}\" }} preserve_order: true"), c, 9));
        }
        out
    }
}

impl Lazy for Measure {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let c = text[start];
        if !(is_digit(c) || is_alpha(c) || c == '-' || FRACTION_FIRST.contains(&c)) {
            return;
        }
        if is_alpha(c) {
            // alpha-led branches: `alpha+ "-" number` or math whose first operand is one letter
            let mut e = start;
            while e < text.len() && is_alpha(text[e]) {
                e += 1;
            }
            let dash = text.get(e) == Some(&'-');
            let math_next = |k: usize| {
                let k = if text.get(k) == Some(&' ') { k + 1 } else { k };
                text.get(k) == Some(&'=') || MATH_SYMBOLS.prefix_matches(text, k).next().is_some()
            };
            if !dash && !(e == start + 1 && math_next(e)) {
                return;
            }
        }
        if is_digit(c) || c == '-' {
            // number-led branches: after the number run (digits . , / space) comes a unit, x/X, "-", a math
            // symbol, "=" or a quantity word
            let mut q = start + 1;
            while q < text.len() && (is_digit(text[q]) || matches!(text[q], '.' | ',' | '/' | ' ' | '\u{a0}')) {
                q += 1;
            }
            let p = q.min(text.len());
            let found = (start + 1..=p).any(|k| {
                AFTER_NUMBER.at(text, k)
                    && (matches!(text[k], 'x' | 'X' | '-' | '=')
                        || UNIT.prefix_matches(text, k).next().is_some()
                        || UNIT_COMPLEX.prefix_matches(text, k).next().is_some()
                        || MATH_SYMBOLS.prefix_matches(text, k).next().is_some()
                        || decimals::QUANTITIES.prefix_matches(text, k).next().is_some())
            });
            if !found && !FRACTION_FIRST.contains(&c) {
                return;
            }
        }
        for (end, b, c, rank) in self.graph(text, start) {
            out.push(end, cost(1.08) + c, rank, || format!("measure {{ {b} }}"));
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::lang::es::test_util::tags;

    #[test]
    fn fraction_takes_singular_unit() {
        // alternatives: units "metro" (complex_unit_singular_graph, k = 0) vs "metros" (unit_plural, k = 1)
        assert_eq!(
            tags("3 3/5 m"),
            "tokens { measure { fraction { integer_part: \"tres\" numerator: \"tres\" denominator: \"quinto\" morphosyntactic_features: \"ordinal\" } units: \"metro\" } }"
        );
    }
}

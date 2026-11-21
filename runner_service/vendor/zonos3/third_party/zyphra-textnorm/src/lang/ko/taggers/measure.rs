//! `ko/taggers/measure.py`.

use std::sync::LazyLock;

use crate::lang::ko::graph_utils::*;
use crate::lang::ko::taggers::{cardinal, decimal, fraction};
use crate::lang::ko::{At, FRACTION};
use crate::text::{Table, is_nemo_space, match_str};

static UNIT: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("measure/unit.tsv")));

/// `opt_space = closure(delete_space, 0, 1)` ends.
fn opt_space(t: &[char], p: usize) -> std::ops::RangeInclusive<usize> {
    p..=run(t, p, is_nemo_space)
}

/// `per = "/" → "퍼" + opt_space + " " + graph_unit`.
fn per(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    if t.get(p) != Some(&'/') {
        return r;
    }
    for k in opt_space(t, p + 1) {
        for (row, e) in UNIT.prefix_matches(t, k) {
            add(&mut r, e, format!("퍼 {}", row.value), row.cost);
        }
    }
    r
}

/// `units: "<graph_unit + optional_per | per>"`.
fn unit(t: &[char], p: usize) -> Outs {
    let mut vals = Outs::new();
    for (row, e) in UNIT.prefix_matches(t, p) {
        add(&mut vals, e, row.value.clone(), row.cost);
        for k in opt_space(t, e) {
            for (e2, s2, c2) in per(t, k) {
                add(&mut vals, e2, format!("{} {s2}", row.value), row.cost + c2);
            }
        }
    }
    for x in per(t, p) {
        add(&mut vals, x.0, x.1, x.2);
    }
    vals.into_iter().map(|(e, s, c)| (e, format!("units: \"{s}\""), c)).collect()
}

/// `x + delete_space + "<close>" + unit`.
fn with_unit(t: &[char], x: Outs, open: &str, close: &str) -> Outs {
    let mut r = Outs::new();
    for (e, s, c) in x {
        for k in e..=run(t, e, is_nemo_space) {
            for (e2, u, c2) in unit(t, k) {
                add(&mut r, e2, format!("{open}{s}{close}{u}"), c + c2);
            }
        }
    }
    r
}

/// Whether a unit (or `/` of a bare `per`) can start after the deletable spaces at `e`.
fn unit_follows(t: &[char], e: usize) -> bool {
    (e..=run(t, e, is_nemo_space)).any(|k| t.get(k) == Some(&'/') || UNIT.prefix_matches(t, k).next().is_some())
}

/// Necessary condition for a measure at a digit: a cardinal or decimal ends at the end of its digit run (units
/// never start with a digit) and must be followed by a unit, unless a fraction starts here.
pub fn digit_gate(a: &At) -> bool {
    let t = a.t;
    unit_follows(t, a.d) || (a.u == Some('.') && unit_follows(t, run(t, a.d + 1, is_digit))) || a.flags & FRACTION != 0
}

pub fn tag(t: &[char], p: usize) -> Outs {
    let Some(&c) = t.get(p) else { return Outs::new() };
    if !(c.is_ascii_digit() || matches!(c, '-' | '마' | '√')) {
        return Outs::new();
    }

    let card = with_unit(t, cardinal::graph_if(t, p, |e| unit_follows(t, e)), "cardinal { integer: \"", "\" } ");
    // optional_minus = negative field + ("-" | "마이너스") + opt_space
    let keep = |e: usize| unit_follows(t, e);
    let mut decs = decimal::just_decimal_if(t, p, keep);
    let minus = if c == '-' { Some(p + 1) } else { match_str(t, p, "마이너스").map(|k| p + k) };
    if let Some(q) = minus {
        for k in opt_space(t, q) {
            for (e, s, c2) in decimal::just_decimal_if(t, k, keep) {
                add(&mut decs, e, format!("negative: \"마이너스\" {s}"), c2);
            }
        }
    }
    let dec = with_unit(t, decs, "decimal { ", " } ");
    let frac = with_unit(t, fraction::graph(t, p), "fraction { ", " } ");
    alt([card, dec, frac])
}

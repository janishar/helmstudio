//! `it/taggers/measure.py`, including the stray `"` of `decimal_dash_alpha`/`decimal_times` (the parser then
//! fails on those taggings exactly as the oracle's does).

use std::sync::LazyLock;

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Table, is_nemo_space, space_to_nbsp};

use super::cardinal;
use super::decimals;

crate::tsv!(UNIT_SINGULAR, "it/measure/measurements.tsv");
crate::tsv!(SUPPLETIVE, "it/measure/suppletive.tsv");

/// `convert_space(unit_singular)`. Two keys have two rows (`kb` kilobit/kilobyte, `mv` millivolt/megavolt) at
/// equal weight; the oracle's FST yields the lexicographically greater output for both (golden `8 kb` →
/// "kilobytes", `228724mv` → "millivolt"), so only that row is kept.
static SINGULAR: LazyLock<Table> = LazyLock::new(|| {
    let mut rows: Vec<crate::text::Row> = Vec::new();
    for r in UNIT_SINGULAR.rows() {
        let value = space_to_nbsp(&r.value);
        match rows.iter_mut().find(|x| x.key == r.key && x.cost == r.cost) {
            Some(x) if x.value < value => x.value = value,
            Some(_) => {}
            None => rows.push(crate::text::Row { key: r.key.clone(), value, cost: r.cost }),
        }
    }
    Table::from_rows(rows)
});

/// `graph_unit_plural`: `convert_space(suppletive)` | singular units with the suppletive rewrite at EOS.
static PLURAL: LazyLock<Table> = LazyLock::new(|| {
    let mut rows: Vec<crate::text::Row> = SUPPLETIVE
        .rows()
        .iter()
        .map(|r| crate::text::Row { key: r.key.clone(), value: space_to_nbsp(&r.value), cost: r.cost })
        .collect();
    for r in SINGULAR.rows() {
        let v = &r.value;
        // cdrewrite(τ, "", [EOS]): the leftmost key occurrence that ends the string
        let mut value = v.clone();
        'pos: for (i, _) in v.char_indices() {
            for s in SUPPLETIVE.rows() {
                if v[i..] == *s.key {
                    value = format!("{}{}", &v[..i], space_to_nbsp(&s.value));
                    break 'pos;
                }
            }
        }
        rows.push(crate::text::Row { key: r.key.clone(), value, cost: r.cost });
    }
    Table::from_rows(rows)
});

/// `("/" → "per") + NBSP + singular`: `f(end, singular unit, cost)`.
fn denominator(text: &[char], p: usize, mut f: impl FnMut(usize, &str, Cost)) {
    if text.get(p) != Some(&'/') {
        return;
    }
    for (r, e) in SINGULAR.prefix_matches(text, p + 1) {
        f(e, &r.value, r.cost);
    }
}

/// `unit_plural` / `unit_singular_graph` bodies at `p`: `f(end, pieces, cost)`, the unit being the pieces
/// concatenated (`<unit>`, `<unit> NBSP per NBSP <den>` or `per NBSP <den>`).
fn units(text: &[char], p: usize, plural: bool, f: &mut dyn FnMut(usize, &[&str], Cost)) {
    let table: &Table = if plural { &PLURAL } else { &SINGULAR };
    for (r, e) in table.prefix_matches(text, p) {
        f(e, &[&r.value], r.cost);
        denominator(text, e, |e2, d, c| f(e2, &[&r.value, "\u{a0}per\u{a0}", d], r.cost + c));
    }
    denominator(text, p, |e, d, c| f(e, &["per\u{a0}", d], c));
}

/// The first chars of every unit key, and `/` (the denominator form).
static UNIT_FIRST: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> =
        PLURAL.rows().iter().chain(SINGULAR.rows()).filter_map(|r| r.key.chars().next()).chain(['/']).collect();
    v.sort_unstable();
    v.dedup();
    v
});

/// Whether [`units`] accepts anything at `p` or after one space there.
fn unit_follows(text: &[char], p: usize, plural: bool) -> bool {
    let at = |q: usize| {
        let mut any = false;
        if text.get(q).is_some_and(|c| UNIT_FIRST.binary_search(c).is_ok()) {
            units(text, q, plural, &mut |_, _, _| any = true);
        }
        any
    };
    at(p) || (text.get(p) == Some(&' ') && at(p + 1))
}

/// What may follow a measure's number run: the first char of a unit key (or `/`) or of a quantity key, `-`, `x`,
/// `X`.
static FOLLOW: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = UNIT_FIRST.iter().copied().chain(['-', 'x', 'X']).collect();
    v.extend(decimals::QUANTITIES.rows().iter().filter_map(|r| r.key.chars().next()));
    v.sort_unstable();
    v.dedup();
    v
});

fn alpha_run(text: &[char], p: usize) -> usize {
    let mut e = p;
    while e < text.len() && text[e].is_ascii_alphabetic() {
        e += 1;
    }
    e
}

pub struct Measure {
    pub cost: Cost,
}

/// Writes a part of the tag body.
type Body<'a> = &'a dyn Fn(&mut String);

impl Measure {
    /// `measure { <body> }`.
    fn push(&self, out: &mut Vec<Candidate>, end: usize, cost: Cost, body: Body) {
        let tag = super::tag(end, 128, |t| {
            t.push_str("measure { ");
            body(t);
            t.push_str(" }");
        });
        out.push(Candidate { end, cost: self.cost + cost, rank: 0, tag });
    }

    /// `insert_space + delete(" ")? + unit` after the number tag `num` (cost `c`) ending at `p`.
    fn with_units(&self, text: &[char], p: usize, plural: bool, out: &mut Vec<Candidate>, num: Body, c: Cost) {
        for q in [p, p + 1] {
            if q == p + 1 && text.get(p) != Some(&' ') {
                break;
            }
            units(text, q, plural, &mut |e, u, uc| {
                self.push(out, e, c + uc, &|t| {
                    num(t);
                    t.push_str(" units: \"");
                    for piece in u {
                        t.push_str(piece);
                    }
                    t.push('"');
                });
            });
        }
    }
}

impl Classifier for Measure {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let neg = text[start] == '-';
        let p = start + neg as usize;
        let a = alpha_run(text, start);
        let alpha_dash = a > start && text.get(a) == Some(&'-');
        if !text.get(p).is_some_and(char::is_ascii_digit) && !alpha_dash {
            return;
        }
        // Every number-led form ends its number inside the run of digits, separators and whitespace at `p` (or
        // after a quantity word); what follows (after at most one space, itself in the run) starts at the run's
        // end: a unit, `/`, `-`, `x`/`X` or a quantity word. No unit or quantity key starts with a run char.
        if !alpha_dash {
            let mut r = p;
            while r < text.len() && (text[r].is_ascii_digit() || matches!(text[r], '.' | ',') || is_nemo_space(text[r]))
            {
                r += 1;
            }
            if !text.get(r).is_some_and(|c| FOLLOW.binary_search(c).is_ok()) {
                return;
            }
        }
        let sign = if neg { "negative: \"true\" " } else { "" };
        let units_of = |t: &mut String, a: usize, b: usize| {
            t.push_str(" units: \"");
            t.extend(&text[a..b]);
            t.push('"');
        };
        if p < text.len() {
            // subgraph_decimal
            for d in decimals::readings(text, p).iter() {
                if unit_follows(text, d.end, true) {
                    let num = |t: &mut String| {
                        t.push_str("decimal { ");
                        t.push_str(sign);
                        d.fields(text, t);
                        t.push_str(" preserve_order: true }");
                    };
                    self.with_units(text, d.end, true, out, &num, d.cost);
                }
            }
            // subgraph_cardinal: plain digits only
            cardinal::plain_spans(text, p, |n| {
                let one = n.digits() == b"1";
                if !unit_follows(text, n.end, !one) {
                    return;
                }
                let Some(c) = cardinal::cost_of(n.digits()) else { return };
                let num = |t: &mut String| {
                    t.push_str("cardinal { ");
                    t.push_str(sign);
                    t.push_str("integer: \"");
                    if one {
                        t.push_str("un");
                    } else {
                        cardinal::words_into(n.digits(), t);
                    }
                    t.push_str("\" }");
                };
                self.with_units(text, n.end, !one, out, &num, c);
            });
        }
        // the unsigned dash / times forms
        cardinal::spans(text, start, |n| {
            let Some(&x @ ('-' | 'x' | 'X')) = text.get(n.end) else { return };
            let e = if x == '-' { alpha_run(text, n.end + 1) } else { n.end + 1 };
            if x == '-' && e < n.end + 2 {
                return;
            }
            let Some(c) = cardinal::cost_of(n.digits()) else { return };
            let ends = if x == '-' { n.end + 2..=e } else { n.end + 1..=n.end + 1 };
            let from = if x == '-' { n.end + 1 } else { n.end };
            for e in ends {
                self.push(out, e, c, &|t| {
                    t.push_str("cardinal { integer: \"");
                    cardinal::words_into(n.digits(), t);
                    t.push_str("\" }");
                    units_of(t, from, e);
                });
            }
        });
        if text[start].is_ascii_digit() {
            for d in decimals::readings(text, start).iter() {
                let Some(&x @ ('-' | 'x' | 'X')) = text.get(d.end) else { continue };
                let (from, ends) = if x == '-' {
                    (d.end + 1, d.end + 2..=alpha_run(text, d.end + 1))
                } else {
                    (d.end, d.end + 1..=d.end + 1)
                };
                for e in ends {
                    self.push(out, e, d.cost, &|t| {
                        t.push_str("decimal { ");
                        d.fields(text, t);
                        t.push_str("\" }");
                        units_of(t, from, e);
                    });
                }
            }
        }
        // alpha_dash_cardinal / alpha_dash_decimal
        if alpha_dash {
            let units_first = |t: &mut String| {
                t.push_str("units: \"");
                t.extend(&text[start..a]);
                t.push_str("\" ");
            };
            cardinal::spans(text, a + 1, |n| {
                if let Some(c) = cardinal::cost_of(n.digits()) {
                    self.push(out, n.end, c, &|t| {
                        units_first(t);
                        t.push_str("cardinal { integer: \"");
                        cardinal::words_into(n.digits(), t);
                        t.push_str("\" }");
                    });
                }
            });
            for d in decimals::readings(text, a + 1).iter() {
                self.push(out, d.end, d.cost, &|t| {
                    units_first(t);
                    t.push_str("decimal { ");
                    d.fields(text, t);
                    t.push_str(" }");
                });
            }
        }
    }
}

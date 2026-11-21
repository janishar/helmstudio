//! `de/taggers/measure.py`: `MeasureFst`.
//!
//! `measurements.tsv` maps `kb` to `kilobit`/`kilobyte` and `mv` to `millivolt`/`megavolt` at equal cost; the oracle
//! shows `kilobyte` and `millivolt`. Units are emitted with all their values; the per-key preference below follows the
//! golden (`KB_PICK`).

use std::sync::LazyLock;

use super::cardinal::{self, WEIGHT, digit_end};
use super::decimal::{self, Dec};
use super::fraction::{self, Frac};
use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, Row, Table, space_to_nbsp};

crate::tsv!(UNITS_RAW, "de/measure/measurements.tsv");
crate::tsv!(SUPPLETIVE, "de/measure/suppletive.tsv");

/// One value per key: the cheapest row, among equal costs the golden's choice (`kb` → kilobyte, `mv` → millivolt).
static UNITS: LazyLock<Table> = LazyLock::new(|| {
    let mut rows: Vec<Row> = Vec::new();
    for r in UNITS_RAW.rows() {
        match rows.iter_mut().find(|x| x.key == r.key) {
            Some(x) => {
                let prefer = (r.key == "kb" && r.value == "kilobyte") || r.cost < x.cost;
                if prefer {
                    *x = r.clone();
                }
            }
            None => rows.push(r.clone()),
        }
    }
    for r in &mut rows {
        r.value = space_to_nbsp(&r.value);
    }
    Table::from_rows(rows)
});

/// `graph_unit_plural`: `cdrewrite(suppletive, "", EOS)` on the converted singular.
fn plural(v: &str) -> String {
    for r in SUPPLETIVE.rows() {
        if let Some(stem) = v.strip_suffix(r.key.as_str()) {
            return format!("{stem}{}", r.value);
        }
    }
    v.to_string()
}

/// One unit reading: `unit (nbsp "/" unit)?` or `"/" unit` (`(first, denominator)` rows of [`UNITS`]).
#[derive(Clone, Copy)]
struct Unit<'a> {
    first: Option<&'a Row>,
    den: Option<&'a Row>,
    plural: bool,
}

impl Unit<'_> {
    fn write(&self, out: &mut String) {
        if let Some(r) = self.first {
            out.push_str(&if self.plural { plural(&r.value) } else { r.value.clone() });
            if self.den.is_some() {
                out.push('\u{a0}');
            }
        }
        if let Some(r) = self.den {
            out.push_str("pro\u{a0}");
            out.push_str(&r.value);
        }
    }
}

/// Unit spans at `s`: `f(end, cost, unit)`.
fn units<'a>(text: &'a [char], s: usize, plural_form: bool, f: &mut dyn FnMut(usize, Cost, Unit<'a>)) {
    let units: &'static Table = &UNITS;
    let den = |at: usize, f: &mut dyn FnMut(usize, Cost, Unit<'a>), first: Option<&'a Row>, pc: Cost| {
        if text.get(at) != Some(&'/') {
            return;
        }
        for (r, e) in units.prefix_matches(text, at + 1) {
            if e > at + 1 {
                f(e, pc + r.cost, Unit { first, den: Some(r), plural: plural_form });
            }
        }
    };
    for (r, e) in units.prefix_matches(text, s) {
        if e == s {
            continue;
        }
        den(e, f, Some(r), r.cost);
        f(e, r.cost, Unit { first: Some(r), den: None, plural: plural_form });
    }
    den(s, f, None, 0);
}

fn alpha_end(text: &[char], s: usize) -> usize {
    let mut e = s;
    while e < text.len() && text[e].is_ascii_alphabetic() {
        e += 1;
    }
    e
}

/// Cheap precheck. An alphabetic start needs `alpha+ "-" digit` (alpha_dash_*). A number start reads a run of digits
/// and `, . / -`; no unit key starts with one of those except `/`, so (barring a `/` in the run) the unit, the
/// quantity before it, the `x`/`X` of the times branches or the letters after a `-` all start right after the run
/// (units and quantities also after one space).
fn may_have_unit(text: &[char], start: usize) -> bool {
    if text[start].is_ascii_alphabetic() {
        let a = alpha_end(text, start);
        return text.get(a) == Some(&'-') && text.get(a + 1).is_some_and(char::is_ascii_digit);
    }
    let mut i = start;
    while i < text.len() && (text[i].is_ascii_digit() || matches!(text[i], ',' | '.' | '/' | '-')) {
        if text[i] == '/' {
            return true;
        }
        i += 1;
    }
    let Some(&c) = text.get(i) else { return false };
    if matches!(c, 'x' | 'X') || (i > start && text[i - 1] == '-' && c.is_ascii_alphabetic()) {
        return true;
    }
    let u = if c == ' ' { i + 1 } else { i };
    UNITS.prefix_matches(text, u).any(|(_, e)| e > u)
        || (c == ' ' && decimal::QUANTITIES.prefix_matches(text, u).any(|(_, e)| e > u))
}

pub struct Measure;

/// The number part of a measure reading.
#[derive(Clone, Copy)]
enum Num {
    Decimal(Dec),
    /// `cardinal::fst` on `text[s..e]`; `one` reads `1` as `ein`.
    Cardinal {
        s: usize,
        e: usize,
        one: bool,
    },
    Fraction(Frac),
}

impl Num {
    fn write(&self, text: &[char], out: &mut String) {
        match self {
            Num::Decimal(d) => {
                out.push_str("decimal { ");
                d.fields(text, out);
            }
            Num::Cardinal { s, e, one } => {
                out.push_str("cardinal { ");
                let mut f = String::new();
                cardinal::fields(text, *s, *e, &mut f);
                out.push_str(&if *one { f.replace("eins", "ein") } else { f });
            }
            Num::Fraction(fr) => {
                out.push_str("fraction { ");
                fr.fields(text, out);
            }
        }
        out.push_str(" }");
    }
}

impl Spans for Measure {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit() || c == '-' || c.is_ascii_alphabetic()
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        let c0 = text[start];
        if !(c0.is_ascii_digit() || c0 == '-' || c0.is_ascii_alphabetic()) || !may_have_unit(text, start) {
            return;
        }
        let wrap = |f: &dyn Fn(&mut String)| {
            let mut t = String::from("measure { ");
            f(&mut t);
            t.push_str(" preserve_order: true }");
            t
        };
        // after a number: insert_space + delete(" ")? + unit
        let with_units = |ne: usize, num: Num, c: Cost, plural_form: bool, out: &mut Sink| {
            for us in [ne, ne + 1] {
                if us == ne + 1 && text.get(ne) != Some(&' ') {
                    continue;
                }
                units(text, us, plural_form, &mut |e, uc, u| {
                    out.push(e, WEIGHT + c + uc, || {
                        wrap(&|t| {
                            num.write(text, t);
                            t.push_str(" units: \"");
                            u.write(t);
                            t.push('"');
                        })
                    })
                });
            }
        };
        // a unit (or "/" unit) may follow at `e`, directly or after one space
        let unit_at = |p: usize| text.get(p) == Some(&'/') || UNITS.prefix_matches(text, p).any(|(_, e)| e > p);
        let keep = |e: usize| unit_at(e) || (text.get(e) == Some(&' ') && unit_at(e + 1));
        // the dash/times branches need `-`, `x` or `X` after the number
        let joint = |e: usize| matches!(text.get(e), Some('-' | 'x' | 'X'));
        // `units: "<text[a..b]>"` after a number
        let joined = |num: &dyn Fn(&mut String), a: usize, b: usize| {
            wrap(&|t| {
                num(t);
                t.push_str(" units: \"");
                t.extend(&text[a..b]);
                t.push('"');
            })
        };
        if c0.is_ascii_digit() || c0 == '-' {
            decimal::fst(text, start, &keep, &mut |e, c, d| with_units(e, Num::Decimal(d), c, true, out));
            cardinal::fst(text, start, &keep, &mut |e, c| {
                let one = &text[start + (c0 == '-') as usize..e] == ['1'];
                with_units(e, Num::Cardinal { s: start, e, one }, c, !one, out)
            });
            fraction::fst(text, start, &keep, &mut |e, c, fr| with_units(e, Num::Fraction(fr), c, true, out));
        }
        if c0.is_ascii_digit() {
            // cardinal_dash_alpha, cardinal_times
            let de = digit_end(text, start);
            for e in start + 1..=de.min(start + 24) {
                if !joint(e) {
                    continue;
                }
                let Some(c) = cardinal::graph_cost(&text[start..e]) else { continue };
                let num = |t: &mut String| Num::Cardinal { s: start, e, one: false }.write(text, t);
                if text.get(e) == Some(&'-') {
                    let ae = alpha_end(text, e + 1);
                    for a in e + 2..=ae {
                        out.push(a, WEIGHT + c, || joined(&num, e + 1, a));
                    }
                }
                if matches!(text.get(e), Some('x' | 'X')) {
                    out.push(e + 1, WEIGHT + c, || joined(&num, e, e + 1));
                }
            }
            // decimal_dash_alpha, decimal_times
            decimal::wo_negative(text, start, &joint, &mut |e, c, d| {
                let num = |t: &mut String| {
                    t.push_str("decimal { ");
                    d.body(text, t);
                    t.push_str(" }");
                };
                if text.get(e) == Some(&'-') {
                    let ae = alpha_end(text, e + 1);
                    for a in e + 2..=ae {
                        out.push(a, WEIGHT + c, || joined(&num, e + 1, a));
                    }
                }
                if matches!(text.get(e), Some('x' | 'X')) {
                    out.push(e + 1, WEIGHT + c, || joined(&num, e, e + 1));
                }
            });
        }
        if c0.is_ascii_alphabetic() {
            // alpha_dash_cardinal, alpha_dash_decimal
            let ae = alpha_end(text, start);
            if text.get(ae) == Some(&'-') {
                let ds = ae + 1;
                let pre = |t: &mut String| {
                    t.push_str("units: \"");
                    t.extend(&text[start..ae]);
                    t.push_str("\" ");
                };
                let de = digit_end(text, ds);
                for e in ds + 1..=de.min(ds + 24) {
                    if let Some(c) = cardinal::graph_cost(&text[ds..e]) {
                        out.push(e, WEIGHT + c, || {
                            wrap(&|t| {
                                pre(t);
                                Num::Cardinal { s: ds, e, one: false }.write(text, t);
                            })
                        });
                    }
                }
                decimal::wo_negative(text, ds, &|_| true, &mut |e, c, d| {
                    out.push(e, WEIGHT + c, || {
                        wrap(&|t| {
                            pre(t);
                            t.push_str("decimal { ");
                            d.body(text, t);
                            t.push_str(" }");
                        })
                    })
                });
            }
        }
    }
}

//! `en/taggers/decimal.py` (deterministic).

use crate::lang::common::field;
use crate::lang::en::graph_utils::{Outs, row_index};
use crate::lang::en::lazy::{Reading, building};
use crate::lang::en::taggers::cardinal::{digit_comma_end, graph_with_and_best, hundred_component, single_digits};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Table, cost, is_digit};

crate::tsv!(QUANTITIES, "en/number/thousand.tsv");
crate::tsv!(QUANTITIES_ABBR, "en/number/quantity_abbr.tsv");

/// One `final_graph_wo_negative` reading, as char spans into the text: with fractional digits `frac`, the
/// `final_graph_wo_sign` form (integer part `int` named by `graph_with_and`, absent when empty); without, the
/// `cardinal_up_to_hundred quantity` form (`int` named by `hundred_component`). `quant`: the quantity word's
/// table (true: `quantity_abbr.tsv`) and row.
#[derive(Clone, Copy, Debug, Default)]
pub struct DecR {
    int: (u32, u32),
    frac: (u32, u32),
    quant: Option<(bool, u16)>,
}

fn chars(text: &[char], (s, e): (u32, u32)) -> String {
    text[s as usize..e as usize].iter().collect()
}

/// Appends the fields of reading `r` to `s` (`integer_part: "…"  fractional_part: "…"quantity: "…"`, exactly as
/// the FST writes them).
pub fn write_into(s: &mut String, text: &[char], r: DecR) {
    if r.frac.0 < r.frac.1 {
        if r.int.0 < r.int.1 {
            field(s, "integer_part", &graph_with_and_best(&chars(text, r.int)).unwrap_or_default().0);
            s.push(' ');
        }
        s.push(' '); // `point` → insert(" ")
        field(s, "fractional_part", &single_digits(&chars(text, r.frac)).unwrap_or_default());
    } else {
        field(s, "integer_part", &hundred_component(&chars(text, r.int)).unwrap_or_default());
        s.push(' ');
    }
    if let Some((abbr, row)) = r.quant {
        let t: &Table = if abbr { &QUANTITIES_ABBR } else { &QUANTITIES };
        field(s, "quantity", &t.rows()[row as usize].value);
    }
}

/// [`write_into`] a new string while tags are built, else empty.
fn text_of(text: &[char], r: DecR) -> String {
    let mut s = String::new();
    if building() {
        write_into(&mut s, text, r);
    }
    s
}

/// Quantity words at `pos`: `(end, quant)` (see [`DecR`]); `abbr` adds `quantity_abbr.tsv`; `wo_thousand` drops
/// k/K/thousand.
fn quantities(text: &[char], pos: usize, abbr: bool, wo_thousand: bool, f: &mut dyn FnMut(usize, (bool, u16))) {
    let tables: [&Table; 2] = [&QUANTITIES, &QUANTITIES_ABBR];
    for (ti, t) in tables.iter().enumerate() {
        if ti == 1 && !abbr {
            continue;
        }
        for (r, e) in t.prefix_matches(text, pos) {
            if wo_thousand && matches!(r.key.as_str(), "k" | "K" | "thousand") {
                continue;
            }
            f(e, (ti == 1, row_index(t, r)));
        }
    }
}

/// `final_graph_wo_sign`: `(integer_part: "…" " ")? "." → " " fractional_part: "…"`, every span from `start`
/// whose end satisfies `keep` (ends a caller cannot use are never named).
pub fn wo_sign(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool, out: &mut Outs<DecR>) {
    let int_end = digit_comma_end(text, start);
    let mut buf = String::new();
    for e in start..=int_end {
        if text.get(e) != Some(&'.') {
            continue;
        }
        let fs = e + 1;
        let mut fe = fs;
        while fe < text.len() && is_digit(text[fe]) {
            fe += 1;
        }
        if !(fs + 1..=fe).any(keep) {
            continue;
        }
        let c = if e == start {
            0
        } else {
            buf.clear();
            buf.extend(&text[start..e]);
            let Some((_, c)) = graph_with_and_best(&buf) else { continue };
            c
        };
        for fe in (fs + 1..=fe).filter(|&x| keep(x)) {
            let r = DecR { int: (start as u32, e as u32), frac: (fs as u32, fe as u32), quant: None };
            out.add_r(fe, c, text_of(text, r), r);
        }
    }
}

/// Whether a quantity word can start at `p` (or after one space).
fn quantity_at(text: &[char], p: usize, abbr: bool) -> bool {
    let at = |q: usize| {
        QUANTITIES.prefix_matches(text, q).next().is_some()
            || (abbr && QUANTITIES_ABBR.prefix_matches(text, q).next().is_some())
    };
    at(p) || (text.get(p) == Some(&' ') && at(p + 1))
}

/// `final_graph_wo_negative(_w_abbr)`: `wo_sign | get_quantity(...)`, ends filtered by `keep`.
pub fn wo_negative(text: &[char], start: usize, abbr: bool, keep: &dyn Fn(usize) -> bool, out: &mut Outs<DecR>) {
    let mut base = Outs::default();
    wo_sign(text, start, &|e| keep(e) || quantity_at(text, e, abbr), &mut base);
    for ((e, c, s), r) in base.entries() {
        if keep(*e) {
            out.add_r(*e, *c, s.clone(), r);
        }
    }
    for (&(e, c, _), r) in base.entries() {
        for skip in [0, 1] {
            let p = e + skip;
            if skip == 1 && text.get(e) != Some(&' ') {
                continue;
            }
            quantities(text, p, abbr, false, &mut |qe, q| {
                if keep(qe) {
                    let r = DecR { quant: Some(q), ..r };
                    out.add_r(qe, c, text_of(text, r), r);
                }
            });
        }
    }
    // cardinal_up_to_hundred quantity_wo_thousand
    let mut buf = [0u8; 3];
    let mut e = start;
    while e < text.len() && is_digit(text[e]) && e - start < 3 {
        buf[e - start] = text[e] as u8;
        e += 1;
        // `hundred_component` on ASCII digits (never fails to be UTF-8)
        let digits = std::str::from_utf8(&buf[..e - start]).unwrap_or("");
        if !quantity_at(text, e, abbr) || hundred_component(digits).is_none() {
            continue;
        }
        for skip in [0, 1] {
            if skip == 1 && text.get(e) != Some(&' ') {
                continue;
            }
            quantities(text, e + skip, abbr, true, &mut |qe, q| {
                if keep(qe) {
                    let r = DecR { int: (start as u32, e as u32), frac: (0, 0), quant: Some(q) };
                    out.add_r(qe, 0, text_of(text, r), r);
                }
            });
        }
    }
}

pub struct Decimal;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 5;

/// The tag of a decimal token at `start` read as `r`.
fn tag(text: &[char], start: usize, r: DecR) -> String {
    let mut tag = String::with_capacity(96);
    tag.push_str("decimal { ");
    if text[start] == '-' {
        tag.push_str("negative: \"true\" ");
    }
    write_into(&mut tag, text, r);
    tag.push_str(" }");
    tag
}

impl Classifier for Decimal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let neg = text[start] == '-';
        let s0 = if neg { start + 1 } else { start };
        if s0 >= text.len() {
            return;
        }
        if !is_digit(text[s0]) && text[s0] != '.' {
            return;
        }
        let mut o = Outs::default();
        wo_negative(text, s0, false, &|e| crate::lang::en::lazy::keep_end(text, e), &mut o);
        for (&(e, c, _), r) in o.entries() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            let cost = cost(1.1) + c;
            crate::lang::en::lazy::record(MEMBER, start, e, cost, 0, Reading::Decimal(r));
            let tag = if building() { tag(text, start, r) } else { String::new() };
            out.push(Candidate { end: e, cost, rank: 0, tag });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Decimal(r) => Some(tag(text, start, r)),
            _ => None,
        })
    }
}

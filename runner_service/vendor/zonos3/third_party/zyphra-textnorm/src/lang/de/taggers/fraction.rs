//! `de/taggers/fraction.py`: `-? (cardinal " ")? cardinal ("/" | " / ") cardinal`.

use super::cardinal::{WEIGHT, digit_end, graph_cost, write_graph};
use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, match_str};

/// One fraction reading (char ranges into the text), written out by [`Frac::fields`].
#[derive(Clone, Copy)]
pub struct Frac {
    pub neg: bool,
    pub integer: Option<(usize, usize)>,
    pub num: (usize, usize),
    pub den: (usize, usize),
}

impl Frac {
    /// `negative: "true"`? `integer_part: "…"`? `numerator: "…" denominator: "…" preserve_order: true`.
    pub fn fields(&self, text: &[char], out: &mut String) {
        if self.neg {
            out.push_str("negative: \"true\" ");
        }
        if let Some(r) = self.integer {
            out.push_str("integer_part: \"");
            write_graph(&text[r.0..r.1], out).expect("fraction number");
            out.push_str("\" ");
        }
        out.push_str("numerator: \"");
        write_graph(&text[self.num.0..self.num.1], out).expect("fraction number");
        out.push_str("\" denominator: \"");
        write_graph(&text[self.den.0..self.den.1], out).expect("fraction number");
        out.push_str("\" preserve_order: true");
    }
}

/// Fraction body spans at `s` (without the wrapper): `f(end, cost, reading)`.
pub fn fst(text: &[char], s: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, Cost, Frac)) {
    let neg = text.get(s) == Some(&'-');
    let p = s + neg as usize;
    let e1 = digit_end(text, p);
    // the first number is followed by "/", " / " or " " + the numerator
    if e1 == p || !matches!(text.get(e1), Some('/' | ' ')) {
        return;
    }
    let Some(c1) = graph_cost(&text[p..e1]) else { return };
    // integer part
    if text.get(e1) == Some(&' ') {
        let e2 = digit_end(text, e1 + 1);
        if e2 > e1 + 1 {
            if let Some(c2) = graph_cost(&text[e1 + 1..e2]) {
                let base = Frac { neg, integer: Some((p, e1)), num: (e1 + 1, e2), den: (0, 0) };
                rest(text, e2, base, c1 + c2, keep, f);
            }
        }
    }
    rest(text, e1, Frac { neg, integer: None, num: (p, e1), den: (0, 0) }, c1, keep, f);
}

fn rest(
    text: &[char],
    at: usize,
    base: Frac,
    c: Cost,
    keep: &dyn Fn(usize) -> bool,
    f: &mut dyn FnMut(usize, Cost, Frac),
) {
    for sep in ["/", " / "] {
        let Some(n) = match_str(text, at, sep) else { continue };
        let d = at + n;
        let de = digit_end(text, d);
        for e in d + 1..=de.min(d + 24) {
            if !keep(e) {
                continue;
            }
            if let Some(k) = graph_cost(&text[d..e]) {
                f(e, c + k, Frac { den: (d, e), ..base });
            }
        }
    }
}

pub struct Fraction;

impl Spans for Fraction {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit() || c == '-'
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if !(text[start].is_ascii_digit() || text[start] == '-') {
            return;
        }
        fst(text, start, &|_| true, &mut |e, c, fr| {
            out.push(e, WEIGHT + c, || {
                let mut t = String::from("fraction { ");
                fr.fields(text, &mut t);
                t.push_str(" }");
                t
            })
        });
    }
}

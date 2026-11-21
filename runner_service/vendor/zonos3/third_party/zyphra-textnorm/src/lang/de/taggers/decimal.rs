//! `de/taggers/decimal.py`: `DecimalFst` (and `get_quantity`).

use super::cardinal::{W, WEIGHT, digit_end, graph_cost, h3_cost, write_graph, write_h3};
use crate::lang::de::{Sink, Spans};
use crate::text::Cost;

crate::tsv!(QUANTITIES, "de/numbers/quantities.tsv");

/// `self.graph`: fractional digits as words.
pub fn fractional_words(d: &[char], s: &mut String) {
    for (i, &c) in d.iter().enumerate() {
        if i > 0 {
            s.push(' ');
        }
        s.push_str(W.single(c as u8 - b'0'));
    }
}

/// One `final_graph_wo_negative` reading (char ranges into the text), written out by [`Dec::body`].
#[derive(Clone, Copy)]
pub struct Dec {
    /// A leading `-` (only from [`fst`]).
    pub neg: bool,
    /// The integer digits.
    pub int: (usize, usize),
    /// The fractional digits after `,`, if any (else the integer is a 1–3 digit `h3` reading).
    pub frac: Option<(usize, usize)>,
    /// The quantity word after one space.
    pub qty: Option<(usize, usize)>,
}

impl Dec {
    /// `integer_part: "…" (fractional_part: "…")? (quantity: "…")?`.
    pub fn body(&self, text: &[char], out: &mut String) {
        let int = &text[self.int.0..self.int.1];
        out.push_str("integer_part: \"");
        match self.frac {
            Some((a, b)) => {
                write_graph(int, out).expect("decimal integer");
                out.push_str("\" fractional_part: \"");
                fractional_words(&text[a..b], out);
            }
            None => {
                write_h3(int, out).expect("decimal integer");
            }
        }
        out.push('"');
        if let Some((a, b)) = self.qty {
            out.push_str(" quantity: \"");
            out.extend(&text[a..b]);
            out.push('"');
        }
    }

    /// The `DecimalFst.fst` fields: `negative: "true"`? + body + `preserve_order: true`.
    pub fn fields(&self, text: &[char], out: &mut String) {
        if self.neg {
            out.push_str("negative: \"true\" ");
        }
        self.body(text, out);
        out.push_str(" preserve_order: true");
    }
}

/// `final_graph_wo_negative` spans at `s`: `f(end, cost, reading)`.
pub fn wo_negative(text: &[char], s: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, Cost, Dec)) {
    let ie = digit_end(text, s);
    if ie == s {
        return;
    }
    let qty = |f: &mut dyn FnMut(usize, Cost, Dec), at: usize, d: Dec, c: Cost| {
        if text.get(at) != Some(&' ') {
            return;
        }
        for (_, e) in QUANTITIES.prefix_matches(text, at + 1) {
            if e > at + 1 && keep(e) {
                f(e, c, Dec { qty: Some((at + 1, e)), ..d });
            }
        }
    };
    // integer part + "," + fractional
    if text.get(ie) == Some(&',') {
        let fe = digit_end(text, ie + 1);
        if fe > ie + 1 {
            // the cardinal graph reads the whole integer run (digits up to the comma)
            if let Some(c) = graph_cost(&text[s..ie]) {
                for e in ie + 2..=fe {
                    let d = Dec { neg: false, int: (s, ie), frac: Some((ie + 1, e)), qty: None };
                    if e == fe {
                        qty(f, e, d, c);
                    }
                    if keep(e) {
                        f(e, c, d);
                    }
                }
            }
        }
    }
    // one to three digits + " " + quantity
    if ie - s <= 3 {
        if let Some(c) = h3_cost(&text[s..ie]) {
            qty(f, ie, Dec { neg: false, int: (s, ie), frac: None, qty: None }, c);
        }
    }
}

/// `DecimalFst.fst` body spans (negative, `preserve_order`), without the `decimal { … }` wrapper.
pub fn fst(text: &[char], s: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, Cost, Dec)) {
    let neg = text.get(s) == Some(&'-');
    let p = s + neg as usize;
    wo_negative(text, p, keep, &mut |e, c, d| f(e, c, Dec { neg, ..d }));
}

pub struct Decimal;

impl Spans for Decimal {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit() || c == '-'
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if !(text[start].is_ascii_digit() || text[start] == '-') {
            return;
        }
        fst(text, start, &|_| true, &mut |e, c, d| {
            out.push(e, WEIGHT + c, || {
                let mut t = String::from("decimal { ");
                d.fields(text, &mut t);
                t.push_str(" }");
                t
            })
        });
    }
}

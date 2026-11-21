//! `de/taggers/ordinal.py`: `(digits + delete(ending @0.0001 | ".")) @ cardinal.graph`.

use super::cardinal::{WEIGHT, digit_end, graph_cost, write_graph};
use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, cost, match_str};

const ENDINGS: [&str; 5] = ["ter", "tes", "tem", "te", "ten"];
const ENDING: Cost = cost(0.0001);

pub struct Ordinal;

impl Spans for Ordinal {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit()
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if !text[start].is_ascii_digit() {
            return;
        }
        // `closure(DIGIT | ".")` then the cardinal graph: only a pure digit string survives the composition
        let e = digit_end(text, start);
        // every ending starts with `t`
        if !matches!(text.get(e), Some('.' | 't')) {
            return;
        }
        let Some(c) = graph_cost(&text[start..e]) else { return };
        let tag = || {
            let mut t = String::from("ordinal { integer: \"");
            write_graph(&text[start..e], &mut t).expect("ordinal");
            t.push_str("\" }");
            t
        };
        if text.get(e) == Some(&'.') {
            out.push(e + 1, WEIGHT + c, tag);
        }
        for s in ENDINGS {
            if let Some(n) = match_str(text, e, s) {
                out.push(e + n, WEIGHT + c + ENDING, tag);
            }
        }
    }
}

//! `fr/taggers/fraction.py`: `FractionFst`.
//!
//! Numerator and denominator are `closure(NEMO_DIGIT)` composed with the cardinal (digits only, no spaces); the
//! denominator gets `e` appended and runs through `ordinal.graph` (always `ième`, pluralized to `ièmes` unless the
//! numerator is exactly `1`). The optional integer part is a full cardinal followed by one literal space.

use super::cardinal::{num_ends, reading, reading_rank};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_digit};

pub struct Fraction;

pub const WEIGHT: Cost = cost(1.09);

fn digit_run(text: &[char], start: usize) -> usize {
    let mut e = start;
    while e < text.len() && is_digit(text[e]) {
        e += 1;
    }
    e
}

impl Fraction {
    /// `numerator "/" denominator` at `start`: every (end, tag body, rank).
    fn core(text: &[char], start: usize, out: &mut Vec<(usize, String, i32)>) {
        let nend = digit_run(text, start);
        if nend == start || text.get(nend) != Some(&'/') {
            return;
        }
        let num = &text[start..nend];
        let ds = nend + 1;
        let dmax = digit_run(text, ds);
        if dmax == ds {
            return;
        }
        let Some((n, nexact)) = reading(num) else { return };
        let suffix = if num == ['1'] { "ième" } else { "ièmes" };
        for dend in ds + 1..=dmax {
            let Some((d, dexact)) = reading(&text[ds..dend]) else { continue };
            let rank = reading_rank(nexact) * 1000 + reading_rank(dexact);
            let body = format!("numerator: \"{n}\" denominator: \"{d}\" morphosyntactic_features: \"{suffix}\"");
            out.push((dend, body, rank));
        }
    }
}

impl Classifier for Fraction {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let (neg, s) = if text.get(start) == Some(&'-') { (true, start + 1) } else { (false, start) };
        let prefix = if neg { "negative: \"true\" " } else { "" };
        let mut cores = Vec::new();
        Self::core(text, s, &mut cores);
        for (end, body, rank) in cores.drain(..) {
            out.push(Candidate { end, cost: WEIGHT, rank, tag: format!("fraction {{ {prefix}{body} }}") });
        }
        for iend in num_ends(text, s) {
            if text.get(iend) != Some(&' ') {
                continue;
            }
            Self::core(text, iend + 1, &mut cores);
            if cores.is_empty() {
                continue;
            }
            let Some((i, iexact)) = reading(&text[s..iend]) else {
                cores.clear();
                continue;
            };
            for (end, body, rank) in cores.drain(..) {
                out.push(Candidate {
                    end,
                    cost: WEIGHT,
                    rank: rank + reading_rank(iexact) * 1_000_000,
                    tag: format!("fraction {{ {prefix}integer_part: \"{i}\" {body} }}"),
                });
            }
        }
    }
}

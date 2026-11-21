// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese approximation: "~" followed by a number, amount or measure.

use super::super::graph_utils::{
    WEIGHT_APPROX, WEIGHT_CARDINAL, WEIGHT_DECIMAL, WEIGHT_FRACTION, WEIGHT_MEASURE, WEIGHT_MONEY,
};
use super::cardinal::Cardinal;
use super::decimal::Decimal;
use super::fraction::Fraction;
use super::measure::Measure;
use super::money::Money;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, is_nemo_space};

pub struct Approx;

fn number_start(text: &[char], start: usize) -> Option<usize> {
    if text.get(start) != Some(&'~') {
        return None;
    }
    let mut pos = start + 1;
    while text.get(pos).is_some_and(|&c| is_nemo_space(c)) {
        pos += 1;
    }
    (pos < text.len()).then_some(pos)
}

impl Classifier for Approx {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let Some(pos) = number_start(text, start) else { return };
        let members: [(&dyn Classifier, Cost); 5] = [
            (&Money, WEIGHT_MONEY),
            (&Measure, WEIGHT_MEASURE),
            (&Decimal, WEIGHT_DECIMAL),
            (&Fraction, WEIGHT_FRACTION),
            (&Cardinal, WEIGHT_CARDINAL),
        ];
        let mut scratch = Vec::new();
        for (member, base) in members {
            scratch.clear();
            member.candidates(text, pos, &mut scratch);
            for sub in scratch.drain(..) {
                out.push(Candidate {
                    end: sub.end,
                    cost: WEIGHT_APPROX + sub.cost - base,
                    rank: 0,
                    tag: String::new(),
                });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let Some(pos) = number_start(text, start) else { return };
        let members: [(&dyn Classifier, Cost); 5] = [
            (&Money, WEIGHT_MONEY),
            (&Measure, WEIGHT_MEASURE),
            (&Decimal, WEIGHT_DECIMAL),
            (&Fraction, WEIGHT_FRACTION),
            (&Cardinal, WEIGHT_CARDINAL),
        ];
        let mut scratch = Vec::new();
        for (member, base) in members {
            scratch.clear();
            member.candidates(text, pos, &mut scratch);
            for mut sub in scratch.drain(..) {
                if sub.end == candidate.end && WEIGHT_APPROX + sub.cost - base == candidate.cost {
                    member.materialize(text, pos, &mut sub);
                    candidate.tag = format!("name: \"khoảng\" }} tokens {{ {}", sub.tag);
                    return;
                }
            }
        }
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese ordinal classifier: "thứ " + number.

use super::super::graph_utils::{WEIGHT_ORDINAL, spell_cardinal_digits};
use crate::lattice::{Candidate, Classifier};

pub struct Ordinal;

const PREFIX: [char; 4] = ['t', 'h', 'ứ', ' '];

impl Classifier for Ordinal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start + 4 >= text.len() || text[start..start + 4] != PREFIX {
            return;
        }

        let num_start = start + 4;
        let mut digits = Vec::with_capacity(24);
        let mut i = num_start;
        while i < text.len() && text[i].is_ascii_digit() && digits.len() < 21 {
            digits.push(text[i] as u8);
            i += 1;

            if digits == b"1" || digits == b"4" {
                let word = if digits == b"1" { "nhất" } else { "tư" };
                out.push(Candidate {
                    end: i,
                    cost: WEIGHT_ORDINAL,
                    rank: 0,
                    tag: format!("ordinal {{ integer: \"{}\" }}", word),
                });
            }
            let mut tag = String::from("ordinal { integer: \"");
            if let Some(weight) = spell_cardinal_digits(&digits, &mut tag) {
                tag.push_str("\" }");
                out.push(Candidate { end: i, cost: WEIGHT_ORDINAL + weight, rank: 0, tag });
            }
        }
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese punctuation classifier: exact source character set.

use super::super::graph_utils::{WEIGHT_PUNCT, is_vi_punct};
use crate::lattice::{Candidate, Classifier};

pub struct Punctuation;

impl Classifier for Punctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start < text.len() && is_vi_punct(text[start]) {
            out.push(Candidate {
                end: start + 1,
                cost: WEIGHT_PUNCT,
                rank: 0,
                tag: format!("name: \"{}\"", text[start]),
            });
        }
    }
}

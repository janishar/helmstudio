// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Word classifier, ported from ru/taggers/word.py.

use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_nemo_space};

pub struct WordClassifier;

impl Classifier for WordClassifier {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() || is_nemo_space(text[start]) {
            return;
        }

        for end in start + 1..=text.len() {
            if is_nemo_space(text[end - 1]) {
                break;
            }
            if crate::lang::common::possible_token_end(text, end) {
                out.push(Candidate { end, cost: cost(100.0), rank: 0, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity((candidate.end - start) * 4 + 10);
        tag.push_str("name: \"");
        for &c in &text[start..candidate.end] {
            tag.push(c);
        }
        tag.push('"');
        candidate.tag = tag;
    }
}

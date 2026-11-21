//! Arabic whitelist classifier.
//! Matching `ar/taggers/whitelist.py`.

use super::char_util::{token_end, token_start};
use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;

crate::tsv!(WHITELIST, "ar/whitelist.tsv");

pub struct WhitelistClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for WhitelistClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }

        for (row, end) in WHITELIST.prefix_matches(text, start) {
            if token_end(text, end, self.punct) {
                let cost_val = cost(1.01) + row.cost;
                out.push(Candidate { end, cost: cost_val, rank: 0, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        for (row, end) in WHITELIST.prefix_matches(text, start) {
            if end == candidate.end {
                let mut tag = String::with_capacity(row.value.len() + 10);
                tag.push_str("name: \"");
                for c in row.value.chars() {
                    if c == ' ' {
                        tag.push('\u{a0}');
                    } else {
                        tag.push(c);
                    }
                }
                tag.push('"');
                candidate.tag = tag;
                return;
            }
        }
    }
}

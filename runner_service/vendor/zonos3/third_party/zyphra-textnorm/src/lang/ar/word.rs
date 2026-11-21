//! Arabic word classifier.
//! Matching `ar/taggers/word.py`.

use super::char_util::char_at;
use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_nemo_space};

pub struct WordClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for WordClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() || is_nemo_space(text[start]) {
            return;
        }

        let mut end = start;
        while end < text.len() && !is_nemo_space(text[end]) {
            end += 1;
            if end == text.len() || is_nemo_space(text[end]) || self.punct.is_mark(text[end]) {
                out.push(Candidate { end, cost: cost(100.0), rank: 100, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity((candidate.end - start) * 4 + 10);
        tag.push_str("name: \"");
        for i in start..candidate.end {
            tag.push(char_at(text, i));
        }
        tag.push('"');
        candidate.tag = tag;
    }
}

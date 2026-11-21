//! Arabic ordinal classifier and verbalizer.
//! Matching `ar/taggers/ordinal.py` and `ar/verbalizers/ordinal.py`.

use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;

crate::tsv!(ORDINALS, "ar/ordinal/ordinals.tsv");

pub struct OrdinalClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for OrdinalClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }
        if start + 2 >= text.len() || text[start] != 'ا' || text[start + 1] != 'ل' {
            return;
        }

        let mut idx = start + 2;
        let num_start = idx;
        let mut digits = [0u8; 4];
        while idx < text.len() && char_at(text, idx).is_ascii_digit() && idx - num_start <= 3 {
            digits[idx - num_start] = char_at(text, idx) as u8;
            idx += 1;
            if token_end(text, idx, self.punct) {
                let key = std::str::from_utf8(&digits[..idx - num_start]).expect("ASCII digits");
                if ORDINALS.contains_key(key) {
                    out.push(Candidate { end: idx, cost: cost(1.10), rank: 0, tag: String::new() });
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let num_start = start + 2;
        let mut num_str = String::with_capacity(candidate.end - num_start);
        for i in num_start..candidate.end {
            num_str.push(char_at(text, i));
        }
        if let Some(row) = ORDINALS.exact(&num_str).next() {
            let mut tag = String::with_capacity(32 + row.value.len());
            tag.push_str("ordinal { integer: \"");
            tag.push_str(&row.value);
            tag.push_str("\" }");
            candidate.tag = tag;
        }
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let int_val = f.take("integer")?;
    if !f.done() || int_val.is_empty() {
        return None;
    }
    Some(int_val.to_string())
}

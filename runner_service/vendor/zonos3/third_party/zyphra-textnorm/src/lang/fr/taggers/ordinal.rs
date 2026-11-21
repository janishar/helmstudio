//! `fr/taggers/ordinal.py`: `OrdinalFst` (`all_nums_no_tokens` + `suffixes.tsv`).

use super::cardinal::{num_ends, reading, reading_rank};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost};

crate::tsv!(SUFFIXES, "fr/ordinals/suffixes.tsv");

pub struct Ordinal;

pub const WEIGHT: Cost = cost(1.1);

impl Classifier for Ordinal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        for end in num_ends(text, start) {
            let mut suffixes = SUFFIXES.prefix_matches(text, end).filter(|&(_, send)| send > end).peekable();
            if suffixes.peek().is_none() {
                continue;
            }
            let Some((o, exact)) = reading(&text[start..end]) else { continue };
            for (row, send) in suffixes {
                let tag = format!("ordinal {{ integer: \"{o}\" morphosyntactic_features: \"{}\" }}", row.value);
                out.push(Candidate { end: send, cost: WEIGHT + row.cost, rank: reading_rank(exact), tag });
            }
        }
    }
}

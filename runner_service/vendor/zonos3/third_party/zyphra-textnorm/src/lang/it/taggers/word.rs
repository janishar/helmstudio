//! `it/taggers/word.py`: `name: "<NEMO_NOT_SPACE+>"`.

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, is_nemo_space};

use super::money::currency_at;

pub struct Word {
    pub cost: Cost,
}

/// Whether some item crossing the whitespace before the run starting at `run_start` may end inside that run:
/// only number-led items cross whitespace (cardinal/decimal groups, quantities, measure units, money with a
/// spaced currency), so the previous non-space run holds a digit or a currency key.
fn crossing_into(text: &[char], run_start: usize) -> bool {
    let prev_end = text[..run_start].iter().rposition(|&c| !is_nemo_space(c)).map_or(0, |i| i + 1);
    let prev_start = text[..prev_end].iter().rposition(|&c| is_nemo_space(c)).map_or(0, |i| i + 1);
    (prev_start..prev_end).any(|i| text[i].is_ascii_digit() || currency_at(text, i))
}

impl Classifier for Word {
    /// A word starting inside a non-space run follows an item ending there. When that item starts in the same run,
    /// the word from the run's start to the same end costs 100 against item + word ≥ 100 (every item weight is
    /// ≥ 0), has no more digit items and no higher rank, and is relaxed first, so it wins: such starts are only
    /// useful after an item crossing the whitespace before the run.
    ///
    /// Ends before the end of the non-space run are pruned unless a token able to cross whitespace (one that
    /// starts with a digit, `-` or a currency key: cardinal/decimal/measure/money) may start there. Any other split
    /// `word + rest-of-run` costs at least 100 + 1.1 against the single word's 100, so it is never on a cheapest
    /// path. Tags are written only for the words on the best path ([`Classifier::materialize`]).
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if is_nemo_space(text[start]) {
            return;
        }
        if start > 0 && !is_nemo_space(text[start - 1]) {
            let run_start = text[..start].iter().rposition(|&c| is_nemo_space(c)).map_or(0, |i| i + 1);
            if run_start == 0 || !crossing_into(text, run_start) {
                return;
            }
        }
        let mut run = start;
        while run < text.len() && !is_nemo_space(text[run]) {
            run += 1;
        }
        for end in start + 1..=run {
            // a word ending between two digits is never cheapest: no number tagger is cheaper split mid-run
            // than whole except through the classifiers' own spans, which start at the word's end only when a
            // non-digit precedes
            if end < run
                && !((text[end].is_ascii_digit() && !text[end - 1].is_ascii_digit())
                    || text[end] == '-'
                    || currency_at(text, end))
            {
                continue;
            }
            out.push(Candidate { end, cost: self.cost, rank: 0, tag: String::new() });
        }
    }

    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        let mut tag = String::with_capacity(c.end - start + 8);
        tag.push_str("name: \"");
        tag.extend(&text[start..c.end]);
        tag.push('"');
        c.tag = tag;
    }
}

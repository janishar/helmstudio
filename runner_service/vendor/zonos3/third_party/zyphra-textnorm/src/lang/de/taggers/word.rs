//! `de/taggers/word.py`: `name: "<NOT_SPACE+>"` at 100.
//!
//! The `De` shape lets tokens touch, so a word may start and end anywhere in a non-space run; only the spans a best
//! path can use are emitted. Inside a run, the boundaries a best path cannot do without are the ends and starts of
//! items crossing whitespace (and the run's edges). A word on a best path spans from one such boundary to the next:
//! otherwise one word over it and its neighbours up to those boundaries costs 100, strictly less than the word plus
//! the neighbours (every item costs more than 0). So a word starts at a run's start or where a whitespace-crossing
//! item ends, and ends at a run's end or where a whitespace-crossing item may start ([`crossing_may_start`]). Inner
//! boundaries also need [`item_may_start`] (no non-word item starts on a letter glued to a letter or digit, so no best
//! path has a boundary there).

use crate::lang::de::{Sink, Spans, crossing_ends_at, crossing_may_start, item_may_start};
use crate::text::{Cost, cost, is_nemo_space};

pub const WEIGHT: Cost = cost(100.0);

pub struct Word;

impl Spans for Word {
    fn may_start(&self, c: char) -> bool {
        !is_nemo_space(c)
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if start > 0 && !is_nemo_space(text[start - 1]) && !(crossing_ends_at(start) && item_may_start(text, start)) {
            return;
        }
        let run_end = text[start..].iter().position(|&c| is_nemo_space(c)).map_or(text.len(), |i| start + i);
        if run_end == start {
            return;
        }
        let tag = |end: usize| {
            let mut tag = String::with_capacity(8 + 2 * (end - start));
            tag.push_str("name: \"");
            tag.extend(&text[start..end]);
            tag.push('"');
            tag
        };
        out.push(run_end, WEIGHT, || tag(run_end));
        for e in start + 1..run_end {
            if item_may_start(text, e) && crossing_may_start(text, e, run_end) {
                out.push(e, WEIGHT, || tag(e));
            }
        }
    }
}

//! `ja/taggers/word.py`: exactly one `NEMO_NOT_SPACE` char.

use super::Sink;
use crate::text::{Cost, is_nemo_space};

pub struct Word {
    pub cost: Cost,
}

impl Word {
    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        let c = t[start];
        if is_nemo_space(c) {
            return;
        }
        out.push(start + 1, self.cost, 0, |tag| {
            tag.push_str("name: \"");
            tag.push(c);
            tag.push('"');
        });
    }
}

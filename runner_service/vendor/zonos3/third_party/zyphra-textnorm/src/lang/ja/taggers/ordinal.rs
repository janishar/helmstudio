//! `ja/taggers/ordinal.py`: `第` + cardinal | cardinal + `番目`.

use super::Sink;
use super::cardinal::{ends, spell_into};
use crate::text::{Cost, starts_with};

pub struct Ordinal {
    pub cost: Cost,
}

impl Ordinal {
    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        if t[start] == '第' {
            for end in ends(t, start + 1) {
                out.push(end, self.cost, 0, |tag| {
                    tag.push_str("ordinal { integer: \"第");
                    spell_into(&t[start + 1..end], tag);
                    tag.push_str("\" }");
                });
            }
        }
        for e in ends(t, start) {
            if starts_with(t, e, "番目") {
                out.push(e + 2, self.cost, 0, |tag| {
                    tag.push_str("ordinal { integer: \"");
                    spell_into(&t[start..e], tag);
                    tag.push_str("番目\" }");
                });
            }
        }
    }
}

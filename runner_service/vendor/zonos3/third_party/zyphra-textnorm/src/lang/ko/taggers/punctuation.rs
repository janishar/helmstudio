//! `ko/taggers/punctuation.py`: only the range mark `〜` → `부터` (and `부터` itself).

use crate::lang::ko::graph_utils::*;

pub fn tag(t: &[char], p: usize, f: TagSink<'_>) {
    let f = &mut |e, c, r: Render<'_>| f(e, c, false, r);
    cross(t, p, "〜", "name: \"부터\"", f);
    cross(t, p, "부터", "name: \"부터\"", f);
}

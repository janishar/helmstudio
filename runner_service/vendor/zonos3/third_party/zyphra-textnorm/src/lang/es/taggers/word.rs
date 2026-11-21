//! `es/taggers/word.py`: `NEMO_NOT_SPACE+`.

use crate::lang::es::{Lazy, Sink, ok_end};
use crate::text::{cost, is_nemo_space};

pub struct Word;

impl Lazy for Word {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        // only spans a path can continue from (end of text, whitespace, punctuation) are pushed
        let mut end = start;
        while end < text.len() && !is_nemo_space(text[end]) {
            end += 1;
            if !ok_end(text, end) {
                continue;
            }
            out.push(end, cost(200.0), 0, || {
                let mut tag = String::with_capacity(8 + 4 * (end - start));
                tag.push_str("name: \"");
                tag.extend(&text[start..end]);
                tag.push('"');
                tag
            });
        }
    }
}

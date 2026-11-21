//! `zh/taggers/word.py`: exactly one `NEMO_NOT_QUOTE` char (an input space becomes `name: " "`).

use super::{Gen, Sink};

pub struct Word;

impl Gen for Word {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        if c == '"' {
            return;
        }
        sink.raw(start + 1, 0, 0, |s| {
            s.push_str("name: \"");
            s.push(c);
            s.push('"');
        });
    }
}

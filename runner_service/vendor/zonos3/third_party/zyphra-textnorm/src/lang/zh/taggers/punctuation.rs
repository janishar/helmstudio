//! `zh/taggers/punctuation.py`: `common::Punctuation` over Unicode `P*` (without `[`/`]`) plus the file's own mark
//! string, minus the first column of **en**'s `data/whitelist/symbol.tsv` (the file imports `get_abs_path` from
//! `en.utils`): `name: "<run>"` for every run of marks, and for an emphasis tag (`<x>`, `<x/>`, `</x>`; priority
//! union, so a span both accept is pushed once).

use super::{Gen, Sink};
use crate::lang::common::{EN_WHITELIST_SYMBOLS, Punctuation as Marks};
use crate::text::is_nemo_space;

const S: &str = "!#%&'()*+,-/:;<=>?@^_`{|}~\"。，；-《》“”";

pub struct Punctuation {
    marks: Marks,
}

impl Punctuation {
    pub fn new() -> Punctuation {
        let symbols = EN_WHITELIST_SYMBOLS.iter().filter_map(|row| row.first()).map(String::as_str);
        Punctuation { marks: Marks::new(S, false, symbols, 0) }
    }
}

/// `"<" + ((X+ "/"?) | ("/" X+)) + ">"` with `X = NOT_SPACE - {<, >}` (`X` includes `/`): the end of the tag at
/// `start`, if any.
fn emphasis_end(t: &[char], start: usize) -> Option<usize> {
    if t[start] != '<' {
        return None;
    }
    let mut j = start + 1;
    while j < t.len() && !is_nemo_space(t[j]) && t[j] != '<' && t[j] != '>' {
        j += 1;
    }
    (j > start + 1 && t.get(j) == Some(&'>')).then_some(j + 1)
}

impl Gen for Punctuation {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        // no CJK ideograph or ASCII alphanumeric is a mark
        let is_mark =
            |c: char| !(c.is_ascii_alphanumeric() || ('\u{4e00}'..='\u{9fff}').contains(&c)) && self.marks.is_mark(c);
        if c != '<' && !is_mark(c) {
            return;
        }
        let name = |s: &mut String, end: usize| {
            s.push_str("name: \"");
            s.extend(&t[start..end]);
            s.push('"');
        };
        let mut end = start;
        while end < t.len() && is_mark(t[end]) {
            end += 1;
            sink.raw(end, 0, 0, |s| name(s, end));
        }
        if let Some(e) = emphasis_end(t, start)
            && e > end
        {
            sink.raw(e, 0, 0, |s| name(s, e));
        }
    }
}

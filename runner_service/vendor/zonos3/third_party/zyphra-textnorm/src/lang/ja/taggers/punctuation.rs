//! `ja/taggers/punctuation.py`: `common::Punctuation` over the file's own mark string (no `[`/`]`) minus the keys of
//! `data/symbol.tsv` (`load_labels`), plus the range component `〜` → `から` | `から` at −1.0.

use super::Sink;
use crate::lang::common::Punctuation as Marks;
use crate::text::{Cost, cost, is_nemo_space, starts_with};

crate::labels!(SYMBOLS, "ja/symbol.tsv");

const S: &str = "!#$%&'()*+,-./:;<=>?@^_`{|}。，；：《》“”·~【】！？、‘’.<>-——_、。.「」『』‘`／・；’”“”‷･〔〕々〃ゝゞヽ〲〱〳〴〵ヾ〆，~";

pub struct Punctuation {
    marks: Marks,
    /// `marks` on ASCII.
    ascii: [bool; 128],
    cost: Cost,
}

impl Punctuation {
    pub fn new(c: Cost) -> Punctuation {
        let symbols = SYMBOLS.iter().filter_map(|row| row.first()).map(String::as_str);
        let marks = Marks::new(S, false, symbols, c);
        let ascii = std::array::from_fn(|i| marks.is_mark(i as u8 as char));
        Punctuation { marks, ascii, cost: c }
    }

    fn is_mark(&self, c: char) -> bool {
        if c.is_ascii() { self.ascii[c as usize] } else { self.marks.is_mark(c) }
    }

    /// Whether any span can start with `c`.
    pub fn can_start(&self, c: char) -> bool {
        self.is_mark(c) || matches!(c, '<' | '〜' | 'か')
    }

    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        let c = t[start];
        if !self.can_start(c) {
            return;
        }
        // `common::Punctuation::spans`: every mark-run prefix, then the emphasis tag `<X+>` (X = NOT_SPACE − {<, >})
        // when its end is not a mark-run end (priority union with an identical output)
        let mut run = start;
        while run < t.len() && self.is_mark(t[run]) {
            run += 1;
            out.push(run, self.cost, 0, |tag| name(tag, &t[start..run]));
        }
        if c == '<' {
            let mut j = start + 1;
            while j < t.len() && !is_nemo_space(t[j]) && t[j] != '<' && t[j] != '>' {
                j += 1;
            }
            if j > start + 1 && t.get(j) == Some(&'>') && j + 1 > run {
                out.push(j + 1, self.cost, 0, |tag| name(tag, &t[start..j + 1]));
            }
        }
        let range = self.cost + cost(-1.0);
        if c == '〜' {
            out.push(start + 1, range, 0, |tag| tag.push_str("name: \"から\""));
        } else if starts_with(t, start, "から") {
            out.push(start + 2, range, 0, |tag| tag.push_str("name: \"から\""));
        }
    }
}

fn name(tag: &mut String, s: &[char]) {
    tag.push_str("name: \"");
    tag.extend(s);
    tag.push('"');
}

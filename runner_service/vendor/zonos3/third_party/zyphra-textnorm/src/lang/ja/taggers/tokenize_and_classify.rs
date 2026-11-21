//! `ja/taggers/tokenize_and_classify.py`: `closure(tokens { classify } , 1)` with `classify` = date 1.1 | fraction
//! 1.0 | time 1.1 | whitelist 1.1 | cardinal 1.1 | decimal 3.05 | ordinal 1.1 | punct 1.0 | word 100.
//!
//! The shape (`Shape::Cjk`) is one token after another with no whitespace items, so the best path is a plain
//! shortest path over token spans. [`Union`] computes it at position 0 from the classifiers' spans alone, with
//! exactly the lattice's order and key (`lattice::Tagger::best_path`: cost, then items holding a digit, then rank =
//! union index × 1000 + in-classifier rank; on equal keys the first relaxation stays), and hands the lattice that
//! whole path as one candidate over the text, its tags joined by the literal `} tokens { ` (a multi-token
//! `Candidate::tag`). The tagged string is the one the lattice would build from every span; a text without a path
//! yields no candidate, so it still has no path.

use std::cell::RefCell;

use super::{Nth, Sink, Spans};
use super::{cardinal, date, decimal, fraction, ordinal, punctuation, time, whitelist, word};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_nemo_space, nd_range_start};

pub struct Union {
    date: date::Date,
    fraction: fraction::Fraction,
    time: time::Time,
    whitelist: whitelist::WhiteList,
    cardinal: cardinal::Cardinal,
    decimal: decimal::Decimal,
    ordinal: ordinal::Ordinal,
    punct: punctuation::Punctuation,
    word: word::Word,
    /// Bit per BMP char: only `word` accepts a span starting with it (the one-char token).
    word_only: Vec<u64>,
}

const CLASSIFIERS: usize = 9;
const WORD: usize = 8;

impl Union {
    pub fn new() -> Union {
        let punct = punctuation::Punctuation::new(cost(1.0));
        let mut word_only = vec![0u64; 0x10000 / 64];
        for cp in 0..0x10000u32 {
            let Some(c) = char::from_u32(cp) else { continue };
            let other = date::can_start(c)
                || time::can_start(c)
                || whitelist::WhiteList::can_start(c)
                || punct.can_start(c)
                // fraction, cardinal, decimal, ordinal: a digit, `√`, a sign (`-`, `マイナス`) or `第`
                || matches!(c, '0'..='9' | '√' | '-' | 'マ' | '第');
            if !other && !is_nemo_space(c) {
                word_only[cp as usize / 64] |= 1 << (cp % 64);
            }
        }
        Union {
            date: date::Date { cost: cost(1.1) },
            fraction: fraction::Fraction { cost: cost(1.0) },
            time: time::Time { cost: cost(1.1) },
            whitelist: whitelist::WhiteList { cost: cost(1.1) },
            cardinal: cardinal::Cardinal { cost: cost(1.1) },
            decimal: decimal::Decimal { cost: cost(3.05) },
            ordinal: ordinal::Ordinal { cost: cost(1.1) },
            punct,
            word: word::Word { cost: cost(100.0) },
            word_only,
        }
    }

    fn word_only(&self, c: char) -> bool {
        let cp = c as usize;
        cp < 0x10000 && self.word_only[cp / 64] & (1 << (cp % 64)) != 0
    }

    /// The spans of union member `ci` at `p`, in that classifier's order.
    fn spans<S: Sink>(&self, ci: usize, t: &[char], p: usize, out: &mut S) {
        match ci {
            0 => self.date.spans(t, p, out),
            1 => self.fraction.spans(t, p, out),
            2 => self.time.spans(t, p, out),
            3 => self.whitelist.spans(t, p, out),
            4 => self.cardinal.spans(t, p, out),
            5 => self.decimal.spans(t, p, out),
            6 => self.ordinal.spans(t, p, out),
            7 => self.punct.spans(t, p, out),
            _ => self.word.spans(t, p, out),
        }
    }

    /// The best path's tokens of `t` (empty when there is none), into `path` as `(start, end, member, push index)`.
    fn best_path(&self, t: &[char], s: &mut Scratch) {
        let n = t.len();
        s.path.clear();
        s.best.clear();
        s.best.resize(n + 1, None);
        s.best[0] = Some(Cell { cost: 0, digits: 0, rank: 0, prev: u32::MAX, member: 0, index: 0 });
        s.digits.clear();
        s.digits.push(0);
        let mut d = 0u32;
        for &c in t {
            d += is_nd(c) as u32;
            s.digits.push(d);
        }
        for pos in 0..n {
            let Some(cur) = s.best[pos] else { continue };
            let members = if self.word_only(t[pos]) { WORD..CLASSIFIERS } else { 0..CLASSIFIERS };
            for ci in members {
                s.spans.clear();
                self.spans(ci, t, pos, &mut Spans(&mut s.spans));
                for (index, &(end, c, r)) in s.spans.iter().enumerate() {
                    if end <= pos || end > n {
                        continue;
                    }
                    let cand = Cell {
                        cost: cur.cost + c,
                        digits: cur.digits + (s.digits[end] > s.digits[pos]) as u32,
                        rank: cur.rank + ci as i64 * 1000 + r as i64,
                        prev: pos as u32,
                        member: ci as u8,
                        index: index as u32,
                    };
                    let slot = &mut s.best[end];
                    if slot.is_none_or(|old| old.key() > cand.key()) {
                        *slot = Some(cand);
                    }
                }
            }
        }
        let mut pos = n;
        while let Some(cell) = s.best[pos] {
            if cell.prev == u32::MAX {
                break;
            }
            s.path.push(Step { start: cell.prev as usize, end: pos, member: cell.member, index: cell.index });
            pos = cell.prev as usize;
        }
        if pos != 0 {
            s.path.clear();
        }
        s.path.reverse();
    }
}

#[derive(Clone, Copy)]
struct Cell {
    cost: Cost,
    digits: u32,
    rank: i64,
    prev: u32,
    member: u8,
    index: u32,
}

impl Cell {
    fn key(&self) -> (Cost, u32, i64) {
        (self.cost, self.digits, self.rank)
    }
}

#[derive(Clone, Copy)]
struct Step {
    start: usize,
    end: usize,
    member: u8,
    index: u32,
}

#[derive(Default)]
struct Scratch {
    path: Vec<Step>,
    best: Vec<Option<Cell>>,
    digits: Vec<u32>,
    spans: Vec<(usize, Cost, i32)>,
}

thread_local! {
    /// [`Union::best_path`]'s buffers, reused across calls.
    static SCRATCH: RefCell<Scratch> = RefCell::new(Scratch::default());
}

/// No `Nd` code point lies in `U+1C5A..U+A620` (Unicode 15.0; `nd_ranges.txt`), which holds the CJK blocks.
const ND_FREE: std::ops::Range<u32> = 0x1C5A..0xA620;

/// Unicode `Nd` (the lattice's digit-item test), without a table search for ASCII and CJK.
fn is_nd(c: char) -> bool {
    if c.is_ascii() || ND_FREE.contains(&(c as u32)) {
        return c.is_ascii_digit();
    }
    nd_range_start(c).is_some()
}

impl Classifier for Union {
    fn candidates(&self, t: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start != 0 {
            return;
        }
        SCRATCH.with(|s| {
            let mut s = s.borrow_mut();
            self.best_path(t, &mut s);
            if s.path.is_empty() {
                return;
            }
            let mut tag = String::with_capacity(48 * s.path.len());
            let mut cost = 0;
            for (k, step) in s.path.iter().enumerate() {
                if k > 0 {
                    tag.push_str(" } tokens { ");
                }
                let mut nth = Nth { left: step.index as usize, end: 0, cost: 0, found: false, out: &mut tag };
                self.spans(step.member as usize, t, step.start, &mut nth);
                debug_assert!(nth.found && nth.end == step.end, "the path's span is pushed again");
                cost += nth.cost;
            }
            out.push(Candidate { end: t.len(), cost, rank: 0, tag });
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn nd_free_range_has_no_digit() {
        for cp in ND_FREE {
            if let Some(c) = char::from_u32(cp) {
                assert!(nd_range_start(c).is_none(), "{cp:x}");
            }
        }
    }

    /// The `word_only` table is exact: for each such char, no other member accepts a span starting with it,
    /// whatever follows.
    #[test]
    fn word_only_chars_start_no_other_span() {
        let u = Union::new();
        let tails = ["", "1", "2021年3月5日", "から", "1/2", "）(月)", "時30分", ".5", "番目", "代", "ンス1", "R.1年"];
        let mut v = Vec::new();
        for cp in 0..0x10000u32 {
            let Some(c) = char::from_u32(cp) else { continue };
            if !u.word_only(c) {
                continue;
            }
            for tail in tails {
                let t: Vec<char> = std::iter::once(c).chain(tail.chars()).collect();
                for ci in 0..WORD {
                    v.clear();
                    u.spans(ci, &t, 0, &mut Spans(&mut v));
                    assert!(v.is_empty(), "{c:?} + {tail:?}: member {ci}");
                }
            }
        }
    }
}

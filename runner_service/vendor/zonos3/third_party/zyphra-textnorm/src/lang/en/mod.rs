//! `text_normalization/en` (NeMo r1.2.0): deterministic, cased, `post_process=True`.

pub mod graph_utils;
pub mod lazy;
pub mod taggers;
pub mod verbalizers;

use std::sync::LazyLock;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};

struct En {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for En {
    fn code(&self) -> &'static str {
        "en"
    }
    fn shape(&self) -> Shape {
        Shape::En
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalizers::verbalize_final::verbalize(token)
    }
    fn post_process(&self, text: String) -> String {
        verbalizers::post_processing::apply(&text)
    }
}

/// The `classify` union as one lattice classifier. `Shape::En` never puts two classified tokens next to each
/// other: a token starts at the text start, after a whitespace run or after a punctuation item, and is followed by
/// the end, whitespace or a punctuation item. Spans violating that can never be on a path, so they are neither
/// computed nor materialized. Each member's candidates carry `index * 1000 + rank`, exactly the rank the lattice
/// would assign to the members listed separately (the lattice adds `0 * 1000` for this single entry); members
/// push rank 0, so the index recovers the member that pushed a winner, whose `materialize` writes its tag.
struct Union {
    /// `(classifier, needs a digit)`: time, decimal, cardinal, ordinal, money, range and money ranges only accept
    /// spans holding an ASCII digit before the next whitespace, so word-only chunks skip them.
    members: Vec<(Box<dyn Classifier>, bool)>,
}

/// Whitespace or a punctuation mark: where a classified token may start after / end before.
#[inline]
pub(crate) fn boundary(c: char) -> bool {
    if c.is_ascii() {
        return ASCII_BOUNDARY[c as usize];
    }
    c == '\u{a0}' || taggers::word::MARKS.is_mark(c)
}

/// ASCII part of [`boundary`]: `NEMO_WHITE_SPACE` and the ASCII punctuation marks of `PunctuationFst` (every
/// ASCII punctuation char except the whitelist symbols `& # @ _ % * + / = ^ | ~ $`).
const ASCII_BOUNDARY: [bool; 128] = {
    let mut t = [false; 128];
    let marks = b" \t\n\r!\"'(),-.:;<>?[\\]`{}";
    let mut i = 0;
    while i < marks.len() {
        t[marks[i] as usize] = true;
        i += 1;
    }
    t
};

/// Whether a token may end at `e` (see [`Union`]).
#[inline]
pub(crate) fn end_ok(text: &[char], e: usize) -> bool {
    e == text.len() || boundary(text[e])
}

thread_local! {
    /// Per text (rebuilt at position 0): whether a digit precedes the next whitespace from each position.
    static DIGIT_AHEAD: std::cell::RefCell<Vec<bool>> = const { std::cell::RefCell::new(Vec::new()) };
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            lazy::clear();
            // digit_ahead[i]: an ASCII digit occurs in text[i..] before the next whitespace char
            DIGIT_AHEAD.with_borrow_mut(|d| {
                d.clear();
                d.resize(text.len() + 1, false);
                for i in (0..text.len()).rev() {
                    d[i] = text[i].is_ascii_digit() || (!is_nemo_space(text[i]) && d[i + 1]);
                }
            });
        } else if !boundary(text[start - 1]) {
            return;
        }
        let has_digit = DIGIT_AHEAD.with_borrow(|d| d.get(start).copied().unwrap_or(false));
        for (index, (c, needs_digit)) in self.members.iter().enumerate() {
            if *needs_digit && !has_digit {
                continue;
            }
            let from = out.len();
            lazy::lazily(|| c.candidates(text, start, out));
            let mut k = from;
            for i in from..out.len() {
                if end_ok(text, out[i].end) {
                    out[i].rank += index as i32 * 1000;
                    out.swap(k, i);
                    k += 1;
                }
            }
            out.truncate(k);
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let index = (candidate.rank / 1000) as usize;
        let base = index as i32 * 1000;
        candidate.rank -= base;
        self.members[index].0.materialize(text, start, candidate);
        candidate.rank += base;
    }
}

static GRAMMAR: LazyLock<En> = LazyLock::new(|| {
    let members: Vec<Box<dyn Classifier>> = vec![
        Box::new(taggers::whitelist::WhiteList),
        Box::new(taggers::time::Time),
        Box::new(taggers::date::Date),
        Box::new(taggers::decimal::Decimal),
        Box::new(taggers::measure::Measure),
        Box::new(taggers::cardinal::Cardinal),
        Box::new(taggers::ordinal::Ordinal),
        Box::new(taggers::money::Money),
        Box::new(taggers::telephone::Telephone),
        Box::new(taggers::electronic::Electronic),
        Box::new(taggers::fraction::Fraction),
        Box::new(taggers::range::Range),
        Box::new(taggers::serial::Serial),
        Box::new(taggers::money::MoneyRange),
        Box::new(taggers::word::Word),
    ];
    let members = members.into_iter().enumerate().map(|(i, c)| (c, matches!(i, 1 | 3 | 5 | 6 | 7 | 11 | 13))).collect();
    En { classifiers: vec![Box::new(Union { members })], punct: Punctuation::en(cost(2.1)) }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

#[cfg(test)]
mod tests {
    #[test]
    fn ascii_boundary_matches_punctuation() {
        for b in 0u8..128 {
            let c = b as char;
            let want = crate::text::is_nemo_space(c) || super::taggers::word::MARKS.is_mark(c);
            assert_eq!(super::ASCII_BOUNDARY[b as usize], want, "{c:?}");
        }
    }
}

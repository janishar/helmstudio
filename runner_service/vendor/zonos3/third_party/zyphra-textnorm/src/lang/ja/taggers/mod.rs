//! `ja/taggers/*.py`.
//!
//! Each classifier reports its spans to a [`Sink`]: the union in [`tokenize_and_classify`] first explores every
//! position with [`Spans`] (ends and weights only, no text) and then writes the tag text of the chosen spans alone
//! with [`Nth`].

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod fraction;
pub mod ordinal;
pub mod punctuation;
pub mod time;
pub mod tokenize_and_classify;
pub mod whitelist;
pub mod word;

use crate::text::Cost;

/// Receiver of the spans a classifier accepts at one start position, in the classifier's order.
pub trait Sink {
    /// The span ending at `end` (exclusive) with its weight and in-classifier rank; `tag` writes the tagger's output
    /// text for it and runs only when the sink wants that text.
    fn push(&mut self, end: usize, cost: Cost, rank: i32, tag: impl FnOnce(&mut String));
    /// Whether later spans are of no use (a classifier may stop exploring).
    fn done(&self) -> bool {
        false
    }
}

/// Collects `(end, cost, rank)` of every span, never building text.
pub struct Spans<'a>(pub &'a mut Vec<(usize, Cost, i32)>);

impl Sink for Spans<'_> {
    fn push(&mut self, end: usize, cost: Cost, rank: i32, _tag: impl FnOnce(&mut String)) {
        self.0.push((end, cost, rank));
    }
}

/// Appends the tag text of the span pushed at index `left` (counting from 0) to `out` and ignores the others.
pub struct Nth<'s> {
    pub left: usize,
    pub end: usize,
    pub cost: Cost,
    pub found: bool,
    pub out: &'s mut String,
}

impl Sink for Nth<'_> {
    fn push(&mut self, end: usize, cost: Cost, _rank: i32, tag: impl FnOnce(&mut String)) {
        if self.left == 0 {
            tag(self.out);
            (self.end, self.cost, self.found) = (end, cost, true);
        }
        self.left = self.left.wrapping_sub(1);
    }
    fn done(&self) -> bool {
        self.found
    }
}

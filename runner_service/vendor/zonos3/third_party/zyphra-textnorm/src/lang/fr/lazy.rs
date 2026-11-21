//! Candidates without tags until they win.
//!
//! A [`Lazy`] classifier writes its tags through an [`Out`]. During the lattice search it runs with [`Skip`], which
//! discards every write, so exploring spans builds no strings (the control flow — which spans are accepted, their
//! costs and ranks — never depends on the written text); `Classifier::materialize` reruns the generator at the
//! winner's start with `String` and takes the first candidate with the winner's `(end, cost, rank)` — the one the
//! lattice kept, since it keeps the first of equal-key candidates.

use crate::lattice::Candidate;
use crate::text::Cost;

/// A tag being written.
pub trait Out {
    fn new() -> Self;
    fn push_str(&mut self, s: &str);
    fn push(&mut self, c: char);
    fn append(&mut self, other: &Self);
    /// The current length, for [`Out::truncate`].
    fn len(&self) -> usize;
    fn truncate(&mut self, len: usize);
    fn into_tag(self) -> String;
}

impl Out for String {
    #[inline]
    fn new() -> Self {
        String::with_capacity(96)
    }
    #[inline]
    fn push_str(&mut self, s: &str) {
        String::push_str(self, s)
    }
    #[inline]
    fn push(&mut self, c: char) {
        String::push(self, c)
    }
    #[inline]
    fn append(&mut self, other: &Self) {
        String::push_str(self, other)
    }
    #[inline]
    fn len(&self) -> usize {
        String::len(self)
    }
    #[inline]
    fn truncate(&mut self, len: usize) {
        String::truncate(self, len)
    }
    #[inline]
    fn into_tag(self) -> String {
        self
    }
}

/// Discards every write.
#[derive(Clone, Copy)]
pub struct Skip;

impl Out for Skip {
    #[inline]
    fn new() -> Self {
        Skip
    }
    #[inline]
    fn push_str(&mut self, _: &str) {}
    #[inline]
    fn push(&mut self, _: char) {}
    #[inline]
    fn append(&mut self, _: &Self) {}
    #[inline]
    fn len(&self) -> usize {
        0
    }
    #[inline]
    fn truncate(&mut self, _: usize) {}
    #[inline]
    fn into_tag(self) -> String {
        String::new()
    }
}

/// A classifier whose candidates are generated with tags written through `O`.
pub trait Lazy: Send + Sync {
    /// Pushes the candidates at `start`. With `want = Some(end)` only those ending at `end` need to be built and
    /// pushed; every rank and cost stays what the unfiltered run gives.
    fn generate<O: Out>(&self, text: &[char], start: usize, want: Option<usize>, out: &mut Vec<Candidate>);
}

/// Whether a candidate ending at `end` is wanted.
#[inline]
pub fn wanted(want: Option<usize>, end: usize) -> bool {
    want.is_none_or(|w| w == end)
}

/// `Candidate { end, cost, rank, tag }`.
#[inline]
pub fn candidate<O: Out>(end: usize, cost: Cost, rank: i32, tag: O) -> Candidate {
    Candidate { end, cost, rank, tag: tag.into_tag() }
}

// reused across `materialize` calls
thread_local! {
    static SCRATCH: std::cell::RefCell<Vec<Candidate>> = const { std::cell::RefCell::new(Vec::new()) };
}

/// `Classifier::materialize` of a [`Lazy`] classifier.
pub fn materialize<L: Lazy>(l: &L, text: &[char], start: usize, c: &mut Candidate) {
    SCRATCH.with(|s| {
        let mut all = s.borrow_mut();
        all.clear();
        l.generate::<String>(text, start, Some(c.end), &mut all);
        if let Some(found) = all.iter_mut().find(|x| x.end == c.end && x.cost == c.cost && x.rank == c.rank) {
            c.tag = std::mem::take(&mut found.tag);
        }
        all.clear();
    });
}

/// Implements `Classifier` for [`Lazy`] types.
macro_rules! lazy_classifier {
    ($($t:ty),* $(,)?) => {$(
        impl crate::lattice::Classifier for $t {
            fn candidates(&self, text: &[char], start: usize, out: &mut Vec<crate::lattice::Candidate>) {
                crate::lang::fr::lazy::Lazy::generate::<crate::lang::fr::lazy::Skip>(self, text, start, None, out)
            }
            fn materialize(&self, text: &[char], start: usize, c: &mut crate::lattice::Candidate) {
                crate::lang::fr::lazy::materialize(self, text, start, c)
            }
        }
    )*};
}
pub(crate) use lazy_classifier;

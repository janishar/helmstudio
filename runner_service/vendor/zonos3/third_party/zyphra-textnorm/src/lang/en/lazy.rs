//! Candidates without tags until they win.
//!
//! The `classify` union runs its members in lazy mode: tag text is not built ([`lz!`] yields an empty, unallocated
//! `String`, number names are validated and costed but not spelled) and candidates carry empty tags. When a
//! candidate is on the best path, [`materialize`] reruns the member that pushed it at its start with text built and
//! with [`keep_end`] accepting only the winner's end, and takes the first candidate with the winner's `(end, cost)`:
//! the one the lattice kept, since the lattice keeps the first of equal-key candidates (every en member pushes rank
//! 0) and a member pushes the same candidates in the same order in both modes. Members that [`record`] a
//! [`Reading`] per candidate (spans and table rows) write a winner's tag straight from it instead
//! ([`materialize_recorded`]); only their branches without readings rerun.
//!
//! So a string built lazily must never decide acceptance, order or cost; code whose strings do (range's verbalized
//! parts) runs under [`with_build`]. Outside the union (verbalizers, tests) everything is built.

use std::cell::{Cell, RefCell};

use crate::lang::en::taggers::date::DateR;
use crate::lang::en::taggers::decimal::DecR;
use crate::lang::en::taggers::electronic::ElR;
use crate::lang::en::taggers::fraction::FracR;
use crate::lang::en::taggers::measure::MeasureR;
use crate::lang::en::taggers::money::MoneyR;
use crate::lang::en::taggers::telephone::TelR;
use crate::lang::en::taggers::time::TimeR;
use crate::lang::en::taggers::whitelist::WlR;
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

thread_local! {
    static LAZY: Cell<bool> = const { Cell::new(false) };
    /// The only end [`keep_end`] accepts while a winner is materialized.
    static TARGET: Cell<usize> = const { Cell::new(usize::MAX) };
    /// The only branch [`branch_on`] runs while a winner is materialized (0: all).
    static BRANCH: Cell<u8> = const { Cell::new(0) };
    /// `(member, start, end, cost, branch, reading)` of every lazily pushed candidate of the current text whose
    /// member records branches ([`record`]); cleared at position 0 of every text ([`clear`]).
    static STASH: RefCell<Vec<(u8, u32, u32, Cost, u8, Reading)>> = const { RefCell::new(Vec::new()) };
}

/// What a recorded candidate's tag is written from: positions and table rows, no text. [`Reading::None`] for
/// branches whose winners rerun.
#[derive(Clone, Copy, Debug, Default)]
pub enum Reading {
    #[default]
    None,
    Measure(MeasureR),
    Date(DateR),
    Decimal(DecR),
    Fraction(FracR),
    Money(MoneyR),
    Time(TimeR),
    /// The end of the ordinal's number part.
    Ordinal(u32),
    WhiteList(WlR),
    Electronic(ElR),
    Telephone(TelR),
}

/// Whether branch `b` (≥ 1) of a member runs: always while exploring, only the recorded one while materializing.
#[inline]
pub fn branch_on(b: u8) -> bool {
    let t = BRANCH.with(Cell::get);
    t == 0 || t == b
}

/// Records that `member` pushed `(end, cost)` at `start` from branch `branch`, whose tag `reading` writes
/// (exploration only).
pub fn record(member: u8, start: usize, end: usize, cost: Cost, branch: u8, reading: Reading) {
    if !building() {
        STASH.with_borrow_mut(|s| s.push((member, start as u32, end as u32, cost, branch, reading)));
    }
}

/// Forgets the recorded branches (a new text starts).
pub fn clear() {
    STASH.with_borrow_mut(Vec::clear);
}

/// Whether tag text is being built (false only while the union explores).
#[inline]
pub fn building() -> bool {
    !LAZY.with(Cell::get)
}

/// Whether a token may end at `e`: [`crate::lang::en::end_ok`], restricted to the winner's end while one is
/// materialized. Members filter their final ends (and the `keep` closures that gate string building) with it.
#[inline]
pub fn keep_end(text: &[char], e: usize) -> bool {
    let t = TARGET.with(Cell::get);
    (t == usize::MAX || t == e) && crate::lang::en::end_ok(text, e)
}

/// Restores the previous mode, also when a grammar panics (the wrapper turns a panic into a grammar failure).
struct Restore(bool, usize, u8);

impl Drop for Restore {
    fn drop(&mut self) {
        LAZY.with(|l| l.set(self.0));
        TARGET.with(|t| t.set(self.1));
        BRANCH.with(|b| b.set(self.2));
    }
}

fn with_mode<T>(lazy: bool, target: usize, branch: u8, f: impl FnOnce() -> T) -> T {
    let _restore =
        Restore(LAZY.with(|l| l.replace(lazy)), TARGET.with(|t| t.replace(target)), BRANCH.with(|b| b.replace(branch)));
    f()
}

/// Runs `f` without building tag text.
pub fn lazily<T>(f: impl FnOnce() -> T) -> T {
    with_mode(true, usize::MAX, 0, f)
}

/// Runs `f` with tag text built and every end kept (for sub-grammars whose strings decide acceptance).
pub fn with_build<T>(f: impl FnOnce() -> T) -> T {
    with_mode(false, usize::MAX, 0, f)
}

/// Fills in the tag of a candidate that `c` pushed lazily at `start`.
pub fn materialize(c: &dyn Classifier, text: &[char], start: usize, candidate: &mut Candidate) {
    materialize_branch(c, 0, text, start, candidate)
}

/// [`materialize`] for a member that [`record`]s its branches as `member`: only the recorded branch reruns (the
/// lattice kept the first equal-key candidate, so it came from the first recorded branch with that key, and that
/// branch alone produces its cheapest output at that end first).
pub fn materialize_branch(c: &dyn Classifier, member: u8, text: &[char], start: usize, candidate: &mut Candidate) {
    let branch = if member == 0 { 0 } else { stashed(member, start, candidate).map_or(0, |x| x.0) };
    rerun(c, branch, text, start, candidate);
}

/// The `(branch, reading)` recorded for `candidate` (the first entry with its key: the one the lattice kept).
fn stashed(member: u8, start: usize, candidate: &Candidate) -> Option<(u8, Reading)> {
    let key = (member, start as u32, candidate.end as u32, candidate.cost);
    STASH.with_borrow(|s| s.iter().find(|x| (x.0, x.1, x.2, x.3) == key).map(|x| (x.4, x.5)))
}

fn rerun(c: &dyn Classifier, branch: u8, text: &[char], start: usize, candidate: &mut Candidate) {
    let mut all = Vec::new();
    with_mode(false, candidate.end, branch, || c.candidates(text, start, &mut all));
    if let Some(found) = all.into_iter().find(|x| x.end == candidate.end && x.cost == candidate.cost) {
        candidate.tag = found.tag;
    }
}

/// [`materialize_branch`] that writes the tag from the recorded reading with `write` (`None`: no reading, the
/// branch reruns). Only branches in `rerun_branches` may lack a reading. Debug builds check the written tag
/// against the rerun, so the fast path is byte-identical wherever the tests reach it.
pub fn materialize_recorded(
    c: &dyn Classifier,
    member: u8,
    rerun_branches: &[u8],
    text: &[char],
    start: usize,
    candidate: &mut Candidate,
    write: impl FnOnce(Reading) -> Option<String>,
) {
    let found = stashed(member, start, candidate);
    debug_assert!(found.is_some(), "member {member}: winner at {start}..{} not recorded", candidate.end);
    let (branch, reading) = found.unwrap_or((0, Reading::None));
    let Some(tag) = write(reading) else {
        debug_assert!(rerun_branches.contains(&branch), "member {member}: branch {branch} has no reading");
        return rerun(c, branch, text, start, candidate);
    };
    if cfg!(debug_assertions) {
        let mut check = candidate.clone();
        rerun(c, branch, text, start, &mut check);
        debug_assert_eq!(tag, check.tag, "member {member}: reading {reading:?} at {start}");
    }
    candidate.tag = tag;
}

/// `format!` while tags are being built, else an empty (unallocated) `String`.
macro_rules! lz {
    ($($t:tt)*) => {
        if $crate::lang::en::lazy::building() { format!($($t)*) } else { String::new() }
    };
}
pub(crate) use lz;

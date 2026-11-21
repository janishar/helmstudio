//! `it/taggers/`.

pub mod cardinal;
pub mod decimals;
pub mod electronic;
pub mod measure;
pub mod money;
pub mod time;
pub mod whitelist;
pub mod word;

/// Equal-cost tie-break for a token whose span starts with the `-` it reads as "meno": the oracle prefers the
/// punctuation run that swallows the `-` (golden `[-8] lui` → `name: "[-"` + `cardinal "otto"`, not `name: "["` +
/// `cardinal { negative: "true" … }`).
pub fn sign_rank(neg: bool) -> i32 {
    if neg { 500 } else { 0 }
}

thread_local! {
    /// The end of the candidate being materialized (`usize::MAX`: none): tags are written only for candidates
    /// ending there, while the union re-runs a member for a best-path item ([`with_tags`]).
    static TARGET: std::cell::Cell<usize> = const { std::cell::Cell::new(usize::MAX) };
}

/// The tag `build` writes for a candidate ending at `end` while materializing it; an empty placeholder otherwise
/// (the lattice only needs end, cost and rank, and only best-path items get their tag).
pub fn tag(end: usize, capacity: usize, build: impl FnOnce(&mut String)) -> String {
    if TARGET.get() != end {
        return String::new();
    }
    let mut t = String::with_capacity(capacity);
    build(&mut t);
    t
}

/// Runs `f` with [`tag`] writing the tags of candidates ending at `end`.
pub fn with_tags(end: usize, f: impl FnOnce()) {
    TARGET.set(end);
    f();
    TARGET.set(usize::MAX);
}

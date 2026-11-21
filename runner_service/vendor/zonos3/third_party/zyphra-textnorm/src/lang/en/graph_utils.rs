//! `en/graph_utils.py` helpers the taggers share: per-end best outputs, `SINGULAR_TO_PLURAL`.

use std::sync::LazyLock;

use crate::text::{Cost, Row, Table};

crate::tsv!(SUPPLETIVE, "en/suppletive.tsv");

/// Outputs of a sub-grammar at one start position: the cheapest output per end (first on ties). Pruning to the
/// cheapest per end is exact for everything above (costs add, the lattice only compares sums). Each entry also
/// holds the branch ([`crate::lang::en::lazy::branch_on`]) and the reading that set it (the small Copy record the
/// entry's text is written from; `()` for sub-grammars that keep only strings); `.1` is the branch now adding.
#[derive(Debug)]
pub struct Outs<R = ()>(Vec<((usize, Cost, String), (u8, R))>, u8);

impl<R> Default for Outs<R> {
    fn default() -> Self {
        Outs(Vec::new(), 0)
    }
}

impl Outs {
    pub fn new() -> Outs {
        Outs::default()
    }

    pub fn add(&mut self, end: usize, cost: Cost, s: String) {
        self.add_r(end, cost, s, ());
    }
}

impl<R: Copy> Outs<R> {
    /// Subsequent [`Outs::add_r`] calls come from branch `b`.
    pub fn set_branch(&mut self, b: u8) {
        self.1 = b;
    }

    /// Keeps `(end, cost, s)` read as `r` if no cheaper (or equal) output has that end.
    pub fn add_r(&mut self, end: usize, cost: Cost, s: String, r: R) {
        if let Some(x) = self.0.iter_mut().find(|x| x.0.0 == end) {
            if cost < x.0.1 {
                *x = ((end, cost, s), (self.1, r));
            }
        } else {
            self.0.push(((end, cost, s), (self.1, r)));
        }
    }

    pub fn iter(&self) -> impl Iterator<Item = &(usize, Cost, String)> {
        self.0.iter().map(|x| &x.0)
    }

    /// Every entry with its reading.
    pub fn entries(&self) -> impl Iterator<Item = (&(usize, Cost, String), R)> {
        self.0.iter().map(|x| (&x.0, x.1.1))
    }

    /// Every entry with its branch and reading.
    pub fn with_branches(&self) -> impl Iterator<Item = &((usize, Cost, String), (u8, R))> {
        self.0.iter()
    }

    /// The entries, owned.
    pub fn into_outputs(self) -> impl Iterator<Item = (usize, Cost, String)> {
        self.0.into_iter().map(|x| x.0)
    }

    /// The entries with their branch and reading, owned.
    pub fn into_entries(self) -> impl Iterator<Item = ((usize, Cost, String), (u8, R))> {
        self.0.into_iter()
    }

    pub fn is_empty(&self) -> bool {
        self.0.is_empty()
    }
}

/// Index of `r` in `t.rows()`; `r` must be one of them (`Table::prefix_matches` ties its rows' lifetime to the
/// text, so readings keep the index instead).
pub fn row_index(t: &Table, r: &Row) -> u16 {
    let i = (std::ptr::from_ref(r).addr() - t.rows().as_ptr().addr()) / size_of::<Row>();
    debug_assert!(std::ptr::eq(&t.rows()[i], r));
    i as u16
}

fn is_consonant(c: char) -> bool {
    "bcdfghjklmnpqrstvwxyz".contains(c)
}

/// `SINGULAR_TO_PLURAL` (priority unions: suppletive, `Cy`→`Cies`, sibilant + `es`, + `s`).
pub fn plural(w: &str) -> String {
    if let Some(r) = SUPPLETIVE.exact(w).next() {
        return r.value.clone();
    }
    let chars: Vec<char> = w.chars().collect();
    let n = chars.len();
    if n >= 2 && chars[n - 1] == 'y' && is_consonant(chars[n - 2]) {
        return format!("{}ies", &w[..w.len() - 1]);
    }
    if w.ends_with('s') || w.ends_with("sh") || w.ends_with("ch") || w.ends_with('x') || w.ends_with('z') {
        return format!("{w}es");
    }
    format!("{w}s")
}

/// A table plus its plural values (`graph @ SINGULAR_TO_PLURAL`), built once.
pub struct Plural {
    pub singular: &'static Table,
    pub plural: Table,
}

impl Plural {
    pub fn new(t: &'static Table) -> Plural {
        let rows = t
            .rows()
            .iter()
            .map(|r| crate::text::Row { key: r.key.clone(), value: plural(&r.value), cost: r.cost })
            .collect();
        Plural { singular: t, plural: Table::from_rows(rows) }
    }
}

pub type LazyPlural = LazyLock<Plural>;

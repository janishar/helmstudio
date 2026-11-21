//! `it/taggers/whitelist.py` (cased, deterministic): `load_labels` rows as a `string_map`, values `convert_space`d.

use std::sync::LazyLock;

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Table, space_to_nbsp};

crate::labels!(WHITELIST, "it/whitelist/whitelist.tsv");

static GRAPH: LazyLock<Table> = LazyLock::new(|| {
    Table::from_pairs(WHITELIST.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), space_to_nbsp(&r[1]))))
});

pub struct Whitelist {
    pub cost: Cost,
}

impl Classifier for Whitelist {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        for (r, end) in GRAPH.prefix_matches(text, start) {
            out.push(Candidate { end, cost: self.cost + r.cost, rank: 0, tag: format!("name: \"{}\"", r.value) });
        }
    }
}

//! `ko/taggers/whitelist.py`.

use std::sync::LazyLock;

use crate::lang::ko::graph_utils::*;
use crate::text::Table;

static WHITELIST: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("whitelist.tsv")));

/// The chars a whitelist span can start with.
pub fn lead_chars() -> Vec<char> {
    WHITELIST.rows().iter().filter_map(|r| r.key.chars().next()).collect()
}

pub fn tag(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    for (row, e) in WHITELIST.prefix_matches(t, p) {
        add(&mut r, e, format!("name: \"{}\"", row.value), row.cost);
    }
    r
}

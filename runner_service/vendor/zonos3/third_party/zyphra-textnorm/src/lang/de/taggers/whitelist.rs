//! `de/taggers/whitelist.py` (cased, deterministic): `string_map(load_labels("data/whitelist.tsv"))`,
//! `convert_space` on the output.

use std::sync::LazyLock;

use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, Table, cost, space_to_nbsp};

crate::labels!(WHITELIST_ROWS, "de/whitelist.tsv");

static WHITELIST: LazyLock<Table> = LazyLock::new(|| {
    Table::from_pairs(WHITELIST_ROWS.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), space_to_nbsp(&r[1]))))
});

pub const WEIGHT: Cost = cost(1.01);

pub struct WhiteList;

/// The first chars of the keys (sorted).
static FIRST: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = WHITELIST.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

impl Spans for WhiteList {
    fn may_start(&self, c: char) -> bool {
        FIRST.binary_search(&c).is_ok()
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if FIRST.binary_search(&text[start]).is_err() {
            return;
        }
        for (row, end) in WHITELIST.prefix_matches(text, start) {
            if end > start {
                out.push(end, WEIGHT, || format!("name: \"{}\"", row.value));
            }
        }
    }
}

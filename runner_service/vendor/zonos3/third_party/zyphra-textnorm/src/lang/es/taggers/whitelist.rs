//! `es/taggers/whitelist.py` (cased, deterministic, `load_labels` + `string_map`).

use crate::lang::es::{FirstChars, Lazy, Sink};
use crate::text::{Table, cost, space_to_nbsp};

crate::labels!(WHITELIST_ROWS, "es/whitelist.tsv");

static WHITELIST: std::sync::LazyLock<Table> = std::sync::LazyLock::new(|| {
    Table::from_pairs(WHITELIST_ROWS.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone())))
});

static FIRST: std::sync::LazyLock<FirstChars> = std::sync::LazyLock::new(|| FirstChars::of_table(&WHITELIST));

pub struct Whitelist;

impl Lazy for Whitelist {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if !FIRST.at(text, start) {
            return;
        }
        for (row, end) in WHITELIST.prefix_matches(text, start) {
            if end > start {
                out.push(end, cost(1.01), 0, || format!("name: \"{}\"", space_to_nbsp(&row.value)));
            }
        }
    }
}

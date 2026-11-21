//! `zh/taggers/whitelist.py`: `name: "<value>"` for a `data/whitelist.tsv` key (`load_labels`), spaces in the value
//! as NBSP. The second copy of the table at +0.0001 never wins and is not emitted.

use std::sync::LazyLock;

use super::{Gen, Sink};
use crate::text::{Row, Table, space_to_nbsp};

crate::labels!(WHITELIST_ROWS, "zh/whitelist.tsv");

static WHITELIST: LazyLock<Table> = LazyLock::new(|| {
    let mut rows: Vec<Row> = Vec::new();
    for r in WHITELIST_ROWS.iter() {
        let [k, v, ..] = r.as_slice() else { continue };
        if !rows.iter().any(|x| x.key == *k && x.value == *v) {
            rows.push(Row { key: k.clone(), value: space_to_nbsp(v), cost: 0 });
        }
    }
    Table::from_rows(rows)
});

/// Whether an ASCII char starts a key (every key starts with an ASCII char).
static FIRST: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut a = [false; 128];
    for r in WHITELIST.rows() {
        let c = r.key.chars().next().expect("non-empty key");
        assert!(c.is_ascii(), "whitelist key {:?} starts with a non-ASCII char", r.key);
        a[c as usize] = true;
    }
    a
});

pub struct WhiteList;

impl Gen for WhiteList {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        if !c.is_ascii() || !FIRST[c as usize] {
            return;
        }
        for (row, e) in WHITELIST.prefix_matches(t, start) {
            sink.raw(e, row.cost, 0, |s| {
                s.push_str("name: \"");
                s.push_str(&row.value);
                s.push('"');
            });
        }
    }
}

//! `fr/taggers/whitelist.py`: `string_map(load_labels("data/whitelist.tsv"))`, `convert_space` on the output.

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Table, cost, space_to_nbsp};

crate::labels!(WHITELIST_ROWS, "fr/whitelist.tsv");

static WHITELIST: std::sync::LazyLock<Table> = std::sync::LazyLock::new(|| {
    Table::from_pairs(WHITELIST_ROWS.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone())))
});

pub struct WhiteList;

pub const WEIGHT: Cost = cost(1.01);

/// First chars of the whitelist keys.
static FIRST: std::sync::LazyLock<Vec<char>> =
    std::sync::LazyLock::new(|| WHITELIST.rows().iter().filter_map(|r| r.key.chars().next()).collect());

impl Classifier for WhiteList {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !FIRST.contains(&text[start]) {
            return;
        }
        for (row, end) in WHITELIST.prefix_matches(text, start) {
            if end > start {
                out.push(Candidate {
                    end,
                    cost: WEIGHT,
                    rank: 0,
                    tag: format!("name: \"{}\"", space_to_nbsp(&row.value)),
                });
            }
        }
    }
}

static SPACED: std::sync::LazyLock<Table> = std::sync::LazyLock::new(|| {
    Table::from_pairs(WHITELIST.rows().iter().filter(|r| r.key.contains(' ')).map(|r| (r.key.clone(), String::new())))
});

static SPACED_FIRST: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let mut v: Vec<char> = SPACED.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

/// Whether a whitelist key containing a space matches at `start`.
pub fn spaced_key_at(text: &[char], start: usize) -> bool {
    text.get(start).is_some_and(|c| SPACED_FIRST.contains(c)) && SPACED.prefix_matches(text, start).next().is_some()
}

static SPACED_TAILS: std::sync::LazyLock<Vec<Vec<char>>> = std::sync::LazyLock::new(|| {
    WHITELIST.rows().iter().filter_map(|r| r.key.rsplit_once(' ').map(|(_, t)| t.chars().collect())).collect()
});

/// Whether `s` is the part after the last space of a whitelist key containing a space.
pub fn is_spaced_key_tail(s: &[char]) -> bool {
    s.first().is_some_and(|f| SPACED_TAILS.iter().any(|t| t.first() == Some(f) && t[..] == *s))
}

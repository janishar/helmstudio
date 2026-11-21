//! Whitelist classifier for Hindi.
use crate::lattice::Candidate;
use crate::text::cost;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

const ABBR_DATA: &str = include_str!("../../../data/hi/whitelist/abbreviations.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static ABBR_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(ABBR_DATA).collect());
static SPACE_PREFIXES: LazyLock<HashSet<(&'static str, char)>> = LazyLock::new(|| {
    ABBR_MAP
        .keys()
        .flat_map(|&key| {
            key.char_indices().filter(|&(_, c)| crate::text::is_nemo_space(c)).map(move |(i, c)| (&key[..i], c))
        })
        .collect()
});
static PREFIXES: LazyLock<super::trie::Trie> = LazyLock::new(|| super::trie::Trie::from_keys(ABBR_MAP.keys().copied()));
pub(crate) fn possible_prefix(s: &str) -> bool {
    PREFIXES.has_prefix(s)
}

pub(crate) fn possible_start(s: &str) -> bool {
    ABBR_MAP.contains_key(s)
}
pub(crate) fn continues_after_space(s: &str, next: char) -> bool {
    SPACE_PREFIXES.contains(&(s, next))
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(&replacement) = ABBR_MAP.get(s) {
        let clean = super::lazy::tag(|| replacement.replace(' ', "\u{00a0}"));
        out.push(Candidate { end, cost: cost(1.01), rank: 0, tag: super::lazy::tag(|| format!("name: \"{clean}\"")) });
    }
    let bare = s.strip_suffix('.').unwrap_or(s);
    for separator in [".", ". "] {
        let parts: Vec<&str> = bare.split(separator).collect();
        if parts.len() >= 3 && parts.iter().all(|p| p.len() == 1 && p.as_bytes()[0].is_ascii_uppercase()) {
            out.push(Candidate {
                end,
                cost: cost(1.01),
                rank: 0,
                tag: super::lazy::tag(|| format!("name: \"{}\"", parts.concat())),
            });
        }
    }
}

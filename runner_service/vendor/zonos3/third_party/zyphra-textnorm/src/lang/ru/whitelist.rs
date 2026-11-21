// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Whitelist classifier, ported from ru/taggers/whitelist.py.

use crate::lang::common::{possible_token_end, possible_token_start};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

crate::labels!(RAW_WHITELIST, "ru/whitelist.tsv");
crate::labels!(RAW_LATIN_TO_CYRILLIC, "ru/latin_to_cyrillic.tsv");

#[derive(Clone, Debug)]
pub struct WhitelistEntry {
    pub written: String,
    pub spoken: String,
    pub cost: Cost,
}

pub static WHITELIST_ENTRIES: LazyLock<Vec<WhitelistEntry>> = LazyLock::new(|| {
    let mut entries = Vec::new();
    let mut seen = HashSet::new();

    for row in RAW_WHITELIST.iter() {
        if row.len() < 2 {
            continue;
        }
        let written = &row[0];
        let spoken = &row[1];

        let mut variants = Vec::new();
        let lower = written.to_lowercase();
        variants.push(lower.clone());
        if *written != lower {
            variants.push(written.clone());
        }
        let mut chars = written.chars();
        if let Some(first) = chars.next() {
            let cap = format!("{}{}", first.to_uppercase(), chars.as_str());
            if !variants.contains(&cap) {
                variants.push(cap);
            }
        }

        for v in variants {
            let is_dup = !seen.insert(v.clone());
            let c = if is_dup { cost(1.01 + 0.1) } else { cost(1.01) };
            entries.push(WhitelistEntry { written: v, spoken: spoken.replace(' ', "\u{a0}"), cost: c });
        }
    }

    // Standalone units
    for u in super::units::UNITS.iter() {
        let is_multi = u.written.chars().count() >= 2;
        let is_non_ru = u.written.chars().next().is_some_and(|c| !is_ru_alpha(c));
        if is_multi || is_non_ru {
            entries.push(WhitelistEntry {
                written: u.written.clone(),
                spoken: u.one.replace(' ', "\u{a0}"),
                cost: cost(1.01),
            });
        }
    }

    entries
});

pub static WHITELIST_MAP: LazyLock<HashMap<String, Vec<(String, Cost)>>> = LazyLock::new(|| {
    let mut map: HashMap<String, Vec<(String, Cost)>> = HashMap::new();
    for e in WHITELIST_ENTRIES.iter() {
        map.entry(e.written.clone()).or_default().push((e.spoken.clone(), e.cost));
    }
    map
});
pub(super) static MAX_RANK: LazyLock<Option<i32>> = LazyLock::new(|| {
    WHITELIST_MAP.values().filter_map(|v| i32::try_from(v.len()).ok().and_then(|n| n.checked_sub(1))).max()
});

#[derive(Default)]
struct TrieNode {
    next: HashMap<char, usize>,
    readings: Option<&'static [(String, Cost)]>,
}
static WHITELIST_TRIE: LazyLock<Vec<TrieNode>> = LazyLock::new(|| {
    let mut nodes = vec![TrieNode::default()];
    for (key, readings) in WHITELIST_MAP.iter() {
        let mut node = 0;
        for c in key.chars() {
            node = if let Some(&next) = nodes[node].next.get(&c) {
                next
            } else {
                let next = nodes.len();
                nodes.push(TrieNode::default());
                nodes[node].next.insert(c, next);
                next
            };
        }
        nodes[node].readings = Some(readings);
    }
    nodes
});

pub static LATIN_TO_CYRILLIC: LazyLock<Vec<(char, &'static str, Cost)>> = LazyLock::new(|| {
    let mut seen = HashSet::new();
    let mut out = Vec::new();
    for row in RAW_LATIN_TO_CYRILLIC.iter() {
        if row.len() < 2 {
            continue;
        }
        let ch = row[0].chars().next().unwrap();
        let spoken: &'static str = row[1].as_str();
        let is_dup = !seen.insert(ch);
        let c = if is_dup { cost(1.01 + 0.1) } else { cost(1.01) };
        out.push((ch, spoken, c));
    }
    out
});

#[inline]
pub fn is_ru_alpha(c: char) -> bool {
    matches!(c, 'а'..='я' | 'А'..='Я' | 'ё' | 'Ё')
}

pub struct WhitelistClassifier;

impl Classifier for WhitelistClassifier {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !possible_token_start(text, start) {
            return;
        }

        // 1. Whitelist entries and standalone units
        let mut node = 0;
        for end in start..text.len() {
            let Some(&next) = WHITELIST_TRIE[node].next.get(&text[end]) else { break };
            node = next;
            if possible_token_end(text, end + 1) {
                if let Some(readings) = WHITELIST_TRIE[node].readings {
                    for (idx, (_, c)) in readings.iter().enumerate() {
                        out.push(Candidate { end: end + 1, cost: *c, rank: idx as i32, tag: String::new() });
                    }
                }
            }
        }

        // 2. Sequence of Latin letters -> spoken names
        if text[start].is_ascii_alphabetic() {
            let mut end = start;
            while end < text.len() && text[end].is_ascii_alphabetic() {
                end += 1;
            }
            if possible_token_end(text, end) {
                // Primary reading of each latin letter
                out.push(Candidate { end, cost: cost(1.01), rank: -1, tag: String::new() });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let s: String = text[start..candidate.end].iter().collect();

        if candidate.rank >= 0 {
            if let Some(readings) = WHITELIST_MAP.get(&s) {
                let idx = candidate.rank as usize;
                if let Some((spoken, _)) = readings.get(idx) {
                    candidate.tag = format!("name: \"{spoken}\"");
                    return;
                }
            }
        }

        if s.chars().all(|c| c.is_ascii_alphabetic()) {
            let mut spoken = Vec::new();
            for c in s.chars() {
                let lower = c.to_ascii_lowercase();
                if let Some((_, sp, _)) = LATIN_TO_CYRILLIC.iter().find(|(ch, _, _)| *ch == lower) {
                    spoken.push(*sp);
                }
            }
            let sp_str = spoken.join(" ").replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{sp_str}\"");
        }
    }
}

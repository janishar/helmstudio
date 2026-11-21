// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese whitelist classifier: exact abbreviation and symbol mappings.

use super::super::graph_utils::{WEIGHT_WHITELIST, space_to_nbsp};
use crate::lattice::{Candidate, Classifier};
use std::collections::HashMap;
use std::sync::LazyLock;

pub struct Whitelist;
crate::labels!(WHITELIST_LABELS, "vi/whitelist.tsv");

static INDEX: LazyLock<HashMap<char, Vec<&'static Vec<String>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static Vec<String>>> = HashMap::new();
    for row in WHITELIST_LABELS.iter() {
        if row.len() >= 2 {
            if let Some(first) = row[0].chars().next() {
                index.entry(first).or_default().push(row);
            }
        }
    }
    index
});

fn scan(text: &[char], start: usize, mut f: impl FnMut(usize, &str)) {
    let Some(rows) = text.get(start).and_then(|c| INDEX.get(c)) else { return };
    for row in rows {
        let len = row[0].chars().count();
        let end = start + len;
        if text.get(start..end).is_some_and(|part| part.iter().copied().eq(row[0].chars())) {
            f(end, &row[1]);
        }
    }
}

impl Classifier for Whitelist {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        scan(text, start, |end, _| out.push(Candidate { end, cost: WEIGHT_WHITELIST, rank: 0, tag: String::new() }));
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        scan(text, start, |end, value| {
            if end == candidate.end && candidate.tag.is_empty() {
                candidate.tag = format!("whitelist {{ name: \"{}\" }}", space_to_nbsp(value));
            }
        });
    }
}

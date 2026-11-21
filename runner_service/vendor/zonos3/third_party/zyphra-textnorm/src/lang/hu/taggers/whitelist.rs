// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use crate::lang::hu::graph_utils::{is_hu_alpha, naive_inflector};
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.01);

#[derive(Default)]
struct WhitelistNode {
    next: HashMap<char, usize>,
    parent: usize,
    value: Option<String>,
    alpha: bool,
}

static WHITELIST: LazyLock<Vec<WhitelistNode>> = LazyLock::new(|| {
    let mut map: HashMap<String, String> = HashMap::new();

    // 1. whitelist.tsv
    let tsv_wl = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/whitelist.tsv"));
    for line in tsv_wl.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if let Some((k, v)) = line.split_once('\t') {
            map.entry(k.to_string()).or_insert_with(|| v.to_string());
        }
    }

    // 2. whitelist_inflect.tsv
    let tsv_inf = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/whitelist_inflect.tsv"));
    for line in tsv_inf.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if let Some((k, v)) = line.split_once('\t') {
            map.entry(k.to_string()).or_insert_with(|| v.to_string());
            if !v.contains(' ') {
                for (ik, iv) in naive_inflector(k, v, false) {
                    map.entry(ik).or_insert(iv);
                }
            }
        }
    }

    // 3. whitelist_inflect_sg.tsv
    let tsv_sg = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/whitelist_inflect_sg.tsv"));
    for line in tsv_sg.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if let Some((k, v)) = line.split_once('\t') {
            map.entry(k.to_string()).or_insert_with(|| v.to_string());
            if !v.contains(' ') {
                for (ik, iv) in naive_inflector(k, v, true) {
                    map.entry(ik).or_insert(iv);
                }
            }
        }
    }

    let mut nodes = vec![WhitelistNode::default()];
    for (key, value) in map {
        let mut node = 0;
        for c in key.chars() {
            node = match nodes[node].next.get(&c) {
                Some(&next) => next,
                None => {
                    let next = nodes.len();
                    nodes.push(WhitelistNode { parent: node, ..WhitelistNode::default() });
                    nodes[node].next.insert(c, next);
                    next
                }
            };
        }
        nodes[node].alpha = key.chars().last().is_some_and(is_hu_alpha);
        nodes[node].value = Some(value);
    }
    nodes
});

pub struct WhiteList;

impl Lazy for WhiteList {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let mut node = 0;
        let mut end = start;
        while let Some(&c) = text.get(end) {
            let Some(&next) = WHITELIST[node].next.get(&c) else { break };
            node = next;
            end += 1;
        }
        while node != 0 {
            let entry = &WHITELIST[node];
            if let Some(value) = &entry.value {
                if !entry.alpha || !text.get(end).copied().is_some_and(is_hu_alpha) {
                    out.push(end, WEIGHT, 0, || format!("name: \"{value}\""));
                }
            }
            node = entry.parent;
            end -= 1;
        }
    }
}

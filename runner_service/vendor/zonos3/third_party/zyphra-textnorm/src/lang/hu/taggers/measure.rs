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
use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::taggers::decimal;
use crate::lang::hu::taggers::fraction;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.08);

#[derive(Default)]
struct UnitNode {
    next: HashMap<char, usize>,
    parent: usize,
    value: Option<String>,
    alpha: bool,
}

struct UnitsData {
    base: Vec<UnitNode>,
    all: Vec<UnitNode>,
    max_base_len: usize,
}

fn unit_trie(entries: HashMap<String, String>) -> Vec<UnitNode> {
    let mut nodes = vec![UnitNode::default()];
    for (key, value) in entries {
        let mut node = 0;
        for c in key.chars() {
            node = match nodes[node].next.get(&c) {
                Some(&next) => next,
                None => {
                    let next = nodes.len();
                    nodes.push(UnitNode { parent: node, ..UnitNode::default() });
                    nodes[node].next.insert(c, next);
                    next
                }
            };
        }
        nodes[node].alpha = key.chars().last().is_some_and(is_hu_alpha);
        nodes[node].value = Some(value);
    }
    nodes
}

static UNITS: LazyLock<UnitsData> = LazyLock::new(|| {
    let tsv = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/measures/measurements.tsv"));

    let mut base_dict: HashMap<String, String> = HashMap::new();
    let mut all_dict: HashMap<String, String> = HashMap::new();

    let mut rows = Vec::new();
    for line in tsv.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        if let Some((k, v)) = line.split_once('\t') {
            rows.push((k.to_string(), v.to_string()));
        }
    }

    for (k, v) in &rows {
        base_dict.entry(k.clone()).or_insert_with(|| v.clone());
        all_dict.entry(k.clone()).or_insert_with(|| v.clone());
        if !v.contains(' ') {
            for (ik, iv) in naive_inflector(k, v, false) {
                all_dict.entry(ik).or_insert(iv);
            }
        }
    }

    for (k, v) in &rows {
        let lk = k.to_lowercase();
        base_dict.entry(lk.clone()).or_insert_with(|| v.clone());
        all_dict.entry(lk.clone()).or_insert_with(|| v.clone());
        if !v.contains(' ') {
            for (ik, iv) in naive_inflector(&lk, v, false) {
                all_dict.entry(ik).or_insert(iv);
            }
        }
    }

    let max_base_len = base_dict.keys().map(|key| key.chars().count()).max().unwrap_or(0);
    UnitsData { base: unit_trie(base_dict), all: unit_trie(all_dict), max_base_len }
});

#[derive(Clone, Copy)]
struct UnitMatch {
    end: usize,
    numerator: Option<&'static str>,
    denominator: Option<&'static str>,
    is_exact_s: bool,
}

impl UnitMatch {
    fn render(self) -> String {
        match (self.numerator, self.denominator) {
            (Some(n), Some(d)) => format!("{n} per {d}").replace(' ', "\u{a0}"),
            (None, Some(d)) => format!("per {d}").replace(' ', "\u{a0}"),
            (Some(n), None) => n.replace(' ', "\u{a0}"),
            (None, None) => unreachable!(),
        }
    }
}

fn matching_units<'a>(
    entries: &'static [UnitNode],
    text: &'a [char],
    p: usize,
) -> impl Iterator<Item = (usize, &'static str)> + 'a {
    let mut node = 0;
    let mut end = p;
    while let Some(&c) = text.get(end) {
        let Some(&next) = entries[node].next.get(&c) else { break };
        node = next;
        end += 1;
    }
    std::iter::from_fn(move || {
        while node != 0 {
            let current = node;
            let current_end = end;
            node = entries[node].parent;
            end -= 1;
            if let Some(value) = &entries[current].value {
                if !entries[current].alpha || !text.get(current_end).copied().is_some_and(is_hu_alpha) {
                    return Some((current_end, value.as_str()));
                }
            }
        }
        None
    })
}

fn match_unit_expression(text: &[char], p: usize, mut emit: impl FnMut(UnitMatch)) {
    let has_slash = text.get(p..).is_some_and(|rest| rest.iter().take(UNITS.max_base_len + 1).any(|&c| c == '/'));
    if has_slash {
        for (base_end, base) in matching_units(&UNITS.base, text, p) {
            if text.get(base_end) == Some(&'/') {
                for (end, denominator) in matching_units(&UNITS.all, text, base_end + 1) {
                    emit(UnitMatch { end, numerator: Some(base), denominator: Some(denominator), is_exact_s: false });
                }
            }
        }
    }
    if text.get(p) == Some(&'/') {
        for (end, denominator) in matching_units(&UNITS.all, text, p + 1) {
            emit(UnitMatch { end, numerator: None, denominator: Some(denominator), is_exact_s: false });
        }
    }
    for (end, numerator) in matching_units(&UNITS.all, text, p) {
        emit(UnitMatch {
            end,
            numerator: Some(numerator),
            denominator: None,
            is_exact_s: end == p + 1 && text[p] == 's',
        });
    }
}

fn is_glued_intl_decade(n: u128) -> bool {
    // 18x0, 19x0, 20x0
    if n % 10 != 0 {
        return false;
    }
    let cent = n / 100;
    cent == 18 || cent == 19 || cent == 20
}

pub struct Measure;

#[derive(Clone, Copy)]
enum Integer {
    Number(u128),
    Range { first: u128, second: u128, negative_second: bool },
}

impl Integer {
    fn render(self, counted: bool) -> String {
        let mut first = String::new();
        match self {
            Self::Number(n) => {
                if counted {
                    cardinal::spell_counted_number(n, &mut first);
                } else {
                    cardinal::spell_number(n, &mut first);
                }
                first
            }
            Self::Range { first: a, second: b, negative_second } => {
                cardinal::spell_counted_number(a, &mut first);
                let mut second = String::new();
                cardinal::spell_counted_number(b, &mut second);
                let sign = if negative_second { "mínusz " } else { "" };
                format!("{} {sign}{}", cardinal::from_suffix(&first), cardinal::to_suffix(&second))
            }
        }
    }
}

fn integers(text: &[char], start: usize, mut emit: impl FnMut(usize, Cost, Integer)) {
    for (end, first) in cardinal::numbers(text, start) {
        let weight = cardinal::number_cost(first);
        emit(end, weight, Integer::Number(first));
        if matches!(text.get(end), Some('-' | '–' | '—')) {
            let mut p = end + 1;
            let negative_second = matches!(text.get(p), Some('-' | '−'));
            p += usize::from(negative_second);
            for (end, second) in cardinal::numbers(text, p) {
                emit(end, weight + cardinal::number_cost(second), Integer::Range { first, second, negative_second });
            }
        }
    }
}

fn unit_starts(text: &[char], end: usize) -> impl Iterator<Item = usize> {
    std::iter::once(end).chain((text.get(end) == Some(&' ')).then_some(end + 1))
}

impl Lazy for Measure {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }
        if is_hu_alpha(text[start]) {
            let mut alpha_end = start;
            while alpha_end < text.len() && is_hu_alpha(text[alpha_end]) {
                alpha_end += 1;
            }
            if text.get(alpha_end) == Some(&'-') {
                let after = alpha_end + 1;
                for (end, number) in cardinal::numbers(text, after) {
                    out.push(end, WEIGHT + cardinal::number_cost(number), 2, || {
                        let alpha: String = text[start..alpha_end].iter().collect();
                        let mut spoken = String::new();
                        cardinal::spell_number(number, &mut spoken);
                        format!(
                            "measure {{ units: \"{alpha}\" cardinal {{ integer: \"{spoken}\" }} preserve_order: true }}"
                        )
                    });
                    if out.found.is_some() {
                        return;
                    }
                }
                decimal::parse_raw_decimal(text, after, |raw| {
                    if raw.negative || raw.separator == Some('.') {
                        return;
                    }
                    out.push(raw.end, WEIGHT + raw.cost, 3, || {
                        let alpha: String = text[start..alpha_end].iter().collect();
                        format!("measure {{ units: \"{alpha}\" decimal {{ {} }} preserve_order: true }}", raw.render())
                    });
                });
            }
            return;
        }

        decimal::parse_raw_decimal(text, start, |raw| {
            if out.found.is_some() {
                return;
            }
            let end = raw.end;
            if !raw.negative && raw.separator != Some('.') {
                if matches!(text.get(end), Some('x' | 'X')) {
                    out.push(end + 1, WEIGHT + raw.cost, 1, || {
                        format!(
                            "measure {{ decimal {{ {} }} units: \"{}\" preserve_order: true }}",
                            raw.render(),
                            text[end]
                        )
                    });
                }
                if text.get(end) == Some(&'-') {
                    let mut alpha_end = end + 1;
                    while alpha_end < text.len() && is_hu_alpha(text[alpha_end]) {
                        alpha_end += 1;
                    }
                    if alpha_end > end + 1 {
                        out.push(alpha_end, WEIGHT + raw.cost, 1, || {
                            let alpha: String = text[end + 1..alpha_end].iter().collect();
                            format!(
                                "measure {{ decimal {{ {} }} units: \"{alpha}\" preserve_order: true }}",
                                raw.render()
                            )
                        });
                    }
                }
            }
            for p in unit_starts(text, end) {
                match_unit_expression(text, p, |unit| {
                    out.push(unit.end, WEIGHT + raw.cost, 0, || {
                        format!(
                            "measure {{ decimal {{ {} preserve_order: true }} units: \"{}\" preserve_order: true }}",
                            raw.render(),
                            unit.render()
                        )
                    });
                });
            }
        });
        if out.found.is_some() {
            return;
        }

        fraction::parse_raw_fraction(text, start, |raw| {
            if out.found.is_some() {
                return;
            }
            for p in unit_starts(text, raw.end) {
                match_unit_expression(text, p, |unit| {
                    out.push(unit.end, WEIGHT + raw.cost, 0, || {
                        format!(
                            "measure {{ fraction {{ {} }} units: \"{}\" preserve_order: true }}",
                            raw.render(),
                            unit.render()
                        )
                    });
                });
            }
        });
        if out.found.is_some() {
            return;
        }

        let negative = matches!(text.get(start), Some('-' | '−'));
        let p = start + usize::from(negative);
        integers(text, p, |end, weight, amount| {
            if out.found.is_some() {
                return;
            }
            if let Integer::Number(number) = amount {
                if !negative && matches!(text.get(end), Some('x' | 'X')) {
                    out.push(end + 1, WEIGHT + weight, 1, || {
                        format!(
                            "measure {{ cardinal {{ integer: \"{}\" }} units: \"{}\" preserve_order: true }}",
                            amount.render(false),
                            text[end]
                        )
                    });
                }
                if !negative && text.get(end) == Some(&'-') {
                    let mut alpha_end = end + 1;
                    while alpha_end < text.len() && is_hu_alpha(text[alpha_end]) {
                        alpha_end += 1;
                    }
                    if alpha_end > end + 1 {
                        let is_case = cardinal::CASE_SUFFIXES
                            .iter()
                            .any(|suffix| text[end + 1..alpha_end].iter().copied().eq(suffix.chars()));
                        if !is_case {
                            out.push(alpha_end, WEIGHT + weight, 1, || {
                                let alpha: String = text[end + 1..alpha_end].iter().collect();
                                format!("measure {{ cardinal {{ integer: \"{}\" }} units: \"{alpha}\" preserve_order: true }}", amount.render(false))
                            });
                        }
                    }
                }
                for unit_start in unit_starts(text, end) {
                    match_unit_expression(text, unit_start, |unit| {
                        if unit_start == end && unit.is_exact_s && is_glued_intl_decade(number) {
                            return;
                        }
                        out.push(unit.end, WEIGHT + weight, 0, || {
                            let sign = if negative { "negative: \"true\" " } else { "" };
                            format!(
                                "measure {{ cardinal {{ {sign}integer: \"{}\" }} units: \"{}\" preserve_order: true }}",
                                amount.render(true),
                                unit.render()
                            )
                        });
                    });
                }
            } else {
                for unit_start in unit_starts(text, end) {
                    match_unit_expression(text, unit_start, |unit| {
                        out.push(unit.end, WEIGHT + weight, 0, || {
                            let sign = if negative { "negative: \"true\" " } else { "" };
                            format!(
                                "measure {{ cardinal {{ {sign}integer: \"{}\" }} units: \"{}\" preserve_order: true }}",
                                amount.render(true),
                                unit.render()
                            )
                        });
                    });
                }
            }
        });
    }
}

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
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.10);

const DENOMINATORS: [&str; 24] = [
    "",
    "tized",
    "század",
    "ezred",
    "tízezred",
    "százezred",
    "milliomod",
    "tízmilliomod",
    "százmilliomod",
    "milliárdod",
    "tízmilliárdod",
    "százmilliárdod",
    "billiomod",
    "tízbilliomod",
    "százbilliomod",
    "billiárdod",
    "tízbilliárdod",
    "százbilliárdod",
    "trilliomod",
    "tíztrilliomod",
    "száztrilliomod",
    "trilliárdod",
    "tíztrilliárdod",
    "száztrilliárdod",
];

pub fn spell_fractional_part(frac_digits: &[char]) -> Option<String> {
    if frac_digits.is_empty() {
        return None;
    }
    let mut last_nonzero = None;
    for (idx, &c) in frac_digits.iter().enumerate() {
        if c != '0' {
            last_nonzero = Some(idx + 1);
        }
    }
    let last_pos = match last_nonzero {
        Some(pos) => pos,
        None if frac_digits.len() >= 2 => 2,
        None => return None,
    };
    if last_pos >= DENOMINATORS.len() {
        return None;
    }
    if last_pos >= 3 && frac_digits[0] == '0' && frac_digits[1] == '0' {
        return None;
    }
    let den_word = DENOMINATORS[last_pos];
    let s_sig: String = frac_digits[..last_pos].iter().collect();
    let num_val: u128 = s_sig.parse().ok()?;
    let mut num_sp = String::new();
    cardinal::spell_number(num_val, &mut num_sp);
    Some(format!("{num_sp} {den_word}"))
}

pub static QUANTITIES: LazyLock<HashMap<String, String>> = LazyLock::new(|| {
    let tsv = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/number/quantities.tsv"));
    let mut map = HashMap::new();
    for line in tsv.lines() {
        let line = line.trim();
        if line.is_empty() || line.starts_with('#') {
            continue;
        }
        let parts: Vec<&str> = line.split('\t').collect();
        if parts.len() == 2 {
            let key = parts[0];
            let val = parts[1];
            map.insert(key.to_string(), val.to_string());
            for (input, iv) in naive_inflector(key, val, true) {
                map.insert(input, iv);
            }
        } else if parts.len() == 1 {
            let word = parts[0];
            map.insert(word.to_string(), word.to_string());
            for (_, iv) in naive_inflector(".", word, true) {
                map.insert(iv.clone(), iv.clone());
                if word.contains("lli") {
                    let alt = word.replace("lli", "li");
                    map.insert(alt, word.to_string());
                    let alt_iv = iv.replace("lli", "li");
                    map.insert(alt_iv, iv);
                }
            }
        }
    }
    map
});
#[derive(Default)]
struct QuantityNode {
    next: HashMap<char, usize>,
    value: Option<&'static str>,
}

static QUANTITY_TRIE: LazyLock<Vec<QuantityNode>> = LazyLock::new(|| {
    let mut nodes = vec![QuantityNode::default()];
    for (key, value) in QUANTITIES.iter() {
        let mut node = 0;
        for c in key.chars() {
            node = match nodes[node].next.get(&c) {
                Some(&next) => next,
                None => {
                    let next = nodes.len();
                    nodes.push(QuantityNode::default());
                    nodes[node].next.insert(c, next);
                    next
                }
            };
        }
        nodes[node].value = Some(value.as_str());
    }
    nodes
});

fn quantities(text: &[char], begin: usize) -> impl Iterator<Item = (usize, &'static str)> + '_ {
    let mut end = begin + 1;
    let mut node = 0;
    let mut valid = text.get(begin) == Some(&' ');
    std::iter::from_fn(move || {
        while valid {
            let Some(&c) = text.get(end).filter(|c| is_hu_alpha(**c)) else {
                valid = false;
                break;
            };
            let Some(&next) = QUANTITY_TRIE[node].next.get(&c) else {
                valid = false;
                break;
            };
            node = next;
            end += 1;
            if let Some(value) = QUANTITY_TRIE[node].value {
                return Some((end, value));
            }
        }
        None
    })
}

/// A recognized decimal path. `cost` is the internal graph cost, without
/// the classifier's outer weight.
#[derive(Clone, Copy)]
pub struct RawDecimal<'a> {
    pub end: usize,
    pub cost: Cost,
    pub negative: bool,
    pub integer: u128,
    pub fractional_digits: Option<&'a [char]>,
    pub fractional_value: u128,
    pub fractional_place: usize,
    pub quantity: Option<&'static str>,
    pub separator: Option<char>,
}

impl RawDecimal<'_> {
    /// Render the unwrapped decimal field body in source field order.
    pub fn render(&self) -> String {
        let mut tag = String::new();
        if self.negative {
            tag.push_str("negative: \"true\" ");
        }
        tag.push_str("integer_part: \"");
        cardinal::spell_number(self.integer, &mut tag);
        tag.push('"');
        if self.fractional_digits.is_some() {
            tag.push_str(" fractional_part: \"");
            cardinal::spell_number(self.fractional_value, &mut tag);
            tag.push(' ');
            tag.push_str(DENOMINATORS[self.fractional_place]);
            tag.push('"');
        }
        if let Some(quantity) = self.quantity {
            tag.push_str(" quantity: \"");
            tag.push_str(quantity);
            tag.push('"');
        }
        tag
    }
}

pub fn parse_raw_decimal<'a>(text: &'a [char], start: usize, emit: impl FnMut(RawDecimal<'a>)) {
    parse_decimal(text, start, false, emit)
}

/// The money graph rewrites dots to commas before composition with the
/// decimal graph, without the standalone clock/three-digit filter.
pub fn parse_raw_money_decimal<'a>(text: &'a [char], start: usize, emit: impl FnMut(RawDecimal<'a>)) {
    parse_decimal(text, start, true, emit)
}

fn parse_decimal<'a>(text: &'a [char], start: usize, money_dot: bool, mut emit: impl FnMut(RawDecimal<'a>)) {
    let negative = matches!(text.get(start), Some('-' | '−'));
    let p = start + usize::from(negative);
    for (int_end, int_val) in cardinal::numbers(text, p) {
        if int_end - p <= 3 && int_val != 0 && text[p..int_end].iter().all(|c| c.is_ascii_digit()) {
            for (end, quantity) in quantities(text, int_end) {
                emit(RawDecimal {
                    end,
                    cost: 0,
                    negative,
                    integer: int_val,
                    fractional_digits: None,
                    fractional_value: 0,
                    fractional_place: 0,
                    quantity: Some(quantity),
                    separator: None,
                });
            }
        }
        let Some(&separator @ (',' | '.')) = text.get(int_end) else { continue };
        if separator == '.' && text[p..int_end].iter().any(|c| !c.is_ascii_digit()) {
            continue;
        }
        let begin = int_end + 1;
        let mut end = begin;
        let mut prefix_value = 0u128;
        let mut fractional_value = 0u128;
        let mut last_nonzero = 0usize;
        while end < text.len() && text[end].is_ascii_digit() {
            let digit = (text[end] as u8 - b'0') as u128;
            end += 1;
            let len = end - begin;
            if len < DENOMINATORS.len() {
                prefix_value = prefix_value * 10 + digit;
            }
            if digit != 0 {
                last_nonzero = len;
                fractional_value = prefix_value;
            }
            if !money_dot && separator == '.' && (len == 3 || (int_val <= 24 && len == 2 && text[begin] <= '5')) {
                continue;
            }
            let place = if last_nonzero == 0 {
                if len < 2 {
                    continue;
                }
                2
            } else {
                last_nonzero
            };
            if place >= DENOMINATORS.len() || (place >= 3 && text[begin] == '0' && text[begin + 1] == '0') {
                continue;
            }
            let internal = cardinal::number_cost(int_val) + cardinal::number_cost(fractional_value);
            let candidate = RawDecimal {
                end,
                cost: internal,
                negative,
                integer: int_val,
                fractional_digits: Some(&text[begin..end]),
                fractional_value,
                fractional_place: place,
                quantity: None,
                separator: Some(separator),
            };
            emit(candidate);
            if separator == ',' || money_dot {
                for (q_end, quantity) in quantities(text, end) {
                    emit(RawDecimal { end: q_end, quantity: Some(quantity), ..candidate });
                }
            }
        }
    }
    // The quantity graph also accepts a three-digit component with zero padding.
    if text.get(p) == Some(&'0') {
        let mut value = 0u128;
        let mut end = p;
        while end < text.len() && end - p < 3 && text[end].is_ascii_digit() {
            value = value * 10 + (text[end] as u8 - b'0') as u128;
            end += 1;
            if end - p > 1 && value > 0 {
                for (q_end, quantity) in quantities(text, end) {
                    emit(RawDecimal {
                        end: q_end,
                        cost: 0,
                        negative,
                        integer: value,
                        fractional_digits: None,
                        fractional_value: 0,
                        fractional_place: 0,
                        quantity: Some(quantity),
                        separator: None,
                    });
                }
            }
        }
    }
}

pub struct Decimal;

impl Lazy for Decimal {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        parse_raw_decimal(text, start, |raw| {
            out.push(raw.end, WEIGHT + raw.cost, 0, || format!("decimal {{ {} preserve_order: true }}", raw.render()));
        });
    }
}

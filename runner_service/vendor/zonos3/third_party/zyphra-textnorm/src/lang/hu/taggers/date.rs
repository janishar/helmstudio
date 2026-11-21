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

use crate::lang::hu::graph_utils::is_hu_alpha;
use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

pub const WEIGHT: Cost = cost(1.10);

const DAYS_NAMES: [&str; 32] = [
    "",
    "elseje",
    "másodika",
    "harmadika",
    "negyedike",
    "ötödike",
    "hatodika",
    "hetedike",
    "nyolcadika",
    "kilencedike",
    "tizedike",
    "tizenegyedike",
    "tizenkettedike",
    "tizenharmadika",
    "tizennegyedike",
    "tizenötödike",
    "tizenhatodika",
    "tizenhetedike",
    "tizennyolcadika",
    "tizenkilencedike",
    "huszadika",
    "huszonegyedike",
    "huszonkettedike",
    "huszonharmadika",
    "huszonnegyedike",
    "huszonötödike",
    "huszonhatodika",
    "huszonhetedike",
    "huszonnyolcadika",
    "huszonkilencedike",
    "harmincadika",
    "harmincegyedike",
];

const MONTHS: [&str; 13] = [
    "",
    "január",
    "február",
    "március",
    "április",
    "május",
    "június",
    "július",
    "augusztus",
    "szeptember",
    "október",
    "november",
    "december",
];

static MONTH_MAP: LazyLock<HashMap<String, &'static str>> = LazyLock::new(|| {
    let mut map = HashMap::new();
    const ABBRS: [&str; 13] =
        ["", "jan", "febr", "márc", "ápr", "máj", "jún", "júl", "aug", "szept", "okt", "nov", "dec"];
    const ROMANS: [&str; 13] = ["", "i", "ii", "iii", "iv", "v", "vi", "vii", "viii", "ix", "x", "xi", "xii"];
    for i in 1..=12 {
        let name = MONTHS[i];
        map.insert(name.to_string(), name);
        let mut chars = name.chars();
        let capitalized = format!("{}{}", chars.next().unwrap().to_uppercase(), chars.as_str());
        map.insert(capitalized, name);
        map.insert(format!("{}.", ABBRS[i]), name);
        map.insert(format!("{}.", ROMANS[i]), name);
        map.insert(format!("{}.", ROMANS[i].to_uppercase()), name);
    }
    map
});

static SUFFIXED_DAYS: LazyLock<HashMap<String, String>> = LazyLock::new(|| {
    let mut map = HashMap::new();
    let endings_e = "ét ének ével éért évé éig eként éül ében én énél ébe ére éhez éből éről étől";
    let endings_a = "át ának ával áért ává áig aként ául ában án ánál ába ára ához ából áról ától";

    for day_num in 1..=31 {
        let day_word = DAYS_NAMES[day_num];
        let last = day_word.chars().last().unwrap();
        let base = &day_word[..day_word.len() - last.len_utf8()];
        let endings_str = if last == 'e' { endings_e } else { endings_a };

        for ending in endings_str.split_whitespace() {
            let end_trimmed = &ending[ending.chars().next().unwrap().len_utf8()..];
            let expanded = format!("{base}{ending}");
            if day_word.ends_with("eje") {
                map.insert(format!("{day_num}-j{ending}"), expanded.clone());
                map.insert(format!("{day_num}-{ending}"), expanded.clone());
            } else {
                map.insert(format!("{day_num}-{ending}"), expanded.clone());
                map.insert(format!("{day_num}-{end_trimmed}"), expanded.clone());
            }
        }
        // Base forms: "1-je", "2-a", "4-e"
        if day_word.ends_with("je") {
            map.insert(format!("{day_num}-je"), day_word.to_string());
        } else if day_word.ends_with('a') {
            map.insert(format!("{day_num}-a"), day_word.to_string());
        } else if day_word.ends_with('e') {
            map.insert(format!("{day_num}-e"), day_word.to_string());
        }
        map.insert(day_word.to_string(), day_word.to_string());
    }
    let generated: Vec<String> = map.values().cloned().collect();
    for value in generated {
        map.entry(value.clone()).or_insert(value);
    }
    map
});

#[derive(Default)]
struct LookupNode {
    next: HashMap<char, usize>,
    value: Option<&'static str>,
}

fn lookup_trie(entries: impl Iterator<Item = (&'static str, &'static str)>) -> Vec<LookupNode> {
    let mut nodes = vec![LookupNode::default()];
    for (key, value) in entries {
        let mut node = 0;
        for c in key.chars() {
            node = match nodes[node].next.get(&c) {
                Some(&next) => next,
                None => {
                    let next = nodes.len();
                    nodes.push(LookupNode::default());
                    nodes[node].next.insert(c, next);
                    next
                }
            };
        }
        nodes[node].value = Some(value);
    }
    nodes
}

static MONTH_TRIE: LazyLock<Vec<LookupNode>> =
    LazyLock::new(|| lookup_trie(MONTH_MAP.iter().map(|(k, &v)| (k.as_str(), v))));
static DAY_TRIE: LazyLock<Vec<LookupNode>> =
    LazyLock::new(|| lookup_trie(SUFFIXED_DAYS.iter().map(|(k, v)| (k.as_str(), v.as_str()))));

fn lookup_prefix(text: &[char], start: usize, nodes: &'static [LookupNode]) -> Option<(&'static str, usize)> {
    let mut node = 0;
    let mut found = None;
    for (offset, &c) in text.get(start..)?.iter().enumerate() {
        let Some(&next) = nodes[node].next.get(&c) else { break };
        node = next;
        if let Some(value) = nodes[node].value {
            found = Some((value, start + offset + 1));
        }
    }
    found
}

pub fn match_month(text: &[char], start: usize) -> Option<(&'static str, usize)> {
    if let Some(found) = lookup_prefix(text, start, &MONTH_TRIE) {
        return Some(found);
    }
    let mut end = start;
    let mut value = 0usize;
    while let Some(c) = text.get(end).filter(|c| c.is_ascii_digit()) {
        if end - start < 2 {
            value = value * 10 + (*c as usize - '0' as usize);
        }
        end += 1;
    }
    if (1..=2).contains(&(end - start)) && (1..=12).contains(&value) && text.get(end) == Some(&'.') {
        return Some((MONTHS[value], end + 1));
    }
    None
}
fn month_endpoints(text: &[char], start: usize, mut emit: impl FnMut(&'static str, usize)) {
    let Some(&first) = text.get(start) else { return };
    if first.is_ascii_digit() {
        let mut end = start;
        let mut value = 0;
        while end < text.len() && text[end].is_ascii_digit() && end - start < 2 {
            value = value * 10 + (text[end] as usize - '0' as usize);
            end += 1;
            if (1..=12).contains(&value) && text.get(end) == Some(&'.') {
                emit(MONTHS[value], end + 1);
            }
        }
        return;
    }
    let mut node = 0;
    for (offset, &c) in text[start..].iter().enumerate() {
        let Some(&next) = MONTH_TRIE[node].next.get(&c) else { break };
        node = next;
        if let Some(value) = MONTH_TRIE[node].value {
            emit(value, start + offset + 1);
        }
    }
}

pub fn match_day(text: &[char], start: usize) -> Option<(&'static str, usize)> {
    let first = *text.get(start)?;
    if !first.is_ascii_digit() {
        return lookup_prefix(text, start, &DAY_TRIE);
    }
    let mut end = start;
    let mut value = 0usize;
    while let Some(c) = text.get(end).filter(|c| c.is_ascii_digit()) {
        if end - start < 2 {
            value = value * 10 + (*c as usize - '0' as usize);
        }
        end += 1;
    }
    if !(1..=2).contains(&(end - start)) || !(1..=31).contains(&value) {
        return None;
    }
    if text.get(end) == Some(&'-') {
        let mut suffix_end = end + 1;
        while text.get(suffix_end).copied().is_some_and(is_hu_alpha) {
            suffix_end += 1;
        }
        if let Some((word, found_end)) = lookup_prefix(&text[..suffix_end], start, &DAY_TRIE) {
            if found_end == suffix_end {
                return Some((word, suffix_end));
            }
        }
    }
    Some((DAYS_NAMES[value], end + usize::from(text.get(end) == Some(&'.'))))
}

fn numeric_piece(text: &[char], start: usize, max: usize) -> impl Iterator<Item = (usize, usize)> + '_ {
    let mut len = 0;
    let mut value = 0;
    std::iter::from_fn(move || {
        while len < 2 {
            let c = text.get(start + len).filter(|c| c.is_ascii_digit())?;
            len += 1;
            value = value * 10 + (*c as usize - '0' as usize);
            if value > 0 && value <= max {
                return Some((start + len, value));
            }
        }
        None
    })
}

pub struct Date;

impl Lazy for Date {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }

        // 1. Format: [year. ]month day
        let mut has_year = false;
        let mut year_val = 0u64;
        let mut after_year = start;

        if text[start].is_ascii_digit() {
            let mut y_end = start;
            while y_end < text.len() && text[y_end].is_ascii_digit() {
                y_end += 1;
            }
            let y_len = y_end - start;
            if y_len >= 2 && y_len <= 4 && text[start] != '0' {
                if y_end < text.len()
                    && text[y_end] == '.'
                    && y_end + 1 < text.len()
                    && crate::text::is_nemo_space(text[y_end + 1])
                {
                    has_year = true;
                    year_val = text[start..y_end].iter().fold(0, |n, c| n * 10 + (*c as u64 - '0' as u64));
                    after_year = y_end + 2;
                }
            }
        }
        if text[start] != '0' {
            let mut year = 0u64;
            for len in 1..=4 {
                let Some(ch) = text.get(start + len - 1).filter(|c| c.is_ascii_digit()) else { break };
                year = year * 10 + (*ch as u64 - '0' as u64);
                if len < 2 || text.get(start + len) != Some(&'-') {
                    continue;
                }
                for (me, month) in numeric_piece(text, start + len + 1, 12) {
                    if text.get(me) != Some(&'-') {
                        continue;
                    }
                    for (end, day) in numeric_piece(text, me + 1, 31) {
                        out.push(end, WEIGHT, 0, move || {
                            let mut spelled = String::new();
                            cardinal::spell(year, &mut spelled);
                            format!(
                                "date {{ year: \"{spelled}\" month: \"{}\" day: \"{}\" preserve_order: true }}",
                                MONTHS[month], DAYS_NAMES[day]
                            )
                        });
                    }
                }
            }
        }

        // Try YMD at after_year
        month_endpoints(text, after_year, |m_str, after_m| {
            if text.get(after_m).is_some_and(|c| crate::text::is_nemo_space(*c)) {
                let day_start = after_m + 1;
                for day_end in day_start + 1..=text.len() {
                    let c = text[day_end - 1];
                    if !is_hu_alpha(c) && !c.is_ascii_digit() && c != '.' && c != '-' {
                        break;
                    }
                    let Some((day, end)) = match_day(&text[..day_end], day_start) else { continue };
                    if end != day_end {
                        continue;
                    }
                    out.push(end, WEIGHT, 0, move || {
                        let mut tag = String::from("date { ");
                        if has_year {
                            let mut yr_sp = String::new();
                            cardinal::spell(year_val, &mut yr_sp);
                            tag.push_str(&format!("year: \"{yr_sp}\" "));
                        }
                        tag.push_str(&format!("month: \"{m_str}\" day: \"{day}\" preserve_order: true }}"));
                        tag
                    });
                }
            }
            if has_year {
                out.push(after_m, WEIGHT, 1, move || {
                    let mut yr_sp = String::new();
                    cardinal::spell(year_val, &mut yr_sp);
                    format!("date {{ year: \"{yr_sp}\" month: \"{m_str}\" preserve_order: true }}")
                });
            }
        });

        for (de, day) in numeric_piece(text, start, 31) {
            let Some(sep @ ('.' | '/' | '-')) = text.get(de).copied() else { continue };
            for (me, month) in numeric_piece(text, de + 1, 12) {
                if text.get(me) != Some(&sep) || text.get(me + 1) == Some(&'0') {
                    continue;
                }
                let mut year = 0u64;
                for len in 1..=4 {
                    let Some(ch) = text.get(me + len).filter(|c| c.is_ascii_digit()) else { break };
                    year = year * 10 + (*ch as u64 - '0' as u64);
                    if len < 2 {
                        continue;
                    }
                    out.push(me + len + 1, WEIGHT, 2, move || {
                        let mut spelled = String::new();
                        cardinal::spell(year, &mut spelled);
                        format!(
                            "date {{ day: \"{}\" month: \"{}\" year: \"{spelled}\" }}",
                            DAYS_NAMES[day], MONTHS[month]
                        )
                    });
                }
            }
        }
    }
}

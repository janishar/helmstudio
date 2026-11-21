//! Keyword-gated address transducer from `measure.py`.
use super::{numeric, serial};
use crate::lattice::Candidate;
use crate::text::{Cost, cost, is_nemo_space};
use std::collections::BTreeMap;
const CONTEXT: &str = include_str!("../../../data/gu/address/context.tsv");
const LETTERS: &str = include_str!("../../../data/gu/address/letters.tsv");
const SYMBOLS: &str = include_str!("../../../data/gu/address/special_characters.tsv");
pub(super) struct KeywordIndex {
    ascii: [Vec<&'static str>; 128],
    gujarati: [Vec<&'static str>; 128],
    other: std::collections::HashMap<char, Vec<&'static str>>,
}
impl KeywordIndex {
    pub(super) fn new() -> Self {
        Self {
            ascii: std::array::from_fn(|_| Vec::new()),
            gujarati: std::array::from_fn(|_| Vec::new()),
            other: Default::default(),
        }
    }
    pub(super) fn insert(&mut self, word: &'static str) {
        let Some(c) = word.chars().next() else { return };
        let n = c as usize;
        if n < 128 {
            self.ascii[n].push(word);
        } else if (0x0a80..=0x0aff).contains(&n) {
            self.gujarati[n - 0x0a80].push(word);
        } else {
            self.other.entry(c).or_default().push(word);
        }
    }
    pub(super) fn get(&self, c: char) -> &[&'static str] {
        let n = c as usize;
        if n < 128 {
            &self.ascii[n]
        } else if (0x0a80..=0x0aff).contains(&n) {
            &self.gujarati[n - 0x0a80]
        } else {
            self.other.get(&c).map(Vec::as_slice).unwrap_or(&[])
        }
    }
}
// `allowed` only admits keywords at the beginning or after a boundary, with
// at most five preceding words. Indexing those positions avoids scanning the
// entire remaining sentence for every lattice start.
pub(crate) fn keyword_bounds(s: &str) -> Option<(usize, usize)> {
    static KEYWORDS: std::sync::LazyLock<KeywordIndex> = std::sync::LazyLock::new(|| {
        let mut index = KeywordIndex::new();
        for word in CONTEXT.lines().map(|line| line.trim_end_matches('\r').split('\t').next().unwrap_or("")) {
            index.insert(word);
        }
        index
    });
    let mut words = 0;
    let mut in_word = false;
    let mut earliest: Option<usize> = None;
    let mut latest: Option<usize> = None;
    for (i, c) in s.char_indices() {
        if boundary(c) {
            in_word = false;
        } else if !in_word {
            if words > 5 {
                break;
            }
            for keyword in KEYWORDS.get(c) {
                if s[i..].starts_with(*keyword) {
                    let end = i + keyword.len();
                    earliest = Some(earliest.map_or(end, |old| old.min(end)));
                    let rest = &s[end..];
                    let mut last = end;
                    if rest.chars().next().is_some_and(boundary) {
                        let mut after_words = 0;
                        let mut after_word = false;
                        last = s.len();
                        for (j, ch) in rest.char_indices() {
                            if boundary(ch) {
                                after_word = false;
                            } else if !after_word {
                                after_words += 1;
                                if after_words > 5 {
                                    last = end + j;
                                    break;
                                }
                                after_word = true;
                            }
                        }
                    }
                    latest = Some(latest.map_or(last, |old| old.max(last)));
                }
            }
            words += 1;
            in_word = true;
        }
    }
    earliest.zip(latest)
}
pub(crate) fn has_keyword(s: &str) -> bool {
    keyword_bounds(s).is_some()
}
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|s| s.trim_end_matches('\r').split_once('\t'))
}
fn map_char(data: &'static str, c: char) -> Option<&'static str> {
    use std::collections::HashMap;
    use std::sync::LazyLock;
    fn index(data: &'static str) -> HashMap<char, &'static str> {
        let mut out = HashMap::new();
        for (key, value) in rows(data) {
            let mut chars = key.chars();
            if let (Some(c), None) = (chars.next(), chars.next()) {
                out.entry(c).or_insert(value);
            }
        }
        out
    }
    static LETTER_INDEX: LazyLock<HashMap<char, &'static str>> = LazyLock::new(|| index(LETTERS));
    static SYMBOL_INDEX: LazyLock<HashMap<char, &'static str>> = LazyLock::new(|| index(SYMBOLS));
    if std::ptr::eq(data, LETTERS) { LETTER_INDEX.get(&c).copied() } else { SYMBOL_INDEX.get(&c).copied() }
}
fn boundary(c: char) -> bool {
    is_nemo_space(c) || matches!(c, ',' | '।' | '.')
}
fn window(s: &str) -> bool {
    if s.is_empty() {
        return true;
    }
    if s.chars().next().is_some_and(boundary) {
        return false;
    }
    let mut count = 0;
    let mut in_word = false;
    for c in s.chars() {
        if boundary(c) {
            in_word = false;
        } else if !in_word {
            count += 1;
            in_word = true;
        }
    }
    count <= 5
}
fn allowed(s: &str) -> bool {
    for row in CONTEXT.lines() {
        let keyword = row.trim_end_matches('\r').split('\t').next().unwrap_or("");
        if keyword.is_empty() {
            continue;
        }
        for (pos, _) in s.match_indices(keyword) {
            let before = &s[..pos];
            let after = &s[pos + keyword.len()..];
            if before.is_empty() && after.chars().next().is_some_and(boundary) {
                let after = after.trim_start_matches(boundary);
                if window(after) {
                    return true;
                }
            }
            if !before.is_empty() && before.chars().last().is_some_and(boundary) {
                let before = before.trim_end_matches(boundary);
                if !window(before) {
                    continue;
                }
                if after.is_empty() {
                    return true;
                }
                if after.chars().next().is_some_and(boundary) && window(after.trim_start_matches(boundary)) {
                    return true;
                }
            }
        }
    }
    false
}
fn add(states: &mut [BTreeMap<String, Cost>], end: usize, prefix: &str, fragment: &str, weight: Cost, space: bool) {
    let mut output = String::with_capacity(prefix.len() + fragment.len() + 1);
    output.push_str(prefix);
    if space {
        output.push(' ');
    }
    output.push_str(fragment);
    let entry = states[end].entry(output).or_insert(weight);
    if weight < *entry {
        *entry = weight;
    }
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !allowed(s) {
        return;
    }
    let chars: Vec<char> = s.chars().collect();
    let mut states: Vec<BTreeMap<String, Cost>> = (0..=chars.len()).map(|_| BTreeMap::new()).collect();
    states[0].insert(String::new(), 0);
    for i in 0..chars.len() {
        if states[i].is_empty() {
            continue;
        }
        // A transition depends only on the input position, not on the output
        // path that reached it. Parse it once rather than rerunning the serial,
        // ordinal and number graphs for every accumulated address reading.
        let mut transitions: Vec<(usize, String, Cost, bool)> = Vec::new();
        let c = chars[i];
        if c == ' ' {
            transitions.push((i + 1, " ".to_owned(), 0, false));
        }
        let mut j = i;
        while j < chars.len() && is_nemo_space(chars[j]) {
            j += 1;
        }
        if j < chars.len() && chars[j] == ',' {
            transitions.push((j + 1, ",".to_owned(), 0, false));
        }
        if matches!(c, '-' | '/') {
            if let Some(v) = map_char(SYMBOLS, c) {
                transitions.push((i + 1, v.to_owned(), 0, true));
            }
        }
        if let Some(v) = map_char(LETTERS, c) {
            transitions.push((i + 1, v.to_owned(), 0, true));
        }
        let mut part = String::new();
        let mut all_upper = true;
        let mut all_digits = true;
        let mut has_digit = false;
        let mut all_other = true;
        for j in i + 1..=chars.len() {
            let ch = chars[j - 1];
            part.push(ch);
            let is_digit = super::numbers::digit(ch).is_some();
            all_upper &= ch.is_ascii_uppercase();
            all_digits &= is_digit;
            has_digit |= is_digit;
            all_other &=
                !is_nemo_space(ch) && !is_digit && !matches!(ch, '-' | '/' | ',') && map_char(LETTERS, ch).is_none();
            if all_upper && j - i >= 2 {
                transitions.push((j, part.clone(), 0, true));
            }
            if all_digits {
                for (v, w) in super::lazy::with_build(true, || serial::code_number(&part)) {
                    transitions.push((j, v, w + cost(0.5), true));
                }
            }
            if has_digit {
                for (v, w) in super::lazy::with_build(true, || serial::mixed_readings(&part)) {
                    transitions.push((j, v, w, true));
                }
                if super::numbers::digit(chars[i]).is_some() {
                    let mut ordinals = Vec::new();
                    super::lazy::with_build(true, || numeric::classify(&part, end, &mut ordinals));
                    for ordinal in ordinals {
                        if let Some(v) =
                            ordinal.tag.strip_prefix("ordinal { integer: \"").and_then(|x| x.strip_suffix("\" }"))
                        {
                            transitions.push((j, v.to_owned(), ordinal.cost - cost(1.1) + cost(-0.0001), true));
                        }
                    }
                }
            }
            if all_other {
                transitions.push((j, part.clone(), cost(0.1), true));
            }
        }
        let current = std::mem::take(&mut states[i]);
        for (output, weight) in current {
            for (end, fragment, extra, space) in &transitions {
                add(&mut states, *end, &output, fragment, weight + *extra, *space);
            }
        }
    }
    for (spoken, weight) in std::mem::take(&mut states[chars.len()]) {
        let spoken = spoken.trim_start_matches(' ');
        if spoken.is_empty() {
            continue;
        }
        out.push(Candidate {
            end,
            cost: cost(1.1) + weight,
            rank: 6,
            tag: super::lazy::tag(|| {
                format!("measure {{ units: \"address\" cardinal {{ integer: \"{spoken}\" }} preserve_order: true }}")
            }),
        });
    }
}

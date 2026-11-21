//! Hindi serial codes and power expressions.
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
fn lines(data: &'static str) -> impl Iterator<Item = &'static str> {
    data.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty())
}
static LETTERS: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(include_str!("../../../data/hi/address/letters.tsv")).collect());
static SYMBOLS: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(include_str!("../../../data/hi/serial/special_symbols.tsv")).collect());
static POWERS: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(include_str!("../../../data/hi/serial/power_special.tsv")).collect());
static DEVANAGARI: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| lines(include_str!("../../../data/hi/serial/chars.tsv")).collect());
static DEV_TRIE: LazyLock<super::trie::Trie> =
    LazyLock::new(|| super::trie::Trie::from_keys(DEVANAGARI.iter().copied()));
static DEV_SINGLE: LazyLock<HashSet<char>> = LazyLock::new(|| {
    DEVANAGARI
        .iter()
        .filter_map(|key| {
            let mut chars = key.chars();
            let first = chars.next()?;
            chars.next().is_none().then_some(first)
        })
        .collect()
});
static DEV_ENDINGS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut endings: HashMap<char, Vec<&'static str>> = HashMap::new();
    for &key in DEVANAGARI.iter() {
        if let Some(last) = key.chars().next_back() {
            endings.entry(last).or_default().push(key);
        }
    }
    endings
});
static EXCLUDED_SUFFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    lines(include_str!("../../../data/hi/ordinal/suffixes.tsv"))
        .chain(rows(include_str!("../../../data/hi/ordinal/suffixes_map.tsv")).map(|(k, _)| k))
        .chain(rows(include_str!("../../../data/hi/measure/unit.tsv")).map(|(k, _)| k))
        .collect()
});
fn is_dev(c: char) -> bool {
    DEV_SINGLE.contains(&c)
}
fn is_alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || is_dev(c)
}
fn devanagari_word(s: &str) -> bool {
    let mut positions = vec![None; s.len() + 1];
    positions[0] = Some(0usize);
    for (i, _) in s.char_indices() {
        let Some(n) = positions[i] else { continue };
        DEV_TRIE.matching_prefixes(&s[i..], |len| {
            let next = i + len;
            positions[next] = Some(positions[next].unwrap_or(0).max(n + 1));
        });
    }
    positions[s.len()].is_some_and(|n| n >= 2)
}
fn segment(s: &str) -> Vec<(String, Cost)> {
    if s.chars().all(super::numbers::is_digit) {
        return super::numbers::code_num(s);
    }
    if !s.is_empty() && s.chars().all(|c| c.is_ascii_alphabetic()) {
        if !s.chars().all(|c| {
            let mut buf = [0u8; 4];
            LETTERS.contains_key(c.to_ascii_lowercase().encode_utf8(&mut buf) as &str)
        }) {
            return Vec::new();
        }
        return vec![(
            super::lazy::tag(|| {
                let mut output = String::new();
                for c in s.chars() {
                    let mut buf = [0u8; 4];
                    let key: &str = c.to_ascii_lowercase().encode_utf8(&mut buf);
                    if !output.is_empty() {
                        output.push(' ');
                    }
                    output.push_str(LETTERS[key]);
                }
                output
            }),
            0,
        )];
    }
    if devanagari_word(s) {
        return vec![(super::lazy::tag(|| s.to_owned()), 0)];
    }
    SYMBOLS.get(s).map(|v| vec![(super::lazy::tag(|| (*v).to_owned()), 0)]).unwrap_or_default()
}
fn excluded(s: &str) -> bool {
    if s.chars().all(|c| c.is_ascii_alphabetic() || matches!(c, '-' | '/'))
        && s.chars().any(|c| c.is_ascii_alphabetic())
    {
        return true;
    }
    if let Some((a, b)) = s.split_once(['x', 'X']) {
        if !a.is_empty()
            && !b.is_empty()
            && a.chars().all(super::numbers::is_digit)
            && b.chars().all(super::numbers::is_digit)
        {
            return true;
        }
    }
    for (index, c) in s.char_indices() {
        if index > 0 && EXCLUDED_SUFFIXES.contains(&s[index..]) {
            return true;
        }
        if !super::numbers::is_digit(c) {
            break;
        }
    }
    let mut parts = s.split('/');
    let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) else {
        return false;
    };
    [a, b, c].iter().all(|p| p.chars().all(super::numbers::is_digit))
        && (1..=2).contains(&a.chars().count())
        && (1..=2).contains(&b.chars().count())
        && c.chars().count() == 4
}
fn emit(out: &mut Vec<Candidate>, end: usize, weight: Cost, value: &str) {
    out.push(Candidate {
        end,
        cost: cost(1.11) + weight,
        rank: 11,
        tag: super::lazy::tag(|| format!("name: \"{}\"", value.replace(' ', "\u{a0}"))),
    });
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !s.contains(['-', '/', '^']) && !s.chars().any(super::numbers::is_digit) {
        return;
    }
    if s.chars().all(super::numbers::is_digit) {
        return;
    }
    if s.chars().count() < 2 || s.chars().any(crate::text::is_nemo_space) || excluded(s) {
        return;
    }
    if let Some((base, exponent)) = s.split_once('^') {
        if base.len() == 1 && base.as_bytes()[0].is_ascii_alphabetic() {
            let key = base.to_ascii_lowercase();
            if let Some(word) = LETTERS.get(key.as_str()) {
                if matches!(exponent, "2" | "२" | "3" | "३") {
                    let suffix =
                        if matches!(exponent, "2" | "२") { " का वर्ग" } else { " का घन" };
                    emit(out, end, 0, &super::lazy::tag(|| format!("{word}{suffix}")));
                }
                for (number, weight) in super::numbers::code_num(exponent) {
                    emit(out, end, cost(1.0) + weight, &super::lazy::tag(|| format!("{word} की घात {number}")));
                }
            }
        }
        for (number, weight) in super::numbers::code_num(base) {
            if let Some(suffix) = POWERS.get(&format!("^{exponent}").as_str()) {
                emit(out, end, weight + cost(-1.0), &super::lazy::tag(|| format!("{number}{suffix}")));
            }
            for (exp, c) in super::numbers::code_num(exponent) {
                emit(out, end, weight + c + cost(1.0), &super::lazy::tag(|| format!("{number} टु द पावर {exp}")));
            }
        }
    }
    for (value, weight) in core(s) {
        emit(out, end, weight, &value);
    }
}

pub(crate) fn mixed(s: &str) -> Vec<(String, Cost)> {
    let mut states = vec![0u8; s.len() + 1];
    states[0] = 1;
    for (i, c) in s.char_indices() {
        let current = states[i];
        if current == 0 {
            continue;
        }
        for bits in 0..4 {
            if current & (1 << bits) == 0 {
                continue;
            }
            let end = i + c.len_utf8();
            if super::numbers::is_digit(c) {
                states[end] |= 1 << (bits | 1);
            }
            if c.is_ascii_alphabetic() {
                states[end] |= 1 << (bits | 2);
            }
            if matches!(c, '-' | '/') {
                states[end] |= 1 << bits;
            }
            DEV_TRIE.matching_prefixes(&s[i..], |len| {
                states[i + len] |= 1 << (bits | 2);
            });
        }
    }
    if states[s.len()] & (1 << 3) == 0 {
        return Vec::new();
    }
    core(s)
}

fn core(s: &str) -> Vec<(String, Cost)> {
    let mut pieces: Vec<(&str, &str)> = Vec::new();
    let mut start = 0;
    let mut previous = None;
    let mut delimiter = "";
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '/') {
            if i == start {
                return Vec::new();
            }
            pieces.push((delimiter, &s[start..i]));
            delimiter = &s[i..i + c.len_utf8()];
            start = i + c.len_utf8();
            previous = None;
        } else if let Some(p) = previous {
            let left_alpha = is_alpha(p)
                || DEV_ENDINGS.get(&p).is_some_and(|keys| keys.iter().any(|key| s[start..i].ends_with(*key)));
            let mut right_alpha = is_alpha(c);
            if !right_alpha {
                DEV_TRIE.matching_prefixes(&s[i..], |_| right_alpha = true);
            }
            if (left_alpha && super::numbers::is_digit(c)) || (super::numbers::is_digit(p) && right_alpha) {
                pieces.push((delimiter, &s[start..i]));
                delimiter = " ";
                start = i;
            }
            previous = Some(c);
        } else {
            previous = Some(c);
        }
    }
    if start == s.len() {
        return Vec::new();
    }
    pieces.push((delimiter, &s[start..]));
    if pieces.len() < 2 {
        return Vec::new();
    }
    let mut paths = vec![(String::new(), 0)];
    for (separator, piece) in pieces {
        let values = segment(piece);
        let mut next = Vec::new();
        for (prefix, old_weight) in &paths {
            for (value, weight) in &values {
                next.push((super::lazy::tag(|| format!("{prefix}{separator}{value}")), old_weight + weight));
            }
        }
        paths = next;
    }
    paths
}

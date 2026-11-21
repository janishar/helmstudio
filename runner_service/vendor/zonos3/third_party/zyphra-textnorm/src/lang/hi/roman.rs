//! Hindi Roman numeral classifier.
use crate::lattice::Candidate;
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
static ROMAN: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(include_str!("../../../data/hi/roman/roman_to_spoken.tsv")).collect());
static EXCEPTIONS: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(include_str!("../../../data/hi/roman/roman_ordinal_exceptions.tsv")).collect());
static CHARS: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    include_str!("../../../data/hi/serial/chars.tsv")
        .lines()
        .map(|s| s.trim_end_matches('\r'))
        .filter(|s| !s.is_empty())
        .collect()
});
fn phrase(s: &str) -> bool {
    if s.is_empty() {
        return false;
    }
    for word in s.split([' ', '-']) {
        if word.is_empty() {
            return false;
        }
        let mut at = vec![false; word.len() + 1];
        at[0] = true;
        for (i, _) in word.char_indices() {
            if at[i] {
                for c in CHARS.iter() {
                    if word[i..].starts_with(*c) {
                        at[i + c.len()] = true;
                    }
                }
            }
        }
        if !at[word.len()] {
            return false;
        }
    }
    true
}
fn emit(out: &mut Vec<Candidate>, end: usize, weight: f64, fields: impl FnOnce() -> String) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + weight),
        rank: 12,
        tag: super::lazy::tag(|| format!("roman {{ preserve_order: true {} }}", fields())),
    });
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !s.bytes().any(|b| matches!(b, b'I' | b'V' | b'X' | b'L' | b'C' | b'D' | b'M')) {
        return;
    }
    for (pos, sep) in s.char_indices().filter(|(_, c)| matches!(*c, '-' | ' ')) {
        let left = &s[..pos];
        let right = &s[pos + sep.len_utf8()..];
        if phrase(left) && ROMAN.contains_key(right) {
            emit(out, end, 0.0, || format!("key_cardinal: \"{}\" integer: \"{right}\"", left.replace(' ', "\u{a0}")));
        }
        if ROMAN.contains_key(left) && phrase(right) {
            emit(out, end, 0.0, || format!("integer: \"{left}\" key_cardinal: \"{}\"", right.replace(' ', "\u{a0}")));
        }
    }
    for (pos, _) in s.char_indices().filter(|(_, c)| *c == ' ').chain(std::iter::once((s.len(), ' '))) {
        let fused = &s[..pos];
        let key = if pos == s.len() { None } else { Some(&s[pos + 1..]) };
        if key.is_some_and(|k| !phrase(k)) {
            continue;
        }
        if let Some(&spoken) = EXCEPTIONS.get(fused) {
            if let Some(numeral) = ROMAN.keys().filter(|n| fused.starts_with(**n)).max_by_key(|n| n.len()) {
                emit(out, end, -0.1, || {
                    let key =
                        key.map(|k| format!(" key_cardinal: \"{}\"", k.replace(' ', "\u{a0}"))).unwrap_or_default();
                    format!("integer: \"{numeral}\" default_ordinal: \"{spoken}\"{key}")
                });
            }
            continue;
        }
        for (&numeral, &spoken) in ROMAN.iter() {
            if let Some(suffix) = fused.strip_prefix(numeral) {
                let mapped = include_str!("../../../data/hi/ordinal/suffixes.tsv")
                    .lines()
                    .map(|v| v.trim_end_matches('\r'))
                    .find(|v| *v == suffix)
                    .map(|v| (v, v))
                    .or_else(|| {
                        rows(include_str!("../../../data/hi/ordinal/suffixes_map.tsv")).find(|(k, _)| *k == suffix)
                    });
                if let Some((_, suffix)) = mapped {
                    emit(out, end, 0.0, || {
                        let key =
                            key.map(|k| format!(" key_cardinal: \"{}\"", k.replace(' ', "\u{a0}"))).unwrap_or_default();
                        format!("integer: \"{numeral}\" default_ordinal: \"{spoken}{suffix}\"{key}")
                    });
                }
            }
        }
    }
}

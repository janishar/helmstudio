//! Telugu Whitelist classifier.

use super::lazy;
use crate::lattice::Candidate;
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

const ABBREVIATIONS_DATA: &str = include_str!("../../../data/te/whitelist/abbreviations.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static ABBREVIATIONS: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ABBREVIATIONS_DATA).collect());

fn tag_name(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 16);
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}

static ABBREVIATION_STARTS: LazyLock<HashMap<u8, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<u8, Vec<&'static str>> = HashMap::new();
    for &key in ABBREVIATIONS.keys() {
        if let Some(&first) = key.as_bytes().first() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});

pub fn last_abbreviation_end(input: &str) -> usize {
    input
        .as_bytes()
        .first()
        .and_then(|first| ABBREVIATION_STARTS.get(first))
        .map_or(0, |keys| keys.iter().filter(|key| input.starts_with(**key)).map(|key| key.len()).max().unwrap_or(0))
}

pub fn is_abbreviation(input: &str) -> bool {
    ABBREVIATIONS.contains_key(input)
}

pub fn is_dynamic_start(_input: &str, first: char) -> bool {
    first.is_ascii_uppercase() || matches!(first, '&' | '©' | '®' | '™' | '§' | '¶')
}

fn parse_initialism(s: &str) -> Option<String> {
    let mut out = String::new();
    let mut chars = s.chars().peekable();
    let first = chars.next()?;
    if !first.is_ascii_uppercase() {
        return None;
    }
    out.push(first);
    let mut count = 1;
    while let Some(&c) = chars.peek() {
        if c == '.' {
            chars.next();
            if chars.peek() == Some(&' ') {
                chars.next();
            }
            if let Some(&next_c) = chars.peek() {
                if next_c.is_ascii_uppercase() {
                    chars.next();
                    out.push(next_c);
                    count += 1;
                } else {
                    return None;
                }
            } else {
                // Trailing dot
                break;
            }
        } else {
            return None;
        }
    }
    if count >= 3 && chars.peek().is_none() { Some(out) } else { None }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let weight = cost(1.01);

    // 1. Static abbreviations table
    if let Some(&spoken) = ABBREVIATIONS.get(s) {
        out.push(Candidate { end, cost: weight, rank: 0, tag: lazy::tag(|| tag_name(spoken)) });
    }

    // 2. Initialisms: e.g. U.S.A., U.S.A, A. B. C.
    if s.chars().next().is_some_and(|c| c.is_ascii_uppercase()) {
        if let Some(acronym) = parse_initialism(s) {
            out.push(Candidate { end, cost: weight, rank: 0, tag: lazy::tag(move || format!("name: \"{acronym}\"")) });
        }
    }
}

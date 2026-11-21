//! Bengali Electronic classifier and verbalization helpers.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const DOMAINS_DATA: &str = include_str!("../../../data/bn/electronic/domain.tsv");
const EXTENSIONS_DATA: &str = include_str!("../../../data/bn/electronic/file_extensions.tsv");
const ELEMENTS_DATA: &str = include_str!("../../../data/bn/electronic/elements.tsv");
const PROTOCOLS_DATA: &str = include_str!("../../../data/bn/electronic/protocols.tsv");
const SYMBOLS_DATA: &str = include_str!("../../../data/bn/electronic/symbols.tsv");
const LETTERS_DATA: &str = include_str!("../../../data/bn/address/letters.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static DOMAINS: LazyLock<std::collections::HashSet<&'static str>> =
    LazyLock::new(|| DOMAINS_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static EXTENSIONS: LazyLock<std::collections::HashSet<&'static str>> =
    LazyLock::new(|| EXTENSIONS_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static ELEMENTS: LazyLock<std::collections::HashSet<&'static str>> =
    LazyLock::new(|| ELEMENTS_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static PROTOCOLS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(PROTOCOLS_DATA).collect());
static SYMBOLS: LazyLock<std::collections::HashMap<char, &'static str>> = LazyLock::new(|| {
    let mut m = std::collections::HashMap::new();
    for (k, v) in rows(SYMBOLS_DATA) {
        let clean_k = k.strip_prefix('\\').unwrap_or(k);
        if let Some(c) = clean_k.chars().next() {
            m.insert(c, v);
        }
    }
    m
});

static LATIN_LETTERS: LazyLock<std::collections::HashMap<char, &'static str>> = LazyLock::new(|| {
    let mut m = std::collections::HashMap::new();
    for (k, v) in rows(LETTERS_DATA) {
        if let Some(c) = k.chars().next() {
            m.insert(c.to_ascii_lowercase(), v);
            m.insert(c.to_ascii_uppercase(), v);
        }
    }
    m
});

pub fn is_subscript(c: char) -> bool {
    ('₀'..='₉').contains(&c)
}

pub fn subscript_value(c: char) -> Option<&'static str> {
    match c {
        '₀' => Some("শূন্য"),
        '₁' => Some("এক"),
        '₂' => Some("দুই"),
        '₃' => Some("তিন"),
        '₄' => Some("চার"),
        '₅' => Some("পাঁচ"),
        '₆' => Some("ছয়"),
        '₇' => Some("সাত"),
        '₈' => Some("আট"),
        '₉' => Some("নয়"),
        _ => None,
    }
}

pub fn symbol_word(c: char) -> Option<&'static str> {
    SYMBOLS.get(&c).copied()
}

pub fn letter_word(c: char) -> Option<&'static str> {
    LATIN_LETTERS.get(&c).copied()
}

fn alphanumeric(c: char) -> bool {
    c.is_ascii_alphabetic() || numbers::is_digit(c) || is_subscript(c)
}

fn ipv4(s: &str) -> bool {
    let mut count = 0;
    for part in s.split('.') {
        if part.is_empty() || part.len() > 3 || !part.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
        count += 1;
    }
    count == 4
}

fn domain(s: &str) -> bool {
    let s = s.strip_suffix('/').unwrap_or(s);
    for (index, _) in s.match_indices('.') {
        let (prefix, rest) = s.split_at(index);
        let suffix = &rest[1..];
        if prefix
            .split('.')
            .all(|segment| !segment.is_empty() && segment.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-'))
            && (DOMAINS.contains(suffix)
                || suffix
                    .match_indices('.')
                    .any(|(i, _)| DOMAINS.contains(&suffix[..i]) && DOMAINS.contains(&suffix[i + 1..])))
        {
            return true;
        }
    }
    false
}

fn file_with_extension(s: &str) -> bool {
    s.split_once('.').is_some_and(|(stem, ext)| {
        !stem.is_empty()
            && stem.bytes().all(|c| c.is_ascii_alphanumeric() || matches!(c, b'-' | b'_'))
            && EXTENSIONS.contains(ext)
    })
}

fn emit(out: &mut Vec<Candidate>, end: usize, weight: f64, tag: impl FnOnce() -> String) {
    out.push(Candidate { end, cost: cost(1.1) + cost(weight), rank: 10, tag: super::lazy::tag(tag) });
}

fn chemical_sequence(s: &str, has_element: bool) -> bool {
    if s.is_empty() {
        return has_element;
    }
    let c = s.chars().next().unwrap();
    if matches!(c, '(' | ')' | '+' | '-' | '–') || (has_element && (c.is_ascii_digit() || is_subscript(c))) {
        if chemical_sequence(&s[c.len_utf8()..], has_element) {
            return true;
        }
    }
    ELEMENTS.iter().any(|element| s.strip_prefix(*element).is_some_and(|rest| chemical_sequence(rest, true)))
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.chars().any(is_subscript) && chemical_sequence(s, false) && !(s.ends_with(')') && !s.contains('(')) {
        emit(out, end, 1.2, || format!("electronic {{ fragment_id: \"{s}\" }}"));
    }

    if let Some((user, domain)) = s.split_once('@') {
        if !user.is_empty()
            && user.chars().all(|c| c.is_ascii_alphanumeric() || "._-".contains(c))
            && !domain.is_empty()
            && domain.chars().all(|c| c.is_ascii_alphanumeric() || ".-".contains(c))
        {
            emit(out, end, 1.0, || format!("electronic {{ username: \"{user}\" domain: \"{domain}\" }}"));
        }
    }

    for (prefix, protocol, weight) in [
        ("https://www.", "httpswww", 2.0),
        ("http://www.", "httpwww", 2.0),
        ("https://", "https", 2.01),
        ("http://", "http", 2.01),
        ("www.", "www", 2.02),
    ] {
        if let Some(rest) = s.strip_prefix(prefix) {
            if !rest.is_empty() && rest.chars().all(|c| alphanumeric(c) || ".-_/+#?&=%:".contains(c)) {
                emit(out, end, weight, || format!("electronic {{ protocol: \"{protocol}\" domain: \"{rest}\" }}"));
            }
        }
    }

    let bytes = s.as_bytes();
    if bytes.len() > 3
        && bytes[0].is_ascii_alphabetic()
        && bytes[1] == b':'
        && bytes[2] == b'\\'
        && s[3..].chars().all(|c| alphanumeric(c) || ".-_\\(): ".contains(c))
    {
        emit(out, end, 1.0, || format!("electronic {{ path: \"{s}\" }}"));
    }
    if let Some(rest) = s.strip_prefix('\\') {
        if !rest.is_empty() && rest.chars().all(|c| alphanumeric(c) || ".-_$\\ ".contains(c)) {
            emit(out, end, 1.0, || format!("electronic {{ path: \"{s}\" }}"));
        }
    }
    if let Some((before, after)) = s.split_once('/') {
        if (!before.is_empty() || !after.is_empty())
            && before.chars().all(|c| alphanumeric(c) || ".-_$".contains(c))
            && after.chars().all(|c| alphanumeric(c) || ".-_/$".contains(c))
        {
            emit(out, end, 1.0, || format!("electronic {{ path: \"{s}\" }}"));
        }
    }
    if ipv4(s) {
        emit(out, end, 1.0, || format!("electronic {{ domain: \"{s}\" }}"));
    }
    if domain(s) || file_with_extension(s) {
        emit(out, end, 1.1, || format!("electronic {{ domain: \"{s}\" }}"));
    }
}

pub fn protocol_word(s: &str) -> Option<&'static str> {
    PROTOCOLS.get(s).copied()
}

pub fn verbalize_content(s: &str, chemical: bool) -> Option<String> {
    let mut out = String::new();
    let mut in_alpha = false;
    for c in s.chars() {
        if !chemical && c.is_ascii_alphabetic() {
            if !in_alpha && !out.is_empty() {
                out.push(' ');
            }
            out.push(c);
            in_alpha = true;
            continue;
        }
        in_alpha = false;
        let word = if chemical && c.is_ascii_alphabetic() {
            letter_word(c)
        } else {
            numbers::digit(c).or_else(|| subscript_value(c)).or_else(|| symbol_word(c))
        }?;
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(word);
    }
    if chemical && out.is_empty() { None } else { Some(out) }
}

//! Telugu electronic classifiers, following `te/taggers/electronic.py`.

use super::{lazy, numbers};
use crate::lattice::Candidate;
use crate::text::cost;
use crate::tsv;

tsv!(TLD, "te/electronic/domain.tsv");
tsv!(FILE_EXT, "te/electronic/file_extensions.tsv");
tsv!(ELEMENTS, "te/electronic/elements.tsv");
const SYMBOLS: &str = include_str!("../../../data/te/electronic/symbol_classes.tsv");

pub fn is_subscript(c: char) -> bool {
    ('\u{2080}'..='\u{2089}').contains(&c)
}

fn symbol(c: char, class: &str) -> bool {
    let mut buf = [0; 4];
    let needle: &str = c.encode_utf8(&mut buf);
    SYMBOLS
        .lines()
        .filter_map(|line| line.split_once('\t'))
        .any(|(key, classes)| key == needle && classes.split(',').any(|s| s == class))
}

fn alphanumeric(c: char) -> bool {
    c.is_ascii_alphanumeric() || numbers::is_digit(c) || is_subscript(c)
}

fn push(out: &mut Vec<Candidate>, end: usize, weight: f64, tag: impl FnOnce() -> String) {
    out.push(Candidate { end, cost: cost(1.1) + cost(weight), rank: 10, tag: lazy::tag(tag) });
}

fn chemical(s: &str) -> bool {
    if !s.chars().any(is_subscript) || (!s.contains('(') && s.ends_with(')')) {
        return false;
    }
    // Reachable positions after the mandatory first element block.
    let mut reachable = vec![false; s.len() + 1];
    let mut prefix = 0;
    loop {
        for row in ELEMENTS.rows() {
            if s[prefix..].starts_with(row.key.as_str()) {
                reachable[prefix + row.key.len()] = true;
            }
        }
        let Some(c) = s[prefix..].chars().next() else { break };
        if !symbol(c, "chem") {
            break;
        }
        prefix += c.len_utf8();
    }
    for i in 0..s.len() {
        if !reachable[i] || !s.is_char_boundary(i) {
            continue;
        }
        let c = s[i..].chars().next().unwrap();
        if c.is_ascii_digit() || is_subscript(c) || symbol(c, "chem") {
            reachable[i + c.len_utf8()] = true;
        }
        for row in ELEMENTS.rows() {
            if s[i..].starts_with(row.key.as_str()) {
                reachable[i + row.key.len()] = true;
            }
        }
    }
    reachable[s.len()]
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    for (prefix, proto, weight) in [
        ("https://www.", "httpswww", 1.0),
        ("http://www.", "httpwww", 1.0),
        ("https://", "https", 1.01),
        ("http://", "http", 1.01),
        ("www.", "www", 1.02),
    ] {
        if let Some(rest) = s.strip_prefix(prefix) {
            if !rest.is_empty() && rest.chars().all(|c| alphanumeric(c) || symbol(c, "url")) {
                push(out, end, 1.0 + weight, || format!("electronic {{ protocol: \"{proto}\" domain: \"{rest}\" }}"));
            }
        }
    }
    if let Some((user, domain)) = s.split_once('@') {
        if !user.is_empty()
            && !domain.is_empty()
            && user.chars().all(|c| c.is_ascii_alphanumeric() || symbol(c, "email"))
            && domain.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
        {
            push(out, end, 1.0, || format!("electronic {{ username: \"{user}\" domain: \"{domain}\" }}"));
        }
    }
    if s.len() >= 3 && s.as_bytes()[0].is_ascii_alphabetic() && s[1..].starts_with(":\\") {
        let rest = &s[3..];
        if !rest.is_empty() && rest.chars().all(|c| alphanumeric(c) || symbol(c, "windows") || c == ' ') {
            push(out, end, 1.0, || format!("electronic {{ path: \"{s}\" }}"));
        }
    }
    if let Some(rest) = s.strip_prefix('\\') {
        if !rest.is_empty()
            && rest.chars().all(|c| alphanumeric(c) || (c != '/' && symbol(c, "unix")) || matches!(c, '\\' | ' '))
        {
            push(out, end, 1.0, || format!("electronic {{ path: \"{s}\" }}"));
        }
    }
    let unix_char = |c| alphanumeric(c) || symbol(c, "unix");
    let abs = s.strip_prefix('/').is_some_and(|r| !r.is_empty() && r.chars().all(unix_char));
    let rel = s.split_once('/').is_some_and(|(first, rest)| {
        !first.is_empty() && first.chars().all(|c| c != '/' && unix_char(c)) && rest.chars().all(unix_char)
    });
    if abs || rel {
        push(out, end, 1.0, || format!("electronic {{ path: \"{s}\" }}"));
    }
    let mut octets = s.split('.');
    if (0..4)
        .all(|_| octets.next().is_some_and(|p| (1..=3).contains(&p.len()) && p.bytes().all(|b| b.is_ascii_digit())))
        && octets.next().is_none()
    {
        push(out, end, 1.0, || format!("electronic {{ domain: \"{s}\" }}"));
    }
    let domain = s.strip_suffix('/').unwrap_or(s);
    if let Some((left, last)) = domain.rsplit_once('.') {
        let normal = TLD.contains_key(last)
            && left.split('.').all(|p| !p.is_empty() && p.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-'));
        let two_tlds = TLD.contains_key(last)
            && left.rsplit_once('.').is_some_and(|(stem, penult)| {
                TLD.contains_key(penult)
                    && stem
                        .split('.')
                        .all(|p| !p.is_empty() && p.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-'))
            });
        if normal || two_tlds {
            push(out, end, 1.1, || format!("electronic {{ domain: \"{s}\" }}"));
        }
    }
    if let Some((stem, ext)) = s.rsplit_once('.') {
        if !stem.is_empty()
            && stem.bytes().all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-'))
            && FILE_EXT.contains_key(ext)
        {
            push(out, end, 1.1, || format!("electronic {{ domain: \"{s}\" }}"));
        }
    }
    if chemical(s) {
        push(out, end, 1.2, || format!("electronic {{ fragment_id: \"{s}\" }}"));
    }
}

//! Urdu electronic classifier.

use super::Sink;
use crate::text::cost;
use std::collections::HashSet;
use std::sync::LazyLock;

const DOMAINS: &str = include_str!("../../../../data/ur/electronic/domain.tsv");
const EXTENSIONS: &str = include_str!("../../../../data/ur/electronic/file_extensions.tsv");
const ELEMENTS: &str = include_str!("../../../../data/ur/electronic/elements.tsv");
const SYMBOLS: &str = include_str!("../../../../data/ur/electronic/symbol_classes.tsv");

static INPUT_SYMBOLS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    SYMBOLS
        .lines()
        .filter_map(|line| line.split_once('\t'))
        .filter_map(|(key, _)| {
            let mut chars = key.chars();
            let c = chars.next()?;
            chars.next().is_none().then_some(c)
        })
        .collect()
});

/// Every electronic path is ASCII alphanumeric, a source symbol, or a
/// subscript; literal space and '@' are also consumed by specific branches.
pub(crate) fn continuation(c: char) -> bool {
    c.is_ascii_alphanumeric() || subscript(c) || matches!(c, ' ' | '@' | '\\') || INPUT_SYMBOLS.contains(&c)
}

pub(crate) fn marker(c: char) -> bool {
    matches!(c, '.' | '/' | '\\' | '@') || subscript(c)
}

fn member(table: &str, value: &str) -> bool {
    table.lines().any(|line| line.trim_end_matches('\r').split('\t').next() == Some(value))
}

fn symbol(c: char, class: &str) -> bool {
    SYMBOLS.lines().any(|line| {
        line.split_once('\t').is_some_and(|(s, classes)| {
            s.chars().next() == Some(c)
                && s.chars().count() == 1
                && classes.trim_end_matches('\r').split(',').any(|x| x == class)
        })
    })
}

fn subscript(c: char) -> bool {
    ('\u{2080}'..='\u{2089}').contains(&c)
}
fn alpha_num(c: char) -> bool {
    c.is_ascii_alphanumeric() || subscript(c)
}
fn valid(s: &str, f: impl Fn(char) -> bool) -> bool {
    !s.is_empty() && s.chars().all(f)
}

fn chemical(input: &str) -> bool {
    if !input.chars().any(subscript) || (!input.contains('(') && input.ends_with(')')) {
        return false;
    }
    // The graph is symbols* followed by an element block, followed by blocks,
    // individual numbers and symbols. Dynamic programming handles overlapping elements.
    let mut reachable = vec![(false, false); input.len() + 1];
    reachable[0].0 = true;
    for i in 0..input.len() {
        if !input.is_char_boundary(i) {
            continue;
        }
        let (before, after) = reachable[i];
        if !before && !after {
            continue;
        }
        let c = input[i..].chars().next().unwrap();
        let next = i + c.len_utf8();
        if symbol(c, "chem") {
            if before {
                reachable[next].0 = true;
            }
            if after {
                reachable[next].1 = true;
            }
        }
        if after && (c.is_ascii_digit() || subscript(c)) {
            reachable[next].1 = true;
        }
        for element in ELEMENTS.lines().map(|s| s.trim_end_matches('\r')) {
            if !element.is_empty() && input[i..].starts_with(element) {
                reachable[i + element.len()].1 = true;
            }
        }
    }
    reachable[input.len()].1
}

pub fn classify(input: &str, end: usize, out: &mut Sink) {
    if input.is_empty() {
        return;
    }
    for (prefix, protocol, weight) in [
        ("https://www.", "httpswww", 3.1),
        ("http://www.", "httpwww", 3.1),
        ("https://", "https", 3.11),
        ("http://", "http", 3.11),
        ("www.", "www", 3.12),
    ] {
        if let Some(rest) = input.strip_prefix(prefix) {
            if valid(rest, |c| alpha_num(c) || symbol(c, "url")) {
                out.push(end, cost(weight), 10, || format!("protocol: \"{protocol}\" domain: \"{rest}\""));
            }
        }
    }
    if let Some((user, domain)) = input.split_once('@') {
        if valid(user, |c| c.is_ascii_alphanumeric() || symbol(c, "email"))
            && valid(domain, |c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
        {
            out.push(end, cost(2.1), 10, || format!("username: \"{user}\" domain: \"{domain}\""));
        }
    }
    let bytes = input.as_bytes();
    if bytes.len() >= 4
        && bytes[0].is_ascii_alphabetic()
        && &bytes[1..3] == b":\\"
        && valid(&input[3..], |c| alpha_num(c) || symbol(c, "windows") || c == ' ')
    {
        out.push(end, cost(2.1), 10, || format!("path: \"{input}\""));
    }
    if let Some(rest) = input.strip_prefix('/') {
        if valid(rest, |c| alpha_num(c) || symbol(c, "unix")) {
            out.push(end, cost(2.1), 10, || format!("path: \"{input}\""));
        }
    }
    if let Some((segment, rest)) = input.split_once('/') {
        if valid(segment, |c| alpha_num(c) || (c != '/' && symbol(c, "unix")))
            && rest.chars().all(|c| alpha_num(c) || symbol(c, "unix"))
        {
            out.push(end, cost(2.1), 10, || format!("path: \"{input}\""));
        }
    }
    if let Some(rest) = input.strip_prefix('\\') {
        if valid(rest, |c| alpha_num(c) || (c != '/' && symbol(c, "unix")) || c == '\\' || c == ' ') {
            out.push(end, cost(2.1), 10, || format!("path: \"{input}\""));
        }
    }
    let mut parts = input.split('.');
    let octet = |p: &str| (1..=3).contains(&p.len()) && p.bytes().all(|c| c.is_ascii_digit());
    if matches!((parts.next(), parts.next(), parts.next(), parts.next(), parts.next()),
        (Some(a), Some(b), Some(c), Some(d), None) if [a, b, c, d].iter().all(|p| octet(p)))
    {
        out.push(end, cost(2.1), 10, || format!("domain: \"{input}\""));
    }
    let body = input.strip_suffix('/').unwrap_or(input);
    let segment_ok = |s: &str| valid(s, |c| c.is_ascii_alphanumeric() || c == '-');
    let (domain_ok, double_tld) = if let Some((before, last)) = body.rsplit_once('.') {
        let last_ok = member(DOMAINS, last);
        let domain = last_ok && before.split('.').all(segment_ok);
        let double = before.rsplit_once('.').is_some_and(|(first, penultimate)| {
            last_ok && member(DOMAINS, penultimate) && first.split('.').all(segment_ok)
        });
        (domain, double)
    } else {
        (false, false)
    };
    if domain_ok || double_tld {
        out.push(end, cost(2.2), 10, || format!("domain: \"{input}\""));
    }
    if let Some((stem, ext)) = input.rsplit_once('.') {
        if valid(stem, |c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_')) && member(EXTENSIONS, ext) {
            out.push(end, cost(2.2), 10, || format!("domain: \"{input}\""));
        }
    }
    if chemical(input) {
        out.push(end, cost(2.3), 10, || format!("fragment_id: \"{input}\""));
    }
}

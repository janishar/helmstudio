//! Electronic-address classifier, including URL, path, IP and chemical graphs.
use crate::lattice::Candidate;
use crate::text::cost;
use std::collections::HashSet;
use std::sync::LazyLock;

fn lines(data: &'static str) -> impl Iterator<Item = &'static str> {
    data.lines().map(|s| s.trim_end_matches('\r')).filter(|s| !s.is_empty())
}
static DOMAINS: LazyLock<HashSet<&'static str>> =
    LazyLock::new(|| lines(include_str!("../../../data/hi/electronic/domain.tsv")).collect());
static EXTENSIONS: LazyLock<HashSet<&'static str>> =
    LazyLock::new(|| lines(include_str!("../../../data/hi/electronic/file_extensions.tsv")).collect());
static ELEMENTS: LazyLock<HashSet<&'static str>> =
    LazyLock::new(|| lines(include_str!("../../../data/hi/electronic/elements.tsv")).collect());
static SYMBOLS: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    lines(include_str!("../../../data/hi/electronic/symbol_classes.tsv")).filter_map(|l| l.split_once('\t')).collect()
});
fn symbol(c: char, class: &str) -> bool {
    SYMBOLS.iter().any(|(key, classes)| key.chars().eq(std::iter::once(c)) && classes.split(',').any(|v| v == class))
}
fn subscript(c: char) -> bool {
    ('₀'..='₉').contains(&c)
}
fn alphanumeric(c: char) -> bool {
    c.is_ascii_alphanumeric() || ('०'..='९').contains(&c) || subscript(c)
}
fn emit(out: &mut Vec<Candidate>, end: usize, weight: f64, fields: impl FnOnce() -> String) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + weight),
        rank: 10,
        tag: super::lazy::tag(|| format!("electronic {{ {} }}", fields())),
    });
}
fn date_slash(s: &str) -> bool {
    let mut parts = s.split('/');
    let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) else {
        return false;
    };
    if ![a, b, c].iter().all(|p| !p.is_empty() && p.chars().all(super::numbers::is_digit)) {
        return false;
    }
    let a = a.chars().count();
    let b = b.chars().count();
    let c = c.chars().count();
    ((1..=2).contains(&a) && (1..=2).contains(&b) && c == 4) || (a == 4 && (1..=2).contains(&b) && (1..=2).contains(&c))
}
fn valid_chemical(s: &str) -> bool {
    if !s.chars().any(subscript) || (s.ends_with(')') && !s.contains('(')) {
        return false;
    }
    let mut at = vec![false; s.len() + 1];
    at[0] = true;
    let mut seen_element = vec![false; s.len() + 1];
    for (i, c) in s.char_indices() {
        if !at[i] {
            continue;
        }
        if symbol(c, "chem") || c.is_ascii_digit() || subscript(c) {
            let next = i + c.len_utf8();
            if symbol(c, "chem") || seen_element[i] {
                at[next] = true;
                seen_element[next] |= seen_element[i];
            }
        }
        for n in [1, 2] {
            if let Some(v) = s.get(i..i + n) {
                if ELEMENTS.contains(v) {
                    at[i + n] = true;
                    seen_element[i + n] = true;
                }
            }
        }
    }
    at[s.len()] && seen_element[s.len()]
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some((username, domain)) = s.split_once('@') {
        if !username.is_empty()
            && !domain.is_empty()
            && username.chars().all(|c| c.is_ascii_alphanumeric() || symbol(c, "email"))
            && domain.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
        {
            emit(out, end, 1.0, || format!("username: \"{username}\" domain: \"{domain}\""));
        }
    }
    for (prefix, key, extra) in [
        ("https://www.", "httpswww", 1.0),
        ("http://www.", "httpwww", 1.0),
        ("https://", "https", 1.01),
        ("http://", "http", 1.01),
        ("www.", "www", 1.02),
    ] {
        if let Some(domain) = s.strip_prefix(prefix) {
            if !domain.is_empty() && domain.chars().all(|c| alphanumeric(c) || symbol(c, "url")) {
                emit(out, end, 1.0 + extra, || format!("protocol: \"{key}\" domain: \"{domain}\""));
            }
        }
    }
    if s.len() >= 4 && s.as_bytes()[0].is_ascii_alphabetic() && s.as_bytes()[1..3] == *b":\\" {
        if s[3..].chars().all(|c| alphanumeric(c) || symbol(c, "windows") || c == ' ') {
            emit(out, end, 1.0, || format!("path: \"{s}\""));
        }
    }
    if let Some(rest) = s.strip_prefix('/') {
        if !rest.is_empty() && rest.chars().all(|c| alphanumeric(c) || symbol(c, "unix")) {
            emit(out, end, 1.0, || format!("path: \"{s}\""));
        }
    } else if let Some((first, rest)) = s.split_once('/') {
        if !first.is_empty()
            && first.chars().all(|c| alphanumeric(c) || (c != '/' && symbol(c, "unix")))
            && rest.chars().all(|c| alphanumeric(c) || symbol(c, "unix"))
            && !date_slash(s)
        {
            emit(out, end, 1.0, || format!("path: \"{s}\""));
        }
    }
    if let Some(rest) = s.strip_prefix('\\') {
        if !rest.is_empty()
            && rest.chars().all(|c| alphanumeric(c) || (c != '/' && symbol(c, "unix")) || matches!(c, '\\' | ' '))
        {
            emit(out, end, 1.0, || format!("path: \"{s}\""));
        }
    }
    let mut octets = s.split('.');
    if let (Some(a), Some(b), Some(c), Some(d), None) =
        (octets.next(), octets.next(), octets.next(), octets.next(), octets.next())
    {
        if [a, b, c, d].iter().all(|p| (1..=3).contains(&p.len()) && p.bytes().all(|c| c.is_ascii_digit())) {
            emit(out, end, 1.0, || format!("domain: \"{s}\""));
        }
    }
    let domain = s.strip_suffix('/').unwrap_or(s);
    if let Some((prefix, last)) = domain.rsplit_once('.') {
        if !last.is_empty()
            && DOMAINS.contains(last)
            && !prefix.is_empty()
            && prefix.split('.').all(|p| !p.is_empty() && p.chars().all(|c| c.is_ascii_alphanumeric() || c == '-'))
        {
            emit(out, end, 1.1, || format!("domain: \"{s}\""));
        }
        if let Some((prefix, previous)) = prefix.rsplit_once('.') {
            if !last.is_empty()
                && !previous.is_empty()
                && DOMAINS.contains(last)
                && DOMAINS.contains(previous)
                && !prefix.is_empty()
                && prefix.split('.').all(|p| !p.is_empty() && p.chars().all(|c| c.is_ascii_alphanumeric() || c == '-'))
            {
                emit(out, end, 1.1, || format!("domain: \"{s}\""));
            }
        }
    }
    if let Some((stem, ext)) = s.rsplit_once('.') {
        if !stem.is_empty()
            && stem.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_'))
            && EXTENSIONS.contains(ext)
        {
            emit(out, end, 1.1, || format!("domain: \"{s}\""));
        }
    }
    if valid_chemical(s) {
        emit(out, end, 1.2, || format!("fragment_id: \"{s}\""));
    }
}

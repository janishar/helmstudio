//! URLs and email addresses as source graph character components.
use super::{cardinal, numeric::Reading};
const DOMAINS: &[(&str, &str)] = &[
    (".nl", " punt n l"),
    (".be", " punt b e"),
    (".dev", " punt dev"),
    (".de", " punt d e"),
    (".com", " punt com"),
    (".org", " punt org"),
    (".net", " punt net"),
    (".edu", " punt edu"),
    (".gov", " punt gov"),
    (".ai", " punt a i"),
    (".io", " punt i o"),
    (".eu", " punt e u"),
    (".info", " punt info"),
];
const SYMBOLS: &[(&str, &str)] = &[
    ("@", " apenstaartje "),
    (".", " punt "),
    ("/", " slash "),
    ("://", " dubbele punt slash slash "),
    (":", " dubbele punt "),
    ("?", " vraagteken "),
    ("=", " is "),
    ("&", " en "),
    ("-", " streepje "),
    ("_", " laag streepje "),
    ("~", " tilde "),
    ("%", " procent "),
    ("#", " hekje "),
];

fn component(s: &str, start: usize, buf: &mut String, out: &mut Vec<String>) {
    if start == s.len() {
        out.push(buf.clone());
        return;
    }
    let tail = &s[start..];
    let c = tail.chars().next().unwrap();
    let old = buf.len();
    if c.is_ascii_alphabetic() {
        buf.push(c);
        component(s, start + 1, buf, out);
        buf.truncate(old);
    }
    if let Some(word) = cardinal::digit(c) {
        buf.push_str(word);
        component(s, start + 1, buf, out);
        buf.truncate(old);
    }
    for &(k, v) in SYMBOLS.iter().chain(DOMAINS.iter()) {
        if tail.starts_with(k) {
            buf.push_str(v);
            component(s, start + k.len(), buf, out);
            buf.truncate(old);
        }
    }
}
fn components(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    component(s, 0, &mut String::new(), &mut out);
    out
}
fn ascii_word(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_alphabetic())
}
fn username(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let mut out = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            out.push(c);
        } else if let Some(d) = cardinal::digit(c) {
            out.push_str(d);
        } else {
            out.push_str(match c {
                '.' => " punt ",
                '_' => " laag streepje ",
                '-' => " streepje ",
                _ => return None,
            });
        }
    }
    Some(out)
}
pub(super) fn parse(s: &str) -> Vec<Reading> {
    let mut out = Vec::new();
    if let Some((user, domain)) = s.split_once('@') {
        if let Some(user) = username(user) {
            for &(ending, word) in DOMAINS {
                if let Some(host) = domain.strip_suffix(ending) {
                    if ascii_word(host) {
                        out.push((format!("{user} apenstaartje {host}{word}"), 0.95));
                    }
                }
            }
        }
    }
    for (protocol, spoken) in [
        ("https://", "h t t p s dubbele punt slash slash "),
        ("http://", "h t t p dubbele punt slash slash "),
        ("ftp://", "f t p dubbele punt slash slash "),
        ("www.", "w w w punt "),
    ] {
        if let Some(core) = s.strip_prefix(protocol) {
            if !core.is_empty() {
                for v in components(core) {
                    out.push((format!("{spoken}{v}"), 0.96));
                }
            }
        }
    }
    for &(ending, spoken) in DOMAINS {
        for (i, _) in s.match_indices(ending) {
            if i > 0 {
                let before = &s[..i];
                let after = &s[i + ending.len()..];
                for a in components(before) {
                    for b in components(after) {
                        out.push((format!("{a}{spoken}{b}"), 0.96));
                    }
                }
                if after.is_empty() && ascii_word(before) {
                    out.push((format!("{before}{spoken}"), 0.98));
                }
            }
        }
    }
    out
}

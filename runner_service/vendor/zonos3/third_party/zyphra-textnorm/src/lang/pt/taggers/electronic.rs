//! `pt/taggers/electronic.py`: `url | domain | email | social_tag`, each + ` preserve_order: true`.
//!
//! A `www.`/`http(s)://` span can be read as protocol + domain or as a bare domain (or with a shorter protocol),
//! all at cost 0. The oracle reads the longest protocol (`www.google.com` → `protocol: "www."`, `https://www.x.com`
//! → `protocol: "https://www."`), so the alternatives are tried longest protocol first.

use std::sync::LazyLock;

use crate::lang::pt::graph_utils::SpanTagger;
use crate::text::{Cost, is_alpha, is_digit};

crate::labels!(SYMBOLS, "pt/electronic/symbols.tsv");

static SYMBOL_CHARS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = SYMBOLS.iter().filter_map(|r| r.first()).filter_map(|s| s.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

fn is_symbol(c: char) -> bool {
    SYMBOL_CHARS.binary_search(&c).is_ok()
}

/// `accepted_characters` member (no `.`).
fn accepted(c: char) -> bool {
    is_alpha(c) || is_digit(c) || (c != '.' && is_symbol(c))
}

fn all_chars(c: char) -> bool {
    is_alpha(c) || is_digit(c) || is_symbol(c)
}

/// `accepted_characters + closure("." + accepted_characters, 1)`.
fn is_domain(s: &[char]) -> bool {
    let mut parts = 0;
    for part in s.split(|&c| c == '.') {
        if part.is_empty() || !part.iter().all(|&c| accepted(c)) {
            return false;
        }
        parts += 1;
    }
    parts >= 2
}

const PROTOCOLS: [&str; 6] = ["https://www.", "http://www.", "https://", "http://", "www.", ""];

pub struct Electronic;

impl Electronic {
    /// The fields of the cheapest reading, written into `out` (`electronic { … preserve_order: true }`).
    fn tag(s: &[char]) -> Option<String> {
        let mut out = String::with_capacity(s.len() + 64);
        out.push_str("electronic { ");
        let push = |out: &mut String, key: &str, v: &[char]| {
            out.push_str(key);
            out.push_str(": \"");
            out.extend(v);
            out.push('"');
        };
        let mut found = false;
        for p in PROTOCOLS {
            let Some(n) = crate::text::match_str(s, 0, p) else { continue };
            let d = &s[n..];
            if is_domain(d) {
                if !p.is_empty() {
                    out.push_str("protocol: \"");
                    out.push_str(p);
                    out.push_str("\" ");
                }
                push(&mut out, "domain", d);
                found = true;
                break;
            }
        }
        // email: all_characters+ "@" domain (every "@" position whose sides fit)
        if !found {
            for (at, _) in s.iter().enumerate().filter(|(_, c)| **c == '@') {
                let (u, d) = (&s[..at], &s[at + 1..]);
                if !u.is_empty() && u.iter().all(|&c| all_chars(c)) && is_domain(d) {
                    push(&mut out, "username", u);
                    out.push(' ');
                    push(&mut out, "domain", d);
                    found = true;
                    break;
                }
            }
        }
        // social tag: "@" (accepted+ | domain)
        if !found && s.first() == Some(&'@') {
            let u = &s[1..];
            if (!u.is_empty() && u.iter().all(|&c| accepted(c))) || is_domain(u) {
                push(&mut out, "username", u);
                found = true;
            }
        }
        if !found {
            return None;
        }
        out.push_str(" preserve_order: true }");
        Some(out)
    }
}

impl SpanTagger for Electronic {
    fn max_words(&self, c: char) -> usize {
        usize::from(all_chars(c) || c == '@')
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        // every reading holds a `.` (domain) or starts/contains `@`
        if !span.iter().any(|&c| c == '.' || c == '@') {
            return None;
        }
        Some((0, Self::tag(span)?))
    }
}

//! `de/taggers/electronic.py`: `url | domain | email | tag`.
//!
//! The `www.` protocol alternative ties with `www.` kept in the domain (no weights); the oracle keeps it in the domain
//! (`www.auf.click` → `domain: "www.auf.click"`, `http://www.zu.do` → `protocol: "http://" domain: "www.zu.do"`), so
//! only the `http(s)://` protocols are emitted.

use std::sync::LazyLock;

use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, cost, match_str};

const WEIGHT: Cost = cost(1.11);

crate::labels!(SYMBOL_ROWS, "de/electronic/symbols.tsv");

/// The one-char symbol keys (all ASCII), as a lookup table.
static SYMBOLS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut t = [false; 128];
    for k in SYMBOL_ROWS.iter().filter_map(|r| r.first()) {
        let mut c = k.chars();
        if let (Some(x), None) = (c.next(), c.next()) {
            assert!(x.is_ascii(), "non-ASCII electronic symbol");
            t[x as usize] = true;
        }
    }
    t
});

fn symbol(c: char) -> bool {
    c.is_ascii() && SYMBOLS[c as usize]
}

/// `accepted_characters` (no `.`).
fn acc(c: char) -> bool {
    c.is_ascii_alphanumeric() || (c != '.' && symbol(c))
}

/// `all_characters`.
fn all(c: char) -> bool {
    c.is_ascii_alphanumeric() || symbol(c)
}

/// Every end of `accepted+ (dot accepted+){min_dots,} dot?` starting at `s`.
fn domain_ends(text: &[char], s: usize, min_dots: usize, out: &mut Vec<usize>) {
    let mut i = s;
    let mut dots = 0;
    let mut seg = 0;
    while i < text.len() {
        let c = text[i];
        if acc(c) {
            seg += 1;
            i += 1;
            if dots >= min_dots {
                out.push(i);
            }
        } else if c == '.' {
            if seg == 0 {
                break;
            }
            if dots >= min_dots {
                out.push(i + 1);
            }
            dots += 1;
            seg = 0;
            i += 1;
        } else {
            break;
        }
    }
}

pub struct Electronic;

impl Spans for Electronic {
    fn may_start(&self, c: char) -> bool {
        all(c) || c == '@'
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        // every branch starts with `@` (tag) or an `all_characters` run (`.` is one) that holds a `.` (domain, url)
        // or ends before `@` (email)
        if text[start] != '@' {
            let mut r = start;
            let mut dot = false;
            while r < text.len() && all(text[r]) {
                dot |= text[r] == '.';
                r += 1;
            }
            if r == start || !(dot || text.get(r) == Some(&'@')) {
                return;
            }
        }
        let tag = |body: &dyn Fn(&mut String)| {
            let mut t = String::from("electronic { ");
            body(&mut t);
            t.push_str(" preserve_order: true }");
            t
        };
        let mut ends = Vec::new();
        // domain_graph (and url)
        let mut proto = None;
        for p in ["https://", "http://"] {
            if let Some(n) = match_str(text, start, p) {
                proto = Some((p, start + n));
                break;
            }
        }
        if let Some((p, ds)) = proto {
            domain_ends(text, ds, 1, &mut ends);
            for &e in &ends {
                out.push(e, WEIGHT, || {
                    tag(&|t| {
                        t.push_str("protocol: \"");
                        t.push_str(p);
                        t.push_str("\" domain: \"");
                        t.extend(&text[ds..e]);
                        t.push('"');
                    })
                });
            }
            ends.clear();
        }
        domain_ends(text, start, 1, &mut ends);
        for &e in &ends {
            out.push(e, WEIGHT, || {
                tag(&|t| {
                    t.push_str("domain: \"");
                    t.extend(&text[start..e]);
                    t.push('"');
                })
            });
        }
        ends.clear();
        // email: all_characters+ "@" domain
        let mut at = start;
        while at < text.len() && all(text[at]) {
            at += 1;
        }
        if at > start && text.get(at) == Some(&'@') {
            domain_ends(text, at + 1, 1, &mut ends);
            for &e in &ends {
                out.push(e, WEIGHT, || {
                    tag(&|t| {
                        t.push_str("username: \"");
                        t.extend(&text[start..at]);
                        t.push_str("\" domain: \"");
                        t.extend(&text[at + 1..e]);
                        t.push('"');
                    })
                });
            }
            ends.clear();
        }
        // tag: "@" accepted+ (dot accepted+)* dot?
        if text[start] == '@' {
            domain_ends(text, start + 1, 0, &mut ends);
            for &e in &ends {
                out.push(e, WEIGHT, || {
                    tag(&|t| {
                        t.push_str("username: \"");
                        t.extend(&text[start + 1..e]);
                        t.push('"');
                    })
                });
            }
        }
    }
}

//! `de/verbalizers/electronic.py`.
//!
//! The domain is the cheapest segmentation into single chars (0.0001 each) and `server_name.tsv` / `domain.tsv`
//! keys (0), pieces joined by spaces; then every symbol and digit is spelled (`verbalize_characters`), and a final
//! ` punkt` becomes `.`.

use crate::lang::common::Fields;
use crate::lang::de::taggers::cardinal::W;
use crate::tag::Dict;
use crate::text::{Cost, cost};

crate::tsv!(SYMBOLS, "de/electronic/symbols.tsv");
crate::tsv!(SERVER, "de/electronic/server_name.tsv");
crate::tsv!(DOMAIN, "de/electronic/domain.tsv");

const CHAR: Cost = cost(0.0001);

/// `verbalize_characters` on one char (`graph_symbols | graph_digit`), or the char itself.
fn spell(c: char, out: &mut String) {
    if c.is_ascii_digit() {
        out.push_str(W.single(c as u8 - b'0'));
        return;
    }
    let mut b = [0u8; 4];
    match SYMBOLS.exact(c.encode_utf8(&mut b)).next() {
        Some(r) => out.push_str(&r.value),
        None => out.push(c),
    }
}

fn spell_str(s: &str, out: &mut String) {
    for c in s.chars() {
        spell(c, out);
    }
}

/// `add_space_after_char` + spelling.
fn spaced(s: &str) -> Option<String> {
    if s.is_empty() || s.contains(' ') {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 3);
    for (i, c) in s.chars().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        spell(c, &mut out);
    }
    Some(out)
}

fn domain(s: &str) -> Option<String> {
    let t: Vec<char> = s.chars().collect();
    let n = t.len();
    if n == 0 {
        return None;
    }
    // best[i] = (cost, previous end, table value)
    let mut best: Vec<Option<(Cost, usize, Option<String>)>> = vec![None; n + 1];
    best[0] = Some((0, 0, None));
    for i in 0..n {
        let Some(c) = best[i].as_ref().map(|b| b.0) else { continue };
        let mut relax = |e: usize, k: Cost, v: Option<&str>| {
            if best[e].as_ref().is_none_or(|b| c + k < b.0) {
                best[e] = Some((c + k, i, v.map(str::to_string)));
            }
        };
        relax(i + 1, CHAR, None);
        for (r, e) in SERVER.prefix_matches(&t, i) {
            if e > i {
                relax(e, 0, Some(&r.value));
            }
        }
        for (r, e) in DOMAIN.prefix_matches(&t, i) {
            if e > i {
                relax(e, 0, Some(&r.value));
            }
        }
    }
    let mut pieces = Vec::new();
    let mut e = n;
    while e > 0 {
        let (_, p, v) = best[e].take()?;
        pieces.push((p, v));
        e = p;
    }
    let mut out = String::with_capacity(n * 3);
    for (k, (p, v)) in pieces.into_iter().rev().enumerate() {
        if k > 0 {
            out.push(' ');
        }
        match v {
            Some(v) => spell_str(&v, &mut out),
            None => spell(t[p], &mut out),
        }
    }
    Some(out)
}

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let mut out = match f.peek_key()? {
        "protocol" => {
            let p = spaced(f.take("protocol")?)?;
            format!("{p} {}", domain(f.take("domain")?)?)
        }
        "domain" => domain(f.take("domain")?)?,
        "username" => {
            let u = spaced(f.take("username")?)?;
            match f.opt("domain")? {
                Some(dm) => format!("{u} at {}", domain(dm)?),
                None => format!("at {u}"),
            }
        }
        _ => return None,
    };
    while f.preserve_order() {}
    if !f.done() {
        return None;
    }
    if let Some(p) = out.strip_suffix(" punkt") {
        out = format!("{p}.");
    }
    Some((out.replace('\u{a0}', " "), 0))
}

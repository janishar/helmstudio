//! `es/verbalizers/electronic.py` (deterministic).

use super::{is_key, rest_is_po, s};
use crate::tag::Field;
use crate::text::Table;

crate::tsv!(SYMBOLS, "es/electronic/symbols.tsv");
crate::tsv!(SERVER, "es/electronic/server_name.tsv");
crate::tsv!(DOMAIN, "es/electronic/domain.tsv");

/// `verbalize_characters`: symbols and digits spelled out (cdrewrite over the spaced string).
fn spell(c: &str) -> String {
    if let Some(r) = SYMBOLS.exact(c).next() {
        return r.value.clone();
    }
    match c {
        "0" => "cero".into(),
        "1" => "uno".into(),
        _ if c.len() == 1 && c.as_bytes()[0].is_ascii_digit() => {
            crate::lang::es::taggers::cardinal::digit(c.chars().next().unwrap()).unwrap().to_string()
        }
        _ => c.to_string(),
    }
}

/// Apply `verbalize_characters` to an already spaced string: each symbol/digit rewritten in place.
fn verbalize_chars(s: &str) -> String {
    let mut out = String::with_capacity(s.len() * 2);
    for ch in s.chars() {
        let mut b = [0u8; 4];
        out.push_str(&spell(ch.encode_utf8(&mut b)));
    }
    out
}

fn spaced(s: &str) -> Option<String> {
    if s.is_empty() || s.contains(' ') {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 2);
    for (i, c) in s.chars().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        out.push(c);
    }
    Some(out)
}

/// Domain: cheapest segmentation into chars (0.0001 each) or common domain/server words (free).
fn domain(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let chars: Vec<char> = s.chars().collect();
    let n = chars.len();
    // best[i] = (cost, pieces)
    let mut best: Vec<Option<(u32, Vec<String>)>> = vec![None; n + 1];
    best[0] = Some((0, Vec::new()));
    let tables: [&Table; 2] = [&DOMAIN, &SERVER];
    for i in 0..n {
        let Some((c, pieces)) = best[i].clone() else {
            continue;
        };
        let mut relax = |j: usize, cc: u32, piece: String| {
            let cand = (c + cc, {
                let mut p = pieces.clone();
                p.push(piece);
                p
            });
            if best[j].as_ref().is_none_or(|b| cand.0 < b.0) {
                best[j] = Some(cand);
            }
        };
        if chars[i] != '"' {
            relax(i + 1, 1, verbalize_chars(&chars[i].to_string()));
        }
        for t in tables {
            for (r, e) in t.prefix_matches(&chars, i) {
                if e > i {
                    relax(e, 0, verbalize_chars(&r.value));
                }
            }
        }
    }
    best[n].take().map(|(_, p)| p.join(" "))
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    if is_key(d, 0, "protocol") {
        let p = spaced(s(d, 0, "protocol")?)?;
        let p: String = p
            .split(' ')
            .map(|c| SYMBOLS.exact(c).next().map(|r| r.value.clone()).unwrap_or(c.to_string()))
            .collect::<Vec<_>>()
            .join(" ");
        let dm = domain(s(d, 1, "domain")?)?;
        i = 2;
        return rest_is_po(d, i).then(|| format!("{p} {dm}"));
    }
    if is_key(d, 0, "domain") {
        let dm = domain(s(d, 0, "domain")?)?;
        return rest_is_po(d, 1).then_some(dm);
    }
    let u = spaced(s(d, 0, "username")?)?;
    let u: String = u.split(' ').map(spell).collect::<Vec<_>>().join(" ");
    i += 1;
    if is_key(d, i, "domain") {
        let dm = domain(s(d, i, "domain")?)?;
        return rest_is_po(d, i + 1).then(|| format!("{u} arroba {dm}"));
    }
    rest_is_po(d, i).then(|| format!("arroba {u}"))
}

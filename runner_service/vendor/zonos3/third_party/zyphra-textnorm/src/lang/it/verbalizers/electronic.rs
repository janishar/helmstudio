//! `it/verbalizers/electronic.py`.

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::{Cost, cost};

crate::tsv!(DIGIT, "it/numbers/digit.tsv");
crate::tsv!(ZERO, "it/numbers/zero.tsv");
crate::tsv!(SYMBOLS, "it/electronic/symbols.tsv");
crate::tsv!(SERVER, "it/electronic/server_name.tsv");
crate::tsv!(DOMAIN, "it/electronic/domain.tsv");

/// `cdrewrite(graph_symbols | graph_digit)` replacement per char.
static CHAR_MAP: LazyLock<Vec<(char, String)>> = LazyLock::new(|| {
    let mut v: Vec<(char, String)> = Vec::new();
    for r in SYMBOLS.rows() {
        if let Some(c) = r.key.chars().next() {
            v.push((c, r.value.clone()));
        }
    }
    for t in [&*DIGIT, &*ZERO] {
        for r in t.rows() {
            if let Some(c) = r.value.chars().next() {
                v.push((c, r.key.clone()));
            }
        }
    }
    v
});

fn map_char(c: char, digits: bool, out: &mut String) {
    for (k, v) in CHAR_MAP.iter() {
        if *k == c && (digits || !k.is_ascii_digit()) {
            out.push_str(v);
            return;
        }
    }
    out.push(c);
}

/// `add_space_after_char` then the rewrite: every char spaced; no spaces allowed in the value.
fn spelled(s: &str, digits: bool) -> Option<String> {
    if s.contains(' ') {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 3);
    for c in s.chars() {
        if !out.is_empty() {
            out.push(' ');
        }
        map_char(c, digits, &mut out);
    }
    Some(out)
}

/// `domain`: shortest path over `convert_defaults (" " convert_defaults)*` with each single char at 0.0001 and
/// `server_name`/`domain.tsv` keys at 0, then the symbol/digit rewrite.
fn domain(s: &str) -> String {
    let chars: Vec<char> = s.chars().collect();
    let n = chars.len();
    const CH: Cost = cost(0.0001);
    // best[i] = (cost, prev, piece output)
    let mut best: Vec<Option<(Cost, usize, Option<&str>)>> = vec![None; n + 1];
    best[0] = Some((0, 0, None));
    for i in 0..n {
        let Some((c0, _, _)) = best[i] else { continue };
        if best[i + 1].is_none_or(|(b, _, _)| c0 + CH < b) {
            best[i + 1] = Some((c0 + CH, i, None));
        }
        for t in [&*SERVER, &*DOMAIN] {
            for (r, e) in t.prefix_matches(&chars, i) {
                if best[e].is_none_or(|(b, _, _)| c0 < b) {
                    best[e] = Some((c0, i, Some(r.value.as_str())));
                }
            }
        }
    }
    let mut pieces: Vec<(usize, Option<&str>)> = Vec::new();
    let mut e = n;
    while e > 0 {
        let (_, p, piece) = best[e].expect("every char is a piece");
        pieces.push((p, piece));
        e = p;
    }
    pieces.reverse();
    let mut out = String::new();
    for (p, piece) in pieces {
        if !out.is_empty() {
            out.push(' ');
        }
        match piece {
            // rewritten pieces still go through the symbol/digit rewrite (e.g. "punto com" stays)
            Some(v) => {
                for c in v.chars() {
                    map_char(c, true, &mut out);
                }
            }
            None => map_char(chars[p], true, &mut out),
        }
    }
    out
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let out = match f.peek_key()? {
        "protocol" => {
            let p = spelled(f.take("protocol")?, false)?;
            let d = f.take("domain")?;
            format!("{p} {}", domain(d))
        }
        "domain" => domain(f.take("domain")?),
        "username" => {
            let u = spelled(f.take("username")?, true)?;
            match f.opt("domain")? {
                Some(d) => format!("{u} chiocciola {}", domain(d)),
                None => format!("chiocciola {u}"),
            }
        }
        _ => return None,
    };
    super::end(&mut f).then_some(out)
}

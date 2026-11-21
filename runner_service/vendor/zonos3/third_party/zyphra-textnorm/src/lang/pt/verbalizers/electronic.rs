//! `pt/verbalizers/electronic.py`. Usernames and domains are segmented into known units (server names, spoken
//! units, and for domains the `domain.tsv` suffixes) at cost 0 and single characters at 0.0001 each, then every
//! symbol/digit character is spelled (`verbalize_characters`).

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::Table;

crate::tsv!(DIGIT, "pt/numbers/digit.tsv");
crate::tsv!(ZERO, "pt/numbers/zero.tsv");
crate::tsv!(SYMBOLS, "pt/electronic/symbols.tsv");
crate::tsv!(SERVER, "pt/electronic/server_name.tsv");
crate::tsv!(DOMAIN, "pt/electronic/domain.tsv");
crate::tsv!(SPOKEN, "pt/electronic/electronic_spoken_unit.tsv");

/// Single-char rewrites, sorted by char: `verbalize_characters` (symbols, digits, zero) and the protocol's
/// symbol-only rewrite.
struct Spell {
    chars: Vec<(char, &'static str)>,
    symbols: Vec<(char, &'static str)>,
}

fn single_char_rows(t: &'static Table, out: &mut Vec<(char, &'static str)>) {
    for r in t.rows() {
        let mut cs = r.key.chars();
        if let (Some(c), None) = (cs.next(), cs.next()) {
            if !out.iter().any(|&(k, _)| k == c) {
                out.push((c, r.value.as_str()));
            }
        }
    }
}

static SPELL: LazyLock<Spell> = LazyLock::new(|| {
    let mut symbols = Vec::new();
    single_char_rows(&SYMBOLS, &mut symbols);
    let mut chars = symbols.clone();
    single_char_rows(&DIGIT, &mut chars);
    single_char_rows(&ZERO, &mut chars);
    chars.sort_unstable_by_key(|&(c, _)| c);
    symbols.sort_unstable_by_key(|&(c, _)| c);
    Spell { chars, symbols }
});

fn lookup(t: &[(char, &'static str)], c: char) -> Option<&'static str> {
    t.binary_search_by_key(&c, |&(k, _)| k).ok().map(|i| t[i].1)
}

/// Cheapest segmentation: fewest single-char segments; among equal counts the first unit found (shortest key
/// first at each position, tables in union order) wins. Single chars are spelled (`verbalize_characters`).
fn segment(s: &str, tables: &[&'static Table], out: &mut String) {
    const STACK: usize = 64;
    let n = s.chars().count();
    if n <= STACK {
        let mut chars = ['\0'; STACK];
        for (slot, c) in chars.iter_mut().zip(s.chars()) {
            *slot = c;
        }
        let mut best = [(0usize, 0usize, None); STACK + 1];
        segment_into(&chars[..n], tables, &mut best[..=n], out);
    } else {
        let chars: Vec<char> = s.chars().collect();
        let mut best = vec![(0usize, 0usize, None); n + 1];
        segment_into(&chars, tables, &mut best, out);
    }
}

/// `best[i]` = (singles, next position, unit value or `None` for a single char) for the suffix from `i`.
fn segment_into(
    s: &[char],
    tables: &[&'static Table],
    best: &mut [(usize, usize, Option<&'static str>)],
    out: &mut String,
) {
    let n = s.len();
    best[n] = (0, n, None);
    for i in (0..n).rev() {
        let mut b = (best[i + 1].0.saturating_add(1), i + 1, None);
        for t in tables {
            let rows: &'static [crate::text::Row] = t.rows();
            for (row, e) in t.prefix_matches(s, i) {
                if e > i && best[e].0 < b.0 {
                    // the same row through the 'static table (prefix_matches ties it to the text borrow)
                    let value = rows.iter().find(|r| std::ptr::eq(*r, row)).map(|r| r.value.as_str());
                    b = (best[e].0, e, value);
                }
            }
        }
        best[i] = b;
    }
    let mut i = 0;
    while i < n {
        if i > 0 {
            out.push(' ');
        }
        let (_, next, unit) = best[i];
        match unit.or_else(|| lookup(&SPELL.chars, s[i])) {
            Some(v) => out.push_str(v),
            None => out.push(s[i]),
        }
        i = next;
    }
}

/// `add_space_after_char @ cdrewrite(graph_symbols)`.
fn protocol(s: &str, out: &mut String) {
    for (i, c) in s.chars().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        match lookup(&SPELL.symbols, c) {
            Some(v) => out.push_str(v),
            None => out.push(c),
        }
    }
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let user_tables: [&'static Table; 2] = [&SERVER, &SPOKEN];
    let domain_tables: [&'static Table; 3] = [&DOMAIN, &SERVER, &SPOKEN];
    let mut out = String::with_capacity(64);
    match f.peek_key()? {
        "protocol" => {
            let p = f.take("protocol")?;
            if p.contains(' ') {
                return None;
            }
            protocol(p, &mut out);
            out.push(' ');
            segment(f.take("domain")?, &domain_tables, &mut out);
        }
        "domain" => segment(f.take("domain")?, &domain_tables, &mut out),
        "username" => {
            let u = f.take("username")?;
            match f.opt("domain")? {
                Some(dm) => {
                    segment(u, &user_tables, &mut out);
                    out.push_str(" arroba ");
                    segment(dm, &domain_tables, &mut out);
                }
                None => {
                    out.push_str("arroba ");
                    segment(u, &user_tables, &mut out);
                }
            }
        }
        _ => return None,
    }
    f.preserve_order();
    f.done().then_some(out)
}

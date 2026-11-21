//! `en/verbalizers/electronic.py` (deterministic).

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::lang::en::taggers::cardinal::DIGIT;
use crate::tag::Dict;
use crate::text::{Table, is_alpha, is_digit, is_upper};

crate::tsv!(SYMBOL, "en/electronic/symbol.tsv");
crate::tsv!(DOMAIN, "en/electronic/domain.tsv");
crate::tsv!(WORDS, "en/electronic/words.tsv");

/// Output side of `words.tsv` (the dict words a domain keeps verbatim at −0.0001 each).
static DICT_OUT: LazyLock<Table> =
    LazyLock::new(|| Table::from_pairs(WORDS.rows().iter().map(|r| (r.value.clone(), r.value.clone()))));

/// `default_chars_symbols`: every symbol / digit replaced by ` word ` (`d000` → ` d thousand `, preferred).
/// `None` if a `{`/`}` occurs.
fn chars_symbols(s: &[char]) -> Option<String> {
    let mut out = String::new();
    let mut i = 0;
    while i < s.len() {
        let c = s[i];
        if c == '{' || c == '}' {
            return None;
        }
        if is_digit(c) {
            if c != '0' && s.get(i + 1..i + 4) == Some(&['0', '0', '0']) {
                out.push(' ');
                out.push_str(DIGIT[c as usize - '0' as usize]);
                out.push_str(" thousand ");
                i += 4;
                continue;
            }
            out.push(' ');
            out.push_str(DIGIT[c as usize - '0' as usize]);
            out.push(' ');
            i += 1;
            continue;
        }
        if let Some((r, e)) = SYMBOL.prefix_matches(s, i).last() {
            out.push(' ');
            out.push_str(&r.value);
            out.push(' ');
            i = e;
            continue;
        }
        out.push(c);
        i += 1;
    }
    Some(out)
}

/// `domain_all` on `s`: symbols spelled, letters lowercased except dict words (kept, −0.0001 each). Returns the
/// output and the number of dict words used, or `None` if some char cannot be lowercased/kept.
fn domain_all(s: &[char]) -> Option<(String, usize)> {
    let rewritten: Vec<char> = chars_symbols(s)?.chars().collect();
    let n = rewritten.len();
    // DP maximizing dict matches; earliest-found on ties
    let mut best: Vec<Option<(usize, String)>> = vec![None; n + 1];
    best[0] = Some((0, String::new()));
    for i in 0..n {
        let Some((k, acc)) = best[i].clone() else { continue };
        let c = rewritten[i];
        for (r, e) in DICT_OUT.prefix_matches(&rewritten, i) {
            let cand = (k + 1, format!("{acc}{}", r.value));
            if best[e].as_ref().is_none_or(|b| cand.0 > b.0) {
                best[e] = Some(cand);
            }
        }
        let low = if is_upper(c) || c.is_ascii_lowercase() || c == ' ' { Some(c.to_ascii_lowercase()) } else { None };
        if let Some(l) = low {
            let cand = (k, format!("{acc}{l}"));
            if best[i + 1].as_ref().is_none_or(|b| cand.0 > b.0) {
                best[i + 1] = Some(cand);
            }
        }
    }
    best[n].clone().map(|(k, s)| (s, k))
}

/// The `domain` field (cheapest path).
fn domain(v: &str) -> Option<String> {
    let s: Vec<char> = v.trim_end_matches(' ').chars().collect();
    // (cost in 1e-4 units, output); `domain_all` alone costs 100
    let mut best: Option<(i64, String)> = None;
    for split in 0..s.len() {
        if s[split] != '.' {
            continue;
        }
        let Some((pre, k)) = domain_all(&s[..split]) else { continue };
        let commons: Vec<(usize, &str)> =
            DOMAIN.prefix_matches(&s, split).map(|(r, e)| (e, r.value.as_str())).collect();
        let mut opts: Vec<(usize, String, i64)> = Vec::new();
        if commons.is_empty() {
            opts.push((split + 1, "dot".into(), 1));
        } else {
            for (e, o) in commons {
                opts.push((e, o.to_string(), 0));
            }
        }
        for (e, mid, c) in opts {
            let rest = if e == s.len() { Some(String::new()) } else { chars_symbols(&s[e..]).map(|r| format!(" {r}")) };
            let Some(rest) = rest else { continue };
            let cost = c - k as i64;
            let out = format!("{pre} {mid}{rest}");
            if best.as_ref().is_none_or(|b| cost < b.0) {
                best = Some((cost, out));
            }
        }
    }
    let out = match best {
        Some((_, o)) => o,
        None => domain_all(&s)?.0,
    };
    Some(out)
}

fn collapse(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut prev_space = false;
    for c in s.chars() {
        if c == ' ' {
            if !prev_space {
                out.push(c);
            }
            prev_space = true;
        } else {
            out.push(c);
            prev_space = false;
        }
    }
    out
}

pub fn electronic(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out = String::new();
    if let Some(p) = f.opt("protocol")? {
        if p.is_empty() {
            return None;
        }
        out.push_str(p);
    }
    if let Some(u) = f.opt("username")? {
        if u.is_empty() {
            return None;
        }
        let chars: Vec<char> = u.chars().collect();
        let spaced = chars.first().is_some_and(|&c| is_alpha(c))
            && chars.iter().all(|&c| is_alpha(c) || c == ' ')
            && chars.contains(&' ');
        let user = if spaced { u.to_string() } else { chars_symbols(&chars)? };
        out.push_str(&user);
        out.push_str(" at ");
    }
    let dv = f.take("domain")?;
    if dv.is_empty() {
        return None;
    }
    out.push_str(&domain(dv)?);
    f.done().then(|| collapse(&out))
}

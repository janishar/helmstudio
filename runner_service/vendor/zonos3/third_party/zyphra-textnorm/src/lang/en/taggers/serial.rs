//! `en/taggers/serial.py` (deterministic).
//!
//! The composed grammar accepts a whitespace-free span (≥ 2 chars, not `alpha+/alpha+`) that splits into chunks
//! (an ASCII-letter run, a digit run or one symbol of `whitelist/symbol.tsv`) separated by single delimiters
//! (`-`, `/`, `" "`), where some window of chunks is a `serial_graph` piece (+0.0001): letters·number,
//! number·letters, number·number·number, or number·number with a symbol in it. It is tried on the input as typed
//! and, failing that, on the input with spaces inserted between letters/symbols and digits/symbols
//! (`graph_with_space`; union order puts the unspaced form first on equal cost). `X^2`/`X^3` → `X squared/cubed`
//! at no extra weight.

use crate::lang::en::lazy::building;
use crate::lang::en::taggers::cardinal::{graph, single_digits};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_alpha, is_digit, is_nemo_space};

crate::labels!(SYMBOL, "en/whitelist/symbol.tsv");

/// One-char keys of `whitelist/symbol.tsv` with the value of their first row, sorted by key.
static SYMBOLS: std::sync::LazyLock<Vec<(char, &'static str)>> = std::sync::LazyLock::new(|| {
    let mut v: Vec<(char, &'static str)> = Vec::new();
    for r in SYMBOL.iter() {
        let mut it = r[0].chars();
        let (Some(c), None) = (it.next(), it.next()) else { continue };
        if !v.iter().any(|x| x.0 == c) {
            v.push((c, r[1].as_str()));
        }
    }
    v.sort_by_key(|x| x.0);
    v
});

fn symbol_value(c: char) -> Option<&'static str> {
    SYMBOLS.binary_search_by_key(&c, |x| x.0).ok().map(|i| SYMBOLS[i].1)
}

fn is_symbol(c: char) -> bool {
    SYMBOLS.binary_search_by_key(&c, |x| x.0).is_ok()
}

#[derive(Clone, Copy, PartialEq, Eq)]
enum K {
    Alpha,
    Num,
}

/// `num_graph` on a digit run.
fn num(d: &str) -> Option<String> {
    if d.len() >= 6 || d.starts_with('0') { single_digits(d) } else { graph(d) }
}

/// Parse `v` (chars) into chunks/delims and verbalize it, if some window is a serial piece: letters·number,
/// number·letters, three numbers in a row, or two numbers with a symbol in or between them. With `build` false
/// only the acceptance is decided (the returned string is empty). Only the last two chunks are kept.
fn parse(v: &[char], build: bool) -> Option<String> {
    // per chunk: (kind, symbol-ish: the chunk is a symbol or a "/" follows it)
    let mut prev2: Option<(K, bool)> = None;
    let mut prev: Option<(K, bool)> = None;
    let mut ok = false;
    let push = |k: (K, bool), prev2: &mut Option<(K, bool)>, prev: &mut Option<(K, bool)>, ok: &mut bool| {
        if let Some(a) = *prev {
            if (a.0 == K::Alpha && k.0 == K::Num) || (a.0 == K::Num && k.0 == K::Alpha) {
                *ok = true;
            }
            if a.0 == K::Num && k.0 == K::Num && (a.1 || k.1) {
                *ok = true;
            }
            if let Some(b) = *prev2 {
                if b.0 == K::Num && a.0 == K::Num && k.0 == K::Num {
                    *ok = true;
                }
            }
        }
        *prev2 = *prev;
        *prev = Some(k);
    };
    let mut out = String::new();
    let mut i = 0;
    while i < v.len() {
        if prev.is_some() {
            let d = v[i];
            if !matches!(d, '-' | '/' | ' ') {
                return None;
            }
            if build {
                out.push(d);
            }
            if d == '/' {
                if let Some(l) = prev.as_mut() {
                    l.1 = true;
                }
            }
            i += 1;
            if i >= v.len() {
                return None;
            }
        }
        let c = v[i];
        if is_alpha(c) {
            let s = i;
            while i < v.len() && is_alpha(v[i]) {
                i += 1;
            }
            if build {
                out.extend(&v[s..i]);
            }
            push((K::Alpha, false), &mut prev2, &mut prev, &mut ok);
        } else if is_digit(c) {
            let s = i;
            while i < v.len() && is_digit(v[i]) {
                i += 1;
            }
            // num_graph accepts every digit run (≥ 6 digits or a leading zero: spelled; else the FAR)
            if build {
                let d: String = v[s..i].iter().collect();
                out.push_str(&num(&d)?);
            }
            let first = prev.is_none();
            push((K::Num, false), &mut prev2, &mut prev, &mut ok);
            // next_alpha_or_num: delimiter + num + insert(" ") + alphas
            if !first && i < v.len() && is_alpha(v[i]) {
                let s = i;
                while i < v.len() && is_alpha(v[i]) {
                    i += 1;
                }
                if build {
                    out.push(' ');
                    out.extend(&v[s..i]);
                }
                push((K::Alpha, false), &mut prev2, &mut prev, &mut ok);
            }
        } else if let Some(val) = symbol_value(c) {
            if build {
                out.push_str(val);
            }
            i += 1;
            push((K::Num, true), &mut prev2, &mut prev, &mut ok);
        } else {
            return None;
        }
    }
    ok.then_some(out)
}

/// Whether [`spaced`] inserts any space into `s` (else it is `s` itself).
fn spaces_needed(s: &[char]) -> bool {
    s.windows(2).any(|w| {
        let (p, c) = (w[0], w[1]);
        if c == '-' || p == '-' {
            return false;
        }
        let (ls, rs) = (is_symbol(p), is_symbol(c));
        ((is_alpha(p) || ls) && (is_digit(c) || rs)) || ((is_digit(p) || ls) && (is_alpha(c) || rs))
    })
}

/// `graph_with_space`: a space between (letter|symbol) and (digit|symbol), and between (digit|symbol) and
/// (letter|symbol).
fn spaced(s: &[char]) -> Vec<char> {
    let mut v = Vec::with_capacity(s.len() * 2);
    for (i, &c) in s.iter().enumerate() {
        if i > 0 && c != '-' && s[i - 1] != '-' {
            let p = s[i - 1];
            let ls = is_symbol(p);
            let rs = is_symbol(c);
            if ((is_alpha(p) || ls) && (is_digit(c) || rs)) || ((is_digit(p) || ls) && (is_alpha(c) || rs)) {
                v.push(' ');
            }
        }
        v.push(c);
    }
    v
}

fn is_alpha_slash_alpha(s: &[char]) -> bool {
    let Some(k) = s.iter().position(|&c| c == '/') else { return false };
    k > 0 && k + 1 < s.len() && s[..k].iter().all(|&c| is_alpha(c)) && s[k + 1..].iter().all(|&c| is_alpha(c))
}

/// `SerialFst.graph` on exactly `s`: `(output, cost)`. The output is built only when tags are (`num_graph` accepts
/// every digit run, so building never changes acceptance).
pub fn serial(s: &[char]) -> Option<(String, Cost)> {
    if s.len() < 2 || is_alpha_slash_alpha(s) {
        return None;
    }
    let build = building();
    if let [pre @ .., '^', p] = s {
        if !pre.is_empty() && (*p == '2' || *p == '3') {
            let mut o = String::new();
            if build {
                o.extend(pre);
                o.push_str(if *p == '2' { " squared" } else { " cubed" });
            }
            return Some((o, 0));
        }
    }
    let accept = |v: &[char]| -> Option<String> {
        parse(v, false)?;
        if build { parse(v, true) } else { Some(String::new()) }
    };
    if let Some(o) = accept(s) {
        return Some((o, cost(0.0001)));
    }
    // the spaced form equals `s` (already rejected) unless some space is inserted
    if !spaces_needed(s) {
        return None;
    }
    accept(&spaced(s)).map(|o| (o, cost(0.0001)))
}

pub struct Serial;

/// The `name: "…"` tag of serial output `o` (spaces as NBSP).
fn tag(o: &str) -> String {
    let mut tag = String::with_capacity(o.len() + 10);
    tag.push_str("name: \"");
    tag.extend(o.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
    tag.push('"');
    tag
}

impl Classifier for Serial {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let mut end = start;
        let mut numeric = false;
        while end < text.len() && !is_nemo_space(text[end]) {
            numeric |= is_digit(text[end]) || is_symbol(text[end]);
            end += 1;
        }
        if !numeric {
            return;
        }
        for e in start + 2..=end {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            if let Some((o, c)) = serial(&text[start..e]) {
                let tag = if building() { tag(&o) } else { String::new() };
                out.push(Candidate { end: e, cost: cost(1.12) + c, rank: 0, tag });
            }
        }
    }

    /// The tag is a function of the span alone (one serial output per span): written from it, no rerun.
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let o = crate::lang::en::lazy::with_build(|| serial(&text[start..candidate.end]));
        debug_assert!(o.as_ref().is_some_and(|x| cost(1.12) + x.1 == candidate.cost), "serial winner at {start}");
        if let Some((o, _)) = o {
            candidate.tag = tag(&o);
        }
    }
}

//! `de/taggers/cardinal.py`: `CardinalFst`.
//!
//! Every alternative of the number graph differs in weight (`und` insertion +0.0001, `eins` −0.0001 from `ones.tsv`,
//! plural scale −0.0001), so each input has one cheapest output; the functions here return it with its weight.

use std::sync::LazyLock;

use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, cost};

crate::tsv!(ZERO, "de/numbers/zero.tsv");
crate::tsv!(DIGIT, "de/numbers/digit.tsv");
crate::tsv!(ONES, "de/numbers/ones.tsv");
crate::tsv!(TEEN, "de/numbers/teen.tsv");
crate::tsv!(TIES, "de/numbers/ties.tsv");

pub const WEIGHT: Cost = cost(1.1);
const PLURAL: Cost = cost(-0.0001);
const AND: &str = "und";

/// The inverted number tables, indexed by digit.
pub struct Words {
    pub zero: String,
    /// `digit.tsv` (2–9).
    pub digit: [String; 10],
    /// `teen.tsv` (1x).
    pub teen: [String; 10],
    /// `ties.tsv` (x0, 2–9).
    pub ties: [String; 10],
    /// The cheapest `ones.tsv` row (`eins`, −0.0001).
    pub one: String,
    pub one_cost: Cost,
}

fn by_digit(t: &crate::text::Table) -> [String; 10] {
    let mut out: [String; 10] = Default::default();
    for r in t.rows() {
        let v = r.value.as_bytes();
        let i = (v[v.len() - 1] - b'0') as usize;
        if out[i].is_empty() {
            out[i] = r.key.clone();
        }
    }
    out
}

pub static W: LazyLock<Words> = LazyLock::new(|| {
    let mut one = ONES.rows()[0].clone();
    for r in ONES.rows() {
        if r.cost < one.cost {
            one = r.clone();
        }
    }
    Words {
        zero: ZERO.rows()[0].key.clone(),
        digit: by_digit(&DIGIT),
        teen: by_digit(&TEEN),
        ties: by_digit(&TIES),
        one: one.key,
        one_cost: one.cost,
    }
});

impl Words {
    /// `graph_digit = digit | ones` on one digit 1–9.
    pub fn graph_digit(&self, d: u8) -> (&str, Cost) {
        if d == 1 { (&self.one, self.one_cost) } else { (&self.digit[d as usize], 0) }
    }
    /// The digit words of `decimal`/`telephone`/`electronic` (`digit.tsv | zero.tsv | 1 → eins`).
    pub fn single(&self, d: u8) -> &str {
        match d {
            0 => &self.zero,
            1 => "eins",
            _ => &self.digit[d as usize],
        }
    }
}

/// End of the ASCII digit run starting at `s`.
pub fn digit_end(text: &[char], s: usize) -> usize {
    let mut e = s;
    while e < text.len() && text[e].is_ascii_digit() {
        e += 1;
    }
    e
}

fn dv(c: char) -> u8 {
    c as u8 - b'0'
}

/// `tens_no_zero` on two digits.
fn tens(b: u8, c: u8, out: &mut String) -> Option<Cost> {
    let w = &*W;
    match (b, c) {
        (0, 0) => None,
        (0, c) => {
            let (s, k) = w.graph_digit(c);
            out.push_str(s);
            Some(k)
        }
        (1, c) => {
            out.push_str(&w.teen[c as usize]);
            Some(0)
        }
        (b, 0) => {
            out.push_str(&w.ties[b as usize]);
            Some(0)
        }
        (b, c) => {
            out.push_str(if c == 1 { "ein" } else { &w.digit[c as usize] });
            out.push(' ');
            out.push_str(AND);
            out.push(' ');
            out.push_str(&w.ties[b as usize]);
            Some(0)
        }
    }
}

/// `hundred_non_zero` on three digits (`None` for `000`).
fn hundred(a: u8, b: u8, c: u8, out: &mut String) -> Option<Cost> {
    if a == 0 {
        return tens(b, c, out);
    }
    if a == 1 {
        out.push_str("ein ");
    } else {
        out.push_str(&W.digit[a as usize]);
        out.push(' ');
    }
    out.push_str("hundert");
    if b == 0 && c == 0 {
        return Some(0);
    }
    out.push(' ');
    tens(b, c, out)
}

const SCALES: [(&str, &str); 6] = [
    ("trilliarde", "n"),
    ("trillion", "en"),
    ("billiarde", "n"),
    ("billion", "en"),
    ("milliarde", "n"),
    ("million", "en"),
];

const FIX_SYNTAX: [(&str, &str); 5] = [
    ("eins tausend", "ein tausend"),
    ("eins millionen", "eine million"),
    ("eins milliarden", "eine milliarde"),
    ("eins billionen", "eine billion"),
    ("eins billiarden", "eine billiarde"),
];

/// `CardinalFst.graph` on a digit string.
pub fn graph(d: &[char]) -> Option<(String, Cost)> {
    let mut out = String::new();
    let c = write_graph(d, &mut out)?;
    Some((out, c))
}

thread_local! {
    /// Scratch for [`write_graph`]'s raw (uncollapsed) words.
    static RAW: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}

/// [`graph`] appended to `out` (`out` is unchanged when `None`).
pub fn write_graph(d: &[char], out: &mut String) -> Option<Cost> {
    let n = d.len();
    if n == 0 || n > 24 || !d.iter().all(char::is_ascii_digit) {
        return None;
    }
    if n == 1 {
        match d[0] {
            '0' => {
                out.push_str(&W.zero);
                return Some(0);
            }
            '1' => {
                out.push_str("eins");
                return Some(0);
            }
            _ => {}
        }
    }
    if d[0] == '0' {
        return None;
    }
    let mut g = [0u8; 24];
    for (i, &c) in d.iter().enumerate() {
        g[24 - n + i] = dv(c);
    }
    RAW.with(|raw| {
        let raw = &mut *raw.borrow_mut();
        raw.clear();
        let mut c: Cost = 0;
        for (i, (scale, plural)) in SCALES.iter().enumerate() {
            if i > 0 {
                raw.push(' ');
            }
            let (a, b, cc) = (g[3 * i], g[3 * i + 1], g[3 * i + 2]);
            if a | b | cc != 0 {
                c += hundred(a, b, cc, raw)?;
                raw.push(' ');
                raw.push_str(scale);
                raw.push_str(plural);
                c += PLURAL;
            }
        }
        raw.push(' ');
        if g[18] | g[19] | g[20] != 0 {
            c += hundred(g[18], g[19], g[20], raw)?;
            raw.push_str(" tausend");
        }
        if g[21] | g[22] | g[23] != 0 {
            raw.push(' ');
            c += hundred(g[21], g[22], g[23], raw)?;
        }
        // delete_space at BOS and EOS, "  " → " " left to right, fix_syntax at BOS
        let t = raw.trim_matches(' ');
        let (from, to) = FIX_SYNTAX.iter().find(|(from, _)| t.starts_with(from)).copied().unwrap_or(("", ""));
        out.push_str(to);
        let t = &t[from.len()..];
        let mut rest = t;
        while let Some(k) = rest.find("  ") {
            out.push_str(&rest[..=k]);
            rest = &rest[k + 2..];
        }
        out.push_str(rest);
        Some(c)
    })
}

/// The weight of `hundred_non_zero` on one non-zero group: only a group ending `01` reads `eins` (`ones.tsv`).
fn group_cost(b: char, c: char) -> Cost {
    if b == '0' && c == '1' { W.one_cost } else { 0 }
}

/// The weight of [`graph`] (`None` exactly where `graph` is `None`) without building the words.
pub fn graph_cost(d: &[char]) -> Option<Cost> {
    let n = d.len();
    if n == 0 || n > 24 || !d.iter().all(char::is_ascii_digit) {
        return None;
    }
    if n == 1 {
        return Some(0);
    }
    if d[0] == '0' {
        return None;
    }
    let mut g = ['0'; 24];
    g[24 - n..].copy_from_slice(d);
    let mut c: Cost = 0;
    for (i, grp) in g.chunks_exact(3).enumerate() {
        if grp != ['0', '0', '0'] {
            c += group_cost(grp[1], grp[2]) + if i < 6 { PLURAL } else { 0 };
        }
    }
    Some(c)
}

/// `graph_hundred_component_at_least_one_none_zero_digit` on 1–3 digits.
pub fn h3(d: &[char]) -> Option<(String, Cost)> {
    let mut out = String::new();
    let c = write_h3(d, &mut out)?;
    Some((out, c))
}

/// [`h3`] appended to `out` (`out` is unchanged when `None`).
pub fn write_h3(d: &[char], out: &mut String) -> Option<Cost> {
    if d.is_empty() || d.len() > 3 || d[0] == '0' || !d.iter().all(char::is_ascii_digit) {
        return None;
    }
    if d == ['1'] {
        out.push_str("eins");
        return Some(0);
    }
    let mut g = [0u8; 3];
    for (i, &c) in d.iter().enumerate() {
        g[3 - d.len() + i] = dv(c);
    }
    // `hundred` writes nothing before its `None` cases except for a leading hundreds word; roll back on failure
    let n = out.len();
    let c = hundred(g[0], g[1], g[2], out);
    if c.is_none() {
        out.truncate(n);
    }
    c
}

/// The weight of [`h3`] without the words.
pub fn h3_cost(d: &[char]) -> Option<Cost> {
    if d.is_empty() || d.len() > 3 || d[0] == '0' || !d.iter().all(char::is_ascii_digit) {
        return None;
    }
    if d == ['1'] {
        return Some(0);
    }
    let n = d.len();
    Some(group_cost(if n >= 2 { d[n - 2] } else { '0' }, d[n - 1]))
}

/// `two_digit_non_zero`.
pub fn two_digit(d: &[char]) -> Option<(String, Cost)> {
    if d.len() > 2 { None } else { h3(d) }
}

/// The weight of [`two_digit`] without the words.
pub fn two_digit_cost(d: &[char]) -> Option<Cost> {
    if d.len() > 2 { None } else { h3_cost(d) }
}

/// `CardinalFst.fst` spans at `s` (without the `cardinal { … }` wrapper): `f(end, cost)`; [`fields`] writes one.
pub fn fst(text: &[char], s: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, Cost)) {
    let neg = text.get(s) == Some(&'-');
    let p = s + neg as usize;
    let r = digit_end(text, p);
    for e in p + 1..=r.min(p + 24) {
        if !keep(e) {
            continue;
        }
        if let Some(c) = graph_cost(&text[p..e]) {
            f(e, c);
        }
    }
}

/// The fields of the [`fst`] span `text[s..e]`: `negative: "true" integer: "…"`.
pub fn fields(text: &[char], s: usize, e: usize, out: &mut String) {
    let neg = text[s] == '-';
    if neg {
        out.push_str("negative: \"true\" ");
    }
    out.push_str("integer: \"");
    write_graph(&text[s + neg as usize..e], out).expect("fst span");
    out.push('"');
}

pub struct Cardinal;

impl Spans for Cardinal {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit() || c == '-'
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if !(text[start].is_ascii_digit() || text[start] == '-') {
            return;
        }
        fst(text, start, &|_| true, &mut |e, c| {
            out.push(e, WEIGHT + c, || {
                let mut t = String::from("cardinal { ");
                fields(text, start, e, &mut t);
                t.push_str(" }");
                t
            })
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn g(s: &str) -> String {
        graph(&s.chars().collect::<Vec<_>>()).unwrap().0
    }

    #[test]
    fn oracle_spacing() {
        // oracle: `eine milliarde  eins`, `eins trilliarden    eins`, `eine million eins tausend`
        assert_eq!(g("1000000001"), "eine milliarde  eins");
        assert_eq!(g("1000000000000000000001"), "eins trilliarden    eins");
        assert_eq!(g("1001000"), "eine million eins tausend");
        assert_eq!(g("2001001001"), "zwei milliarden eins millionen eins tausend eins");
        assert_eq!(g("1990"), "ein tausend neun hundert neunzig");
        assert_eq!(g("5000000000000"), "fünf billionen");
    }
}

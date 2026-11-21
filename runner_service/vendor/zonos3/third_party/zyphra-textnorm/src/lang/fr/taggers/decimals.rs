//! `fr/taggers/decimals.py`: `DecimalFst` (deterministic branch).

use super::cardinal::{digit, num_ends, reading_into, reading_rank, w_double, w_hundreds};
use crate::lang::fr::lazy::{Lazy, Out, candidate, lazy_classifier, wanted};
use crate::lattice::Candidate;
use crate::text::{Cost, cost, is_digit};

crate::tsv!(QUANTITIES, "fr/numbers/quantities.tsv");

pub struct Decimal;

pub const WEIGHT: Cost = cost(1.1);

fn zero() -> &'static str {
    super::cardinal::zero()
}

/// `graph_digit` = `digit | zero`.
fn graph_digit(c: char) -> Option<&'static str> {
    if c == '0' { Some(zero()) } else { digit(c) }
}

/// `all_double_digits` accepts `ab` (the tables cover 10–99).
fn is_double(a: char, b: char) -> bool {
    is_digit(a) && is_digit(b) && a != '0'
}

fn zeros(x: &[char]) -> bool {
    x.iter().all(|&c| c == '0')
}

/// Fractional `graph` branches (deterministic union of `decimals.py`).
enum Frac {
    /// `graph_digit (" " zero)*`, −0.00001
    Digit,
    /// `all_double_digits (" " zero)*`, −0.00002
    Double,
    /// `all_hundreds (" " zero)*`, +0.00001
    Hundreds,
    /// `double (" " double){k-1} (" " zero)? ((" " graph_digit)? | (" " zero)*)` with `k` pairs, −0.00002
    Pairs(usize),
    /// `zero (" " zero)* (" " graph_digit)*`, 0
    Zeros,
}

/// The cheapest branch accepting the digit string `f` and its cost (ties: first in union order, which is how
/// `shortestpath` resolves the equal-cost `Double`/`Pairs` alternatives here).
fn frac_branch(f: &[char]) -> Option<(Frac, Cost)> {
    let n = f.len();
    if n >= 2 && is_double(f[0], f[1]) && zeros(&f[2..]) {
        return Some((Frac::Double, cost(-0.00002)));
    }
    let mut k = 0;
    while 2 * k + 2 <= n && is_double(f[2 * k], f[2 * k + 1]) {
        k += 1;
        if k >= 2 {
            let tail = &f[2 * k..];
            if tail.len() <= 1 || tail[0] == '0' && tail.len() == 2 || zeros(tail) {
                return Some((Frac::Pairs(k), cost(-0.00002)));
            }
        }
    }
    if n >= 1 && is_digit(f[0]) && zeros(&f[1..]) {
        return Some((Frac::Digit, cost(-0.00001)));
    }
    if n >= 1 && f[0] == '0' && f.iter().all(|&c| is_digit(c)) {
        return Some((Frac::Zeros, 0));
    }
    if n >= 3 && f[0] != '0' && is_digit(f[1]) && is_digit(f[2]) && zeros(&f[3..]) {
        return Some((Frac::Hundreds, cost(0.00001)));
    }
    None
}

fn push_digits<O: Out>(out: &mut O, rest: &[char]) {
    for &c in rest {
        out.push(' ');
        out.push_str(graph_digit(c).unwrap_or(""));
    }
}

/// Appends the cheapest output of the fractional `graph` for the digit string `f`; returns its cost.
pub fn fractional_into<O: Out>(f: &[char], out: &mut O) -> Option<Cost> {
    let (b, c) = frac_branch(f)?;
    match b {
        Frac::Digit => {
            out.push_str(graph_digit(f[0])?);
            push_digits(out, &f[1..]);
        }
        Frac::Double => {
            w_double(f[0], f[1], out)?;
            push_digits(out, &f[2..]);
        }
        Frac::Hundreds => {
            w_hundreds(&f[..3], out)?;
            push_digits(out, &f[3..]);
        }
        Frac::Pairs(k) => {
            for i in 0..k {
                if i > 0 {
                    out.push(' ');
                }
                w_double(f[2 * i], f[2 * i + 1], out)?;
            }
            push_digits(out, &f[2 * k..]);
        }
        Frac::Zeros => {
            out.push_str(zero());
            push_digits(out, &f[1..]);
        }
    }
    Some(c)
}

/// `decimal { [negative: "true" ]integer_part: "<int>"` written to a new tag buffer.
fn head<O: Out>(neg: bool, int: &[char]) -> Option<(O, bool)> {
    let mut t = O::new();
    t.push_str(if neg { "decimal { negative: \"true\" integer_part: \"" } else { "decimal { integer_part: \"" });
    let exact = reading_into(int, &mut t)?;
    t.push('"');
    Some((t, exact))
}

fn finish<O: Out>(mut t: O, quantity: Option<&str>) -> O {
    if let Some(q) = quantity {
        t.push_str(" quantity: \"");
        t.push_str(q);
        t.push('"');
    }
    t.push_str(" preserve_order: true }");
    t
}

impl Lazy for Decimal {
    fn generate<O: Out>(&self, text: &[char], start: usize, want: Option<usize>, out: &mut Vec<Candidate>) {
        let (neg, s) = if text.get(start) == Some(&'-') { (true, start + 1) } else { (false, start) };
        if !text.get(s).is_some_and(|&c| is_digit(c)) {
            return;
        }
        // integer "," fractional [" " quantity]
        for iend in num_ends(text, s) {
            if text.get(iend) != Some(&',') || !text.get(iend + 1).is_some_and(|&c| is_digit(c)) {
                continue;
            }
            if want.is_some_and(|w| w < iend + 2) {
                continue;
            }
            let Some((h, exact)) = head::<O>(neg, &text[s..iend]) else { continue };
            let rank = reading_rank(exact);
            let fs = iend + 1;
            let mut fe = fs;
            while fe < text.len() && is_digit(text[fe]) {
                fe += 1;
                // a quantity after `fe` ends past `fe + 1`
                if !(wanted(want, fe) || want.is_some_and(|w| w > fe + 1 && text.get(fe) == Some(&' '))) {
                    continue;
                }
                let mut t = O::new();
                t.append(&h);
                t.push_str(" fractional_part: \"");
                let Some(fc) = fractional_into(&text[fs..fe], &mut t) else { continue };
                t.push('"');
                if text.get(fe) == Some(&' ') {
                    for (row, qend) in QUANTITIES.prefix_matches(text, fe + 1) {
                        if qend > fe + 1 && wanted(want, qend) {
                            let mut q = O::new();
                            q.append(&t);
                            let tag = finish(q, Some(&row.value));
                            out.push(candidate(qend, WEIGHT + fc + row.cost, rank, tag));
                        }
                    }
                }
                if wanted(want, fe) {
                    out.push(candidate(fe, WEIGHT + fc, rank, finish(t, None)));
                }
            }
        }
        // numbers (1-6 digits, spaces deleted) " " quantity
        let mut digits = ['0'; 6];
        let mut nd = 0;
        let mut e = s;
        while e < text.len() && (is_digit(text[e]) || text[e] == ' ') {
            if text[e] == ' ' {
                for (row, qend) in QUANTITIES.prefix_matches(text, e + 1) {
                    if qend > e + 1 && wanted(want, qend) {
                        let Some((h, exact)) = head::<O>(neg, &digits[..nd]) else { break };
                        let tag = finish(h, Some(&row.value));
                        out.push(candidate(qend, WEIGHT + row.cost, reading_rank(exact), tag));
                    }
                }
            } else {
                if nd == 6 {
                    break;
                }
                digits[nd] = text[e];
                nd += 1;
            }
            e += 1;
        }
    }
}

lazy_classifier!(Decimal);

/// `quantities.tsv` words as chars.
static QUANTITY_WORDS: std::sync::LazyLock<Vec<Vec<char>>> =
    std::sync::LazyLock::new(|| QUANTITIES.rows().iter().map(|r| r.value.chars().collect()).collect());

/// Whether `s` is a `quantities.tsv` word.
pub fn is_quantity(s: &[char]) -> bool {
    QUANTITY_WORDS.iter().any(|w| w[..] == *s)
}

/// First chars of the `quantities.tsv` words.
static QUANTITY_FIRST: std::sync::LazyLock<Vec<char>> =
    std::sync::LazyLock::new(|| QUANTITIES.rows().iter().filter_map(|r| r.value.chars().next()).collect());

/// Whether a `quantities.tsv` word starts at `i`.
pub fn quantity_at(text: &[char], i: usize) -> bool {
    text.get(i).is_some_and(|c| QUANTITY_FIRST.contains(c)) && QUANTITIES.prefix_matches(text, i).any(|(_, e)| e > i)
}

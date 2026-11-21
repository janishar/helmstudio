//! `fr/taggers/cardinal.py`: `CardinalFst`.
//!
//! `all_nums` is `final_graph` before tokens (`all_nums_no_tokens`): every output of the masculine graph for one input
//! span. The feminine union (`… @ (Σ* + "un" → "une")`) adds equal-cost alternatives the oracle never picks (no `une`
//! value in any golden tag), so it is not emitted.

use crate::lang::fr::lazy::{Lazy, Out, candidate, lazy_classifier, wanted};
use crate::lattice::Candidate;
use crate::text::{Cost, cost, is_digit};

crate::tsv!(ZERO, "fr/numbers/zero.tsv");
crate::tsv!(DIGIT, "fr/numbers/digit.tsv");
crate::tsv!(TEN, "fr/numbers/ten.tsv");
crate::tsv!(TEENS, "fr/numbers/teens.tsv");
crate::tsv!(TENS, "fr/numbers/tens_simple.tsv");
crate::tsv!(SEVENTY, "fr/numbers/seventy.tsv");
crate::tsv!(EIGHTY, "fr/numbers/eighty.tsv");
crate::tsv!(NINETY, "fr/numbers/ninety.tsv");

/// One-char-key table as a digit-indexed array (built once).
fn by_digit(t: &'static crate::text::Table) -> [Option<&'static str>; 10] {
    let mut a = [None; 10];
    for r in t.rows() {
        let mut k = r.key.chars();
        if let (Some(c @ '0'..='9'), None) = (k.next(), k.next()) {
            let slot = &mut a[c as usize - '0' as usize];
            if slot.is_none() {
                *slot = Some(r.value.as_str());
            }
        }
    }
    a
}

struct Words {
    digit: [Option<&'static str>; 10],
    /// `teens.tsv` by second digit (key "1d").
    teen: [Option<&'static str>; 10],
    ten: Option<&'static str>,
    tens: [Option<&'static str>; 10],
    seventy: [Option<&'static str>; 10],
    eighty: [Option<&'static str>; 10],
    ninety: [Option<&'static str>; 10],
    zero: &'static str,
}

static WORDS: std::sync::LazyLock<Words> = std::sync::LazyLock::new(|| {
    let mut teen = [None; 10];
    for r in TEENS.rows() {
        let k: Vec<char> = r.key.chars().collect();
        if let ['1', c @ '0'..='9'] = k[..] {
            teen[c as usize - '0' as usize].get_or_insert(r.value.as_str());
        }
    }
    Words {
        digit: by_digit(&DIGIT),
        teen,
        ten: TEN.rows().iter().find(|r| r.key == "10").map(|r| r.value.as_str()),
        tens: by_digit(&TENS),
        seventy: by_digit(&SEVENTY),
        eighty: by_digit(&EIGHTY),
        ninety: by_digit(&NINETY),
        zero: ZERO.rows().iter().find(|r| r.key == "0").map_or("", |r| r.value.as_str()),
    }
});

#[inline]
fn di(c: char) -> Option<usize> {
    c.is_ascii_digit().then(|| c as usize - '0' as usize)
}

/// `zero.tsv`.
pub fn zero() -> &'static str {
    WORDS.zero
}

/// `digits` (1–9).
pub fn digit(c: char) -> Option<&'static str> {
    WORDS.digit[di(c)?]
}

fn digit_no_one(c: char) -> Option<&'static str> {
    if c == '1' { None } else { digit(c) }
}

fn teen(a: char, b: char) -> Option<&'static str> {
    if a != '1' {
        return None;
    }
    WORDS.teen[di(b)?]
}

fn table1(t: &[Option<&'static str>; 10], c: char) -> Option<&'static str> {
    t[di(c)?]
}

/// Runs `f` on `out`; on failure restores `out` to its previous length.
#[inline]
fn attempt<O: Out>(out: &mut O, f: impl FnOnce(&mut O) -> Option<()>) -> bool {
    let len = out.len();
    let ok = f(out).is_some();
    if !ok {
        out.truncate(len);
    }
    ok
}

/// `all_double_digits` (10–99) written to `out`.
pub fn w_double<O: Out>(a: char, b: char, out: &mut O) -> Option<()> {
    if !is_digit(a) || !is_digit(b) {
        return None;
    }
    if a == '1' && b == '0' {
        out.push_str(WORDS.ten?);
        return Some(());
    }
    if let Some(t) = teen(a, b) {
        out.push_str(t);
        return Some(());
    }
    if let Some(t) = table1(&WORDS.tens, a) {
        match b {
            '0' => out.push_str(t),
            '1' => {
                out.push_str(t);
                out.push_str(" et un");
            }
            _ => {
                let d = digit_no_one(b)?;
                out.push_str(t);
                out.push('-');
                out.push_str(d);
            }
        }
        return Some(());
    }
    if let Some(t) = table1(&WORDS.seventy, a) {
        match b {
            '0' => {
                out.push_str(t);
                out.push_str("-dix");
            }
            '1' => {
                out.push_str(t);
                out.push_str(" et onze");
            }
            _ => {
                let d = teen('1', b)?;
                out.push_str(t);
                out.push('-');
                out.push_str(d);
            }
        }
        return Some(());
    }
    if let Some(t) = table1(&WORDS.eighty, a) {
        out.push_str(t);
        match b {
            '0' => out.push('s'),
            _ => {
                out.push('-');
                out.push_str(digit(b)?);
            }
        }
        return Some(());
    }
    if let Some(t) = table1(&WORDS.ninety, a) {
        out.push_str(t);
        match b {
            '0' => out.push_str("-dix"),
            _ => {
                out.push('-');
                out.push_str(teen('1', b)?);
            }
        }
        return Some(());
    }
    None
}

/// `all_double_digits` on a two-char input (10–99).
pub fn double(x: &[char]) -> Option<String> {
    let [a, b] = *x else { return None };
    let mut s = String::new();
    attempt(&mut s, |o| w_double(a, b, o)).then_some(s)
}

/// `" " + hundreds_parts` (two chars).
fn w_hundreds_parts<O: Out>(a: char, b: char, out: &mut O) -> Option<()> {
    out.push(' ');
    if a == '0' {
        out.push_str(digit(b)?);
        return Some(());
    }
    w_double(a, b, out)
}

/// `all_hundreds` (three chars, 100–999).
pub fn w_hundreds<O: Out>(x: &[char], out: &mut O) -> Option<()> {
    let [h, a, b] = *x else { return None };
    if !is_digit(a) || !is_digit(b) {
        return None;
    }
    if h == '1' {
        out.push_str("cent");
        if a == '0' && b == '0' {
            return Some(());
        }
        return w_hundreds_parts(a, b, out);
    }
    out.push_str(digit_no_one(h)?);
    if a == '0' && b == '0' {
        out.push_str(" cents");
        return Some(());
    }
    out.push_str(" cent");
    w_hundreds_parts(a, b, out)
}

/// `all_hundreds` (three chars, 100–999).
pub fn hundreds(x: &[char]) -> Option<String> {
    let mut s = String::new();
    attempt(&mut s, |o| w_hundreds(x, o)).then_some(s)
}

/// `digits_to_hundreds_no_one` on a digit string.
fn w_to_hundreds_no_one<O: Out>(x: &[char], out: &mut O) -> Option<()> {
    match *x {
        [c] => out.push_str(digit_no_one(c)?),
        [a, b] => return w_double(a, b, out),
        [_, _, _] => return w_hundreds(x, out),
        _ => return None,
    }
    Some(())
}

/// `all_thousands`.
fn w_thousands<O: Out>(x: &[char], out: &mut O) -> Option<()> {
    if x.len() < 4 {
        return None;
    }
    let t = &x[x.len() - 3..];
    if !t.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let mut p = &x[..x.len() - 3];
    if p.last() == Some(&' ') {
        p = &p[..p.len() - 1];
    }
    if p.is_empty() || !p.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let one = p == ['1'];
    if one {
        out.push_str("mille");
    } else {
        w_to_hundreds_no_one(p, out)?;
        out.push_str(" mille");
    }
    if t == ['0', '0', '0'] {
        return Some(());
    }
    out.push(' ');
    if t[0] == '0' && t[1] == '0' {
        if one && t[2] == '1' {
            out.push_str("et un");
            return Some(());
        }
        out.push_str(if one { digit_no_one(t[2])? } else { digit(t[2])? });
        return Some(());
    }
    if t[0] == '0' {
        return w_double(t[1], t[2], out);
    }
    w_hundreds(t, out)
}

/// `all_thousands`.
pub fn thousands(x: &[char]) -> Option<String> {
    let mut s = String::new();
    attempt(&mut s, |o| w_thousands(x, o)).then_some(s)
}

/// `digits_to_hundreds_no_one` on a digit string.
fn to_hundreds_no_one(x: &[char]) -> Option<String> {
    let mut s = String::new();
    attempt(&mut s, |o| w_to_hundreds_no_one(x, o)).then_some(s)
}

/// `millions_components` / `billions_components`: `(" "? DIGIT{3,}){n,}` with the spaces deleted.
fn components(tail: &[char], n: usize) -> Option<Vec<char>> {
    let mut digits = Vec::with_capacity(tail.len());
    let mut groups = 0;
    let mut i = 0;
    while i < tail.len() {
        if tail[i] == ' ' {
            i += 1;
        }
        let st = i;
        while i < tail.len() && is_digit(tail[i]) {
            digits.push(tail[i]);
            i += 1;
        }
        let len = i - st;
        if len < 3 {
            return None;
        }
        groups += len / 3;
        if i < tail.len() && tail[i] != ' ' {
            return None;
        }
    }
    (groups >= n).then_some(digits)
}

fn to_thousands_no_one(x: &[char]) -> Option<String> {
    to_hundreds_no_one(x).or_else(|| thousands(x))
}

/// Outputs of the `all_millions` / `all_billions` branch.
fn scale(x: &[char], n: usize, max_zeros: usize, one: &str, many: &str, out: &mut Vec<String>) {
    for plen in 1..=3 {
        if plen > x.len() {
            continue;
        }
        let p = &x[..plen];
        if !p.iter().all(|&c| is_digit(c)) {
            continue;
        }
        let head = if p == ['1'] {
            format!("un {one}")
        } else {
            match to_hundreds_no_one(p) {
                Some(h) => format!("{h} {many}"),
                None => continue,
            }
        };
        let Some(d) = components(&x[plen..], n) else {
            continue;
        };
        let k = d.iter().take_while(|&&c| c == '0').count();
        if k > max_zeros {
            continue;
        }
        let rest = &d[k..];
        if rest.is_empty() {
            out.push(head);
            continue;
        }
        if rest == ['1'] {
            out.push(format!("{head} et un"));
            continue;
        }
        if n == 2 {
            if let Some(r) = to_thousands_no_one(rest) {
                out.push(format!("{head} {r}"));
            }
        } else {
            let mut subs = Vec::new();
            subs.extend(to_thousands_no_one(rest));
            millions(rest, &mut subs);
            for r in subs {
                out.push(format!("{head} {r}"));
            }
        }
    }
}

fn millions(x: &[char], out: &mut Vec<String>) {
    scale(x, 2, 6, "million", "millions", out);
}

/// The reading of `x` whose value equals its digits, written to `out`, if the grammar has it: the scale word sits
/// exactly above the last 6 (millions) or 9 (milliards) digits.
fn w_exact<O: Out>(x: &[char], nd: usize, out: &mut O) -> Option<()> {
    match nd {
        1..=3 => w_small(x, out),
        4..=6 => w_thousands(x, out),
        7..=9 => w_scale_exact(x, 2, 6, "million", "millions", nd - 6, out),
        10..=12 => w_scale_exact(x, 3, 9, "milliard", "milliards", nd - 9, out),
        _ => None,
    }
}

fn exact(x: &[char]) -> Option<String> {
    let nd = x.iter().filter(|c| is_digit(**c)).count();
    let mut s = String::new();
    attempt(&mut s, |o| w_exact(x, nd, o)).then_some(s)
}

/// The exact branch of `all_millions` / `all_billions`: head of `plen` digits, then the `3n`-digit tail (spaces
/// deleted, ≤ `max_zeros` leading zeros) read exactly.
fn w_scale_exact<O: Out>(
    x: &[char],
    n: usize,
    max_zeros: usize,
    one: &str,
    many: &str,
    plen: usize,
    out: &mut O,
) -> Option<()> {
    let p = x.get(..plen)?;
    if !p.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let mut buf = ['0'; 12];
    let d = components_into(&x[plen..], n, &mut buf)?;
    if p == ['1'] {
        out.push_str("un ");
        out.push_str(one);
    } else {
        w_to_hundreds_no_one(p, out)?;
        out.push(' ');
        out.push_str(many);
    }
    let k = d.iter().take_while(|&&c| c == '0').count();
    if k > max_zeros {
        return None;
    }
    let rest = &d[k..];
    if rest.is_empty() {
        return Some(());
    }
    out.push(' ');
    if rest == ['1'] {
        out.push_str("et un");
        return Some(());
    }
    if n == 2 {
        // digits_to_thousands_no_one
        let len = out.len();
        if w_to_hundreds_no_one(rest, out).is_some() {
            return Some(());
        }
        out.truncate(len);
        w_thousands(rest, out)
    } else {
        let nd = rest.len();
        if nd <= 3 {
            return w_to_hundreds_no_one(rest, out);
        }
        w_exact(rest, nd, out)
    }
}

/// [`components`] into a fixed buffer (exact readings only: at most 12 digits).
fn components_into<'b>(tail: &[char], n: usize, buf: &'b mut [char; 12]) -> Option<&'b [char]> {
    let mut len = 0;
    let mut groups = 0;
    let mut i = 0;
    while i < tail.len() {
        if tail[i] == ' ' {
            i += 1;
        }
        let st = i;
        while i < tail.len() && is_digit(tail[i]) {
            *buf.get_mut(len)? = tail[i];
            len += 1;
            i += 1;
        }
        let glen = i - st;
        if glen < 3 {
            return None;
        }
        groups += glen / 3;
        if i < tail.len() && tail[i] != ' ' {
            return None;
        }
    }
    (groups >= n).then_some(&buf[..len])
}

/// `zero | one_to_all_tens | all_hundreds` on 1–3 digits.
fn w_small<O: Out>(x: &[char], out: &mut O) -> Option<()> {
    match *x {
        ['0'] => out.push_str(zero()),
        [c] => out.push_str(digit(c)?),
        [a, b] => return w_double(a, b, out),
        [_, _, _] => return w_hundreds(x, out),
        _ => return None,
    }
    Some(())
}

fn all_nums_small(x: &[char]) -> Option<String> {
    let mut s = String::new();
    attempt(&mut s, |o| w_small(x, o)).then_some(s)
}

/// `all_nums_no_tokens` (masculine): every output for the span `x` (digits and single spaces), the numerically
/// correct reading first (see [`reading`]).
pub fn all_nums(x: &[char]) -> Vec<String> {
    let mut out = Vec::new();
    if x.is_empty() {
        return out;
    }
    out.extend(all_nums_small(x));
    out.extend(thousands(x));
    millions(x, &mut out);
    scale(x, 3, 9, "milliard", "milliards", &mut out);
    if let Some(e) = exact(x) {
        if let Some(i) = out.iter().position(|o| *o == e) {
            let e = out.remove(i);
            out.insert(0, e);
        }
    }
    let mut seen: Vec<String> = Vec::with_capacity(out.len());
    for o in out {
        if !seen.contains(&o) {
            seen.push(o);
        }
    }
    seen
}

/// The reading the lattice keeps for the span `x`, if `all_nums_no_tokens` accepts it. Every reading of one span
/// costs the same, so only the lowest-ranked survives: the reading whose value equals the digits (the upstream union
/// also accepts e.g. "1|00708262468" and "10|0708262468"; "100708262468" → "cent milliards …"), otherwise the first
/// in source union order. Appends it to `out` and returns whether it is the exact reading.
pub fn reading_into<O: Out>(x: &[char], out: &mut O) -> Option<bool> {
    let nd = x.iter().filter(|c| is_digit(**c)).count();
    if attempt(out, |o| w_exact(x, nd, o)) {
        return Some(true);
    }
    // 1-6 digits: `w_exact` covers every branch that can accept them; no branch accepts more than 27 digits
    // (milliards head 3 + 9 deleted zeros + a millions remainder of 15)
    if !(7..=27).contains(&nd) {
        return None;
    }
    out.push_str(&all_nums(x).into_iter().next()?);
    Some(false)
}

/// [`reading_into`] as a new string.
pub fn reading(x: &[char]) -> Option<(String, bool)> {
    let mut s = String::new();
    reading_into(x, &mut s).map(|e| (s, e))
}

/// Candidate rank of a reading: the numerically correct one before the others.
pub fn reading_rank(exact: bool) -> i32 {
    if exact { 0 } else { 100 }
}

/// Ends of every span `[start, end)` of digits and single spaces that starts and ends with a digit (the inputs
/// `all_nums_no_tokens` can accept), shortest first. Readings are built by the caller only for spans it keeps.
pub fn num_ends(text: &[char], start: usize) -> impl Iterator<Item = usize> + '_ {
    let mut end = start;
    let mut ok = start < text.len() && is_digit(text[start]);
    std::iter::from_fn(move || {
        while ok && end < text.len() {
            let c = text[end];
            if is_digit(c) {
                end += 1;
                return Some(end);
            } else if c == ' ' && end + 1 < text.len() && is_digit(text[end + 1]) {
                end += 1;
            } else {
                break;
            }
        }
        ok = false;
        None
    })
}

pub struct Cardinal;

pub const WEIGHT: Cost = cost(1.1);

impl Lazy for Cardinal {
    fn generate<O: Out>(&self, text: &[char], start: usize, want: Option<usize>, out: &mut Vec<Candidate>) {
        let (neg, s) = if text.get(start) == Some(&'-') { (true, start + 1) } else { (false, start) };
        let prefix = if neg { "cardinal { negative: \"true\" integer: \"" } else { "cardinal { integer: \"" };
        for end in num_ends(text, s).filter(|&e| wanted(want, e)) {
            let mut tag = O::new();
            tag.push_str(prefix);
            let Some(exact) = reading_into(&text[s..end], &mut tag) else { continue };
            tag.push_str("\" }");
            out.push(candidate(end, WEIGHT, reading_rank(exact), tag));
        }
    }
}

lazy_classifier!(Cardinal);

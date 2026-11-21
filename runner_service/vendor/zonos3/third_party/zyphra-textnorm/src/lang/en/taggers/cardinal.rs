//! `en/taggers/cardinal.py` (deterministic) and the two number-name FARs it loads.
//!
//! The FARs (`data/number/cardinal_number_name{,_au}.far`) have no source; their behaviour was read off the oracle
//! (`tests/golden/en_cardinal_far.jsonl.zst`, `tests/en_cardinal.rs`):
//! - `graph`: 1-15 digits without leading zero (or `0`), short-scale names without "and", weight 0.
//! - `graph_au`: 1-3 digits, or comma-grouped `d{1,3}(,ddd)+` up to 24 digits, same names; weight
//!   `0.1 × (8 − k)` where `k` counts the groups that are non-zero, the first and the last group always counting.

use crate::lang::common::field;
use crate::lang::en::lazy::building;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_digit};

pub const DIGIT: [&str; 10] = ["zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine"];
const TEEN: [&str; 10] =
    ["ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"];
pub const TY: [&str; 10] = ["", "", "twenty", "thirty", "forty", "fifty", "sixty", "seventy", "eighty", "ninety"];
const SCALE: [&str; 8] = ["", "thousand", "million", "billion", "trillion", "quadrillion", "quintillion", "sextillion"];

fn push_word(out: &mut String, w: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(w);
}

/// Words of one 1-3 digit group (nothing for `000`).
fn group(out: &mut String, g: &[u8]) {
    let (h, t, u) = match g.len() {
        1 => (0, 0, g[0]),
        2 => (0, g[0], g[1]),
        _ => (g[0], g[1], g[2]),
    };
    if h != 0 {
        push_word(out, DIGIT[h as usize]);
        push_word(out, "hundred");
    }
    match t {
        0 if u != 0 => push_word(out, DIGIT[u as usize]),
        0 => {}
        1 => push_word(out, TEEN[u as usize]),
        _ => {
            push_word(out, TY[t as usize]);
            if u != 0 {
                push_word(out, DIGIT[u as usize]);
            }
        }
    }
}

/// Names of a digit string (already validated, ≤ 24 digits, no leading zero unless it is `0`), as digit values.
fn name(d: &[u8]) -> String {
    let mut out = String::with_capacity(d.len() * 12);
    name_into(&mut out, d);
    out
}

fn name_into(out: &mut String, d: &[u8]) {
    if d == [0] {
        out.push_str("zero");
        return;
    }
    let n = d.len();
    let head = if n % 3 == 0 { 3 } else { n % 3 };
    let groups = 1 + (n - head) / 3;
    let mut i = 0;
    for gi in 0..groups {
        let len = if gi == 0 { head } else { 3 };
        let g = &d[i..i + len];
        i += len;
        if g.iter().any(|&x| x != 0) {
            group(out, g);
            let s = groups - 1 - gi;
            if s > 0 {
                push_word(out, SCALE[s]);
            }
        }
    }
}

/// Digit values of `s` into a stack buffer (`None` unless 1-24 ASCII digits).
fn digits_of(s: &str, buf: &mut [u8; 24]) -> Option<usize> {
    let b = s.as_bytes();
    if b.is_empty() || b.len() > 24 {
        return None;
    }
    for (k, &c) in b.iter().enumerate() {
        if !c.is_ascii_digit() {
            return None;
        }
        buf[k] = c - b'0';
    }
    Some(b.len())
}

/// `cardinal_number_name.far` (shortest path; the FAR is functional with weight 0). While tags are not built
/// (`lazy`), an accepted input yields an empty name.
pub fn number_name(digits: &str) -> Option<String> {
    let mut buf = [0u8; 24];
    let n = digits_of(digits, &mut buf)?;
    if n > 15 || (n > 1 && buf[0] == 0) {
        return None;
    }
    Some(if building() { name(&buf[..n]) } else { String::new() })
}

/// `cardinal_number_name_au.far` with its path weight.
pub fn number_name_au_weighted(s: &str) -> Option<(String, Cost)> {
    let mut buf = [0u8; 24];
    if !s.contains(',') {
        let n = digits_of(s, &mut buf)?;
        if n > 3 || (n > 1 && buf[0] == 0) {
            return None;
        }
        return Some((name(&buf[..n]), cost(0.7)));
    }
    let parts: Vec<&str> = s.split(',').collect();
    let first = parts[0];
    if first.is_empty() || first.len() > 3 || !first.bytes().all(|b| b.is_ascii_digit()) || first.starts_with('0') {
        return None;
    }
    if parts[1..].iter().any(|p| p.len() != 3 || !p.bytes().all(|b| b.is_ascii_digit())) {
        return None;
    }
    let joined: String = parts.concat();
    let n = digits_of(&joined, &mut buf)?;
    let last = parts.len() - 1;
    let k = parts.iter().enumerate().filter(|(i, p)| *i == 0 || *i == last || **p != "000").count() as i64;
    Some((name(&buf[..n]), (8 - k) * cost(0.1)))
}

/// `cardinal_number_name_au.far` (shortest path).
pub fn number_name_au(s: &str) -> Option<String> {
    number_name_au_weighted(s).map(|(n, _)| n)
}

/// `single_digits_graph`: every digit spelled, space separated (empty while tags are not built).
pub fn single_digits(digits: &str) -> Option<String> {
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if !building() {
        return Some(String::new());
    }
    let mut out = String::with_capacity(digits.len() * 5);
    for b in digits.bytes() {
        push_word(&mut out, DIGIT[(b - b'0') as usize]);
    }
    Some(out)
}

/// `add_optional_and` applied to one output `base` (input `input`) of a graph whose path weight is `w`.
/// Pushes `(output, cost)` in union order: plain (+0.00001), "hundred and"/"thousand and" rewrites (−0.0001, left
/// to right), optional-hundred deletion for three non-zero digits.
pub fn with_and(input: &str, base: &str, w: Cost, out: &mut Vec<(String, Cost)>) {
    out.push((base.to_string(), w + cost(0.00001)));
    for (i, _) in base.match_indices("hundred ") {
        let rest = &base[i + 8..];
        if !rest.contains("thousand") && !rest.contains("million") {
            out.push((format!("{}hundred and {}", &base[..i], rest), w + cost(-0.0001)));
        }
    }
    for (i, _) in base.match_indices("thousand ") {
        let rest = &base[i + 9..];
        if !rest.contains("hundred") {
            out.push((format!("{}thousand and {}", &base[..i], rest), w + cost(-0.0001)));
        }
    }
    if input.len() == 3 && input.bytes().all(|b| (b'1'..=b'9').contains(&b)) {
        if let Some(i) = base.find(" hundred") {
            out.push((format!("{}{}", &base[..i], &base[i + 8..]), w));
        }
    }
}

/// `self.graph`: `(d{1,3} ("," ddd)* | d{1,3} ddd*)` with commas deleted, `@ FAR` (empty while tags are not
/// built).
pub fn graph(s: &str) -> Option<String> {
    let mut out = String::new();
    if building() {
        return graph_into(s, &mut out).then_some(out);
    }
    graph_digits(s, &mut [0u8; 24]).map(|_| out)
}

/// [`graph`] appended to `out` (unchanged when it rejects `s`), always spelled.
fn graph_into(s: &str, out: &mut String) -> bool {
    let mut buf = [0u8; 24];
    let Some(n) = graph_digits(s, &mut buf) else { return false };
    out.reserve(n * 12);
    name_into(out, &buf[..n]);
    true
}

/// The digit values `graph` accepts in `s` (into `buf`), or `None`.
fn graph_digits(s: &str, buf: &mut [u8; 24]) -> Option<usize> {
    let n = if s.contains(',') {
        // groups: first 1-3 digits, then exactly 3 per comma
        let mut n = 0;
        for (gi, part) in s.split(',').enumerate() {
            if part.is_empty() || part.len() > 3 || (gi > 0 && part.len() != 3) || n + part.len() > 24 {
                return None;
            }
            for c in part.bytes() {
                if !c.is_ascii_digit() {
                    return None;
                }
                buf[n] = c - b'0';
                n += 1;
            }
        }
        n
    } else {
        digits_of(s, buf)?
    };
    (n <= 15 && (n == 1 || buf[0] != 0)).then_some(n)
}

/// `graph_with_and` on input `s`.
pub fn graph_with_and(s: &str, out: &mut Vec<(String, Cost)>) {
    let mut n = String::new();
    if graph_into(s, &mut n) {
        with_and(s, &n, 0, out);
    }
}

/// Cheapest `graph_with_and` output (first on ties) with its cost; while tags are not built the output is empty
/// (the cost still comes from the spelled name, written into a reused buffer).
pub fn graph_with_and_best(s: &str) -> Option<(String, Cost)> {
    if building() {
        let mut base = String::new();
        return graph_into(s, &mut base).then(|| with_and_best(s, base, 0));
    }
    SCRATCH.with_borrow_mut(|(_, name)| {
        name.clear();
        graph_into(s, name).then(|| (String::new(), with_and_cost(s, name, 0)))
    })
}

/// The cheapest output of [`with_and`] (the first "and" rewrite at −0.0001, else the optional-hundred deletion
/// at 0, else the plain output at +0.00001), computed without materializing the others.
pub fn with_and_best(input: &str, mut base: String, w: Cost) -> (String, Cost) {
    let c = match and_rule(input, &base) {
        AndRule::Insert(e) => {
            base.insert_str(e, " and");
            cost(-0.0001)
        }
        AndRule::DropHundred(i) => {
            base.replace_range(i..i + 8, "");
            0
        }
        AndRule::Plain => cost(0.00001),
    };
    (base, w + c)
}

/// Which [`with_and`] output is the cheapest.
enum AndRule {
    /// Insert " and" at this byte offset.
    Insert(usize),
    /// Delete " hundred" at this byte offset.
    DropHundred(usize),
    Plain,
}

fn and_rule(input: &str, base: &str) -> AndRule {
    // word scan: the first "hundred" with words after it and no later "thousand"/"million" substring, else the
    // first "thousand" with words after it and no later "hundred" substring (the FST's `not_quote` differences)
    let b = base.as_bytes();
    let rest_has = |from: usize, pat: &[u8]| b[from..].windows(pat.len()).any(|x| x == pat);
    let mut k = 0;
    let mut first_thousand: Option<usize> = None;
    while k < b.len() {
        let e = b[k..].iter().position(|&c| c == b' ').map_or(b.len(), |p| k + p);
        let word = &b[k..e];
        if e < b.len() {
            if word == b"hundred" && !rest_has(e + 1, b"thousand") && !rest_has(e + 1, b"million") {
                return AndRule::Insert(e);
            }
            if word == b"thousand" && first_thousand.is_none() && !rest_has(e + 1, b"hundred") {
                first_thousand = Some(e);
            }
        }
        k = e + 1;
    }
    if let Some(e) = first_thousand {
        return AndRule::Insert(e);
    }
    if input.len() == 3 && input.bytes().all(|b| (b'1'..=b'9').contains(&b)) {
        if let Some(i) = base.find(" hundred") {
            return AndRule::DropHundred(i);
        }
    }
    AndRule::Plain
}

/// The cost of [`with_and_best`].
fn with_and_cost(input: &str, base: &str, w: Cost) -> Cost {
    w + match and_rule(input, base) {
        AndRule::Insert(_) => cost(-0.0001),
        AndRule::DropHundred(_) => 0,
        AndRule::Plain => cost(0.00001),
    }
}

/// Cheapest of `(output, cost)` pairs, first on ties.
pub fn best_owned(v: Vec<(String, Cost)>) -> Option<(String, Cost)> {
    let mut b: Option<(String, Cost)> = None;
    for o in v {
        if b.as_ref().is_none_or(|x| o.1 < x.1) {
            b = Some(o);
        }
    }
    b
}

/// `graph_hundred_component_at_least_one_none_zero_digit`: `(d{2,3} | [1-9]) @ FAR`.
pub fn hundred_component(s: &str) -> Option<String> {
    let n = s.len();
    if (2..=3).contains(&n) || (n == 1 && s != "0") { number_name(s) } else { None }
}

/// Every output of `CardinalFst().final_graph` with its cost, in union order.
pub fn final_graph_outputs(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    let all_digits = !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit());
    if all_digits && s.len() >= 5 {
        out.push((single_digits(s).unwrap_or_default(), 0));
    } else {
        graph_with_and(s, &mut out);
    }
    if all_digits && s.starts_with('0') {
        out.push((single_digits(s).unwrap_or_default(), 0));
    }
    if let Some((n, w)) = number_name_au_weighted(s) {
        with_and(s, &n, w, &mut out);
    }
    out
}

/// The cheapest output of `final_graph` with its cost (first in union order on ties), built without materializing
/// the losing outputs. Equal to `best(&final_graph_outputs(s))` (checked by `tests/en_cardinal.rs`).
pub fn final_graph_best(s: &str) -> Option<(String, Cost)> {
    // The union's cheapest branch, decided without building the others:
    // - all digits, 5+ digits or a leading zero: single digits at 0 (graph rejects leading zeros except "0", whose
    //   graph output "zero" costs +0.00001; graph_au costs ≥ 0.7 − 0.0001 without commas);
    // - 1-4 digits otherwise: graph_with_and (≤ +0.00001) beats graph_au (≥ 0.7 − 0.0001);
    // - with commas: graph (≤ 15 digits, ≤ +0.00001) beats graph_au (≥ 0.3 − 0.0001 for ≤ 5 groups); beyond 15
    //   digits only graph_au remains.
    let all_digits = !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit());
    let mut base = String::new();
    if all_digits {
        if s.len() >= 5 || s.starts_with('0') {
            return Some((single_digits(s)?, 0));
        }
        return graph_into(s, &mut base).then(|| with_and_best(s, base, 0));
    }
    if graph_into(s, &mut base) {
        return Some(with_and_best(s, base, 0));
    }
    let (n, w) = number_name_au_weighted(s)?;
    Some(with_and_best(s, n, w))
}

/// The cost of [`final_graph_best`] (`scratch` holds the number name it inspects; no other allocation).
pub fn final_graph_cost(s: &str, scratch: &mut String) -> Option<Cost> {
    let all_digits = !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit());
    if all_digits && (s.len() >= 5 || s.starts_with('0')) {
        return Some(0);
    }
    scratch.clear();
    if graph_into(s, scratch) {
        return Some(with_and_cost(s, scratch, 0));
    }
    if all_digits {
        return None;
    }
    let (n, w) = number_name_au_weighted(s)?;
    Some(with_and_cost(s, &n, w))
}

/// The cheapest output (first on ties).
pub fn best(outs: &[(String, Cost)]) -> Option<&str> {
    let mut b: Option<&(String, Cost)> = None;
    for o in outs {
        if b.is_none_or(|x| o.1 < x.1) {
            b = Some(o);
        }
    }
    b.map(|x| x.0.as_str())
}

/// Spans of `[0-9,]+` starting at `start` (inputs any digit graph might accept).
pub fn digit_comma_end(text: &[char], start: usize) -> usize {
    let mut e = start;
    while e < text.len() && (is_digit(text[e]) || text[e] == ',') {
        e += 1;
    }
    e
}

/// `CardinalFst` tagger: `(negative: "true" )? integer: "<final>"`. Candidates carry the cost only; the winner's
/// tag is written by `materialize` (the name of a span is a function of the span).
pub struct Cardinal;

/// The number span of a cardinal candidate starting at `start`: `(negative, digits start)`.
fn sign(text: &[char], start: usize) -> (bool, usize) {
    let neg = text[start] == '-';
    (neg, if neg { start + 1 } else { start })
}

thread_local! {
    /// Digits and number-name buffers reused across [`Cardinal::candidates`] calls.
    static SCRATCH: std::cell::RefCell<(String, String)> = const { std::cell::RefCell::new((String::new(), String::new())) };
}

impl Classifier for Cardinal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let (_, s0) = sign(text, start);
        if s0 >= text.len() || !is_digit(text[s0]) {
            return;
        }
        let end = digit_comma_end(text, s0);
        SCRATCH.with_borrow_mut(|(buf, name)| {
            for e in s0 + 1..=end {
                if text[e - 1] == ',' || !crate::lang::en::end_ok(text, e) {
                    continue;
                }
                buf.clear();
                buf.extend(&text[s0..e]);
                let Some(c) = final_graph_cost(buf, name) else { continue };
                out.push(Candidate { end: e, cost: cost(1.1) + c, rank: 0, tag: String::new() });
            }
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let (neg, s0) = sign(text, start);
        let digits: String = text[s0..candidate.end].iter().collect();
        let Some((name, _)) = final_graph_best(&digits) else { return };
        let mut tag = String::from("cardinal { ");
        if neg {
            tag.push_str("negative: \"true\" ");
        }
        field(&mut tag, "integer", &name);
        tag.push_str(" }");
        candidate.tag = tag;
    }
}

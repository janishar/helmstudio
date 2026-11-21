//! `es/taggers/decimals.py`.

use super::cardinal;
use crate::lang::es::graph_utils::{NUM_ZERO, strip_cardinal_apocope};
use crate::lang::es::{Lazy, Sink, prefix_static};
use crate::text::{Cost, Row, cost, is_digit, is_nemo_space};

crate::tsv!(QUANTITIES, "es/numbers/quantities.tsv");

fn zero() -> &'static str {
    NUM_ZERO.rows()[0].key.as_str()
}

/// `digit | zero` on one digit.
fn graph_digit(d: u8) -> &'static str {
    if d == b'0' { zero() } else { cardinal::digit(d as char).unwrap() }
}

fn tens(d: &[u8]) -> Option<String> {
    if d.len() != 2 || d[0] == b'0' {
        return None;
    }
    cardinal::tens(d)
}

fn push(out: &mut String, w: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(w);
}

fn zeros(out: &mut String, d: &[u8]) -> bool {
    for _ in d {
        push(out, zero());
    }
    d.iter().all(|&c| c == b'0')
}

/// The deterministic fractional graph on digits: the cheapest reading (ties: union order).
pub fn fractional(d: &[u8]) -> Option<(String, Cost)> {
    let mut best: Option<(String, Cost)> = None;
    let mut consider = |s: String, c: Cost| {
        if best.as_ref().is_none_or(|b| c < b.1) {
            best = Some((s, c));
        }
    };
    if d.is_empty() {
        return None;
    }
    // A: graph_digit + zeros*
    let mut s = graph_digit(d[0]).to_string();
    if zeros(&mut s, &d[1..]) {
        consider(s, cost(-0.00001));
    }
    // B: tens + zeros*
    if d.len() >= 2 {
        if let Some(t) = tens(&d[..2]) {
            let mut s = t.clone();
            if zeros(&mut s, &d[2..]) {
                consider(s, cost(-0.00002));
            }
        }
    }
    // C: hundreds + zeros*
    if d.len() >= 3 && d[0] != b'0' {
        if let Some(h) = cardinal::hundreds(&d[..3]) {
            let mut s = h;
            if zeros(&mut s, &d[3..]) {
                consider(s, cost(0.00001));
            }
        }
    }
    // D: tens (tens)+ zero? (graph_digit? | zeros*)
    let mut s = String::new();
    let mut i = 0;
    while i + 2 <= d.len() {
        let Some(t) = tens(&d[i..i + 2]) else { break };
        push(&mut s, &t);
        i += 2;
        if i >= 4 {
            let rest = &d[i..];
            // tail options
            let mut tails: Vec<String> = Vec::new();
            match rest.len() {
                0 => tails.push(String::new()),
                1 => tails.push(graph_digit(rest[0]).to_string()),
                _ => {}
            }
            if rest.len() == 2 && rest[0] == b'0' {
                tails.push(format!("{} {}", zero(), graph_digit(rest[1])));
            }
            if rest.iter().all(|&c| c == b'0') && !rest.is_empty() {
                let mut z = String::new();
                zeros(&mut z, rest);
                tails.push(z);
            }
            for t in tails {
                let mut full = s.clone();
                push(&mut full, &t);
                if t.is_empty() {
                    full = s.clone();
                }
                consider(full, cost(-0.00002));
            }
        }
    }
    // E: zero zeros* graph_digit*
    if d[0] == b'0' {
        let mut s = String::new();
        for &c in d {
            push(&mut s, graph_digit(c));
        }
        consider(s, 0);
    }
    let (s, c) = best?;
    // reverse apocope before a space
    let mut out = String::with_capacity(s.len() + 4);
    let words: Vec<&str> = s.split(' ').collect();
    for (k, w) in words.iter().enumerate() {
        if k > 0 {
            out.push(' ');
        }
        if k + 1 < words.len() {
            if let Some(p) = w.strip_suffix("ún").or_else(|| w.strip_suffix("un")) {
                out.push_str(p);
                out.push_str("uno");
                continue;
            }
        }
        out.push_str(w);
    }
    Some((out, c))
}

/// The cost of [`fractional`] without building the words (the cheapest branch's weight).
pub fn fractional_cost(d: &[u8]) -> Option<Cost> {
    if d.is_empty() {
        return None;
    }
    let zero_tail = |r: &[u8]| r.iter().all(|&c| c == b'0');
    let tens_ok = |p: &[u8]| p[0] != b'0' && cardinal::tens_str(p).is_some();
    let mut best: Option<Cost> = None;
    let mut consider = |c: Cost| {
        if best.is_none_or(|b| c < b) {
            best = Some(c);
        }
    };
    // A: graph_digit + zeros*
    if zero_tail(&d[1..]) {
        consider(cost(-0.00001));
    }
    // B: tens + zeros*
    if d.len() >= 2 && tens_ok(&d[..2]) && zero_tail(&d[2..]) {
        consider(cost(-0.00002));
    }
    // C: hundreds + zeros*
    if d.len() >= 3 && cardinal::hundreds_ok(&d[..3]) && zero_tail(&d[3..]) {
        consider(cost(0.00001));
    }
    // D: tens (tens)+ zero? (graph_digit? | zeros*)
    let mut i = 0;
    while i + 2 <= d.len() && tens_ok(&d[i..i + 2]) {
        i += 2;
        let rest = &d[i..];
        if i >= 4 && (rest.len() <= 1 || (rest.len() == 2 && rest[0] == b'0') || zero_tail(rest)) {
            consider(cost(-0.00002));
        }
    }
    // E: zero zeros* graph_digit*
    if d[0] == b'0' {
        consider(0);
    }
    best
}

/// Calls `f` with the ASCII digits of `text[a..b]` (whitespace skipped), on the stack when they fit.
fn with_digits<T>(text: &[char], a: usize, b: usize, f: impl FnOnce(&[u8]) -> T) -> T {
    let mut buf = [0u8; 64];
    let mut n = 0;
    let mut heap = Vec::new();
    for &c in &text[a..b] {
        if is_digit(c) {
            if n < buf.len() {
                buf[n] = c as u8;
            } else {
                if heap.is_empty() {
                    heap.extend_from_slice(&buf);
                }
                heap.push(c as u8);
            }
            n += 1;
        }
    }
    if heap.is_empty() { f(&buf[..n]) } else { f(&heap) }
}

/// One `final_graph_wo_negative` reading at a start `s`, without its text: written by [`Dec::write`].
#[derive(Clone, Copy)]
pub struct Dec {
    pub end: usize,
    pub cost: Cost,
    /// The integer digits are `text[s..int_end]`.
    int_end: usize,
    /// `Some(fe)`: `final_graph_wo_sign` with fractional digits `text[int_end + 1..fe]` (whitespace skipped);
    /// `None`: the `integer + quantity` branch (integer separators `.`/` ` deleted, no `filter_punctuation`).
    frac_end: Option<usize>,
    quantity: Option<&'static Row>,
}

impl Dec {
    /// The body (`integer_part: "…" fractional_part: "…" quantity: "…"`) of the reading at `s`.
    pub fn write(&self, text: &[char], s: usize, out: &mut String) {
        out.push_str("integer_part: \"");
        match self.frac_end {
            Some(fe) => {
                out.push_str(&strip_cardinal_apocope(&cardinal::words_at(text, s, self.int_end)));
                out.push_str("\" fractional_part: \"");
                let (f, _) = with_digits(text, self.int_end + 1, fe, fractional).expect("accepted fractional digits");
                out.push_str(&f);
            }
            None => {
                let digits: String = text[s..self.int_end].iter().filter(|c| is_digit(**c)).collect();
                out.push_str(&cardinal::number(&digits).expect("accepted integer").0);
            }
        }
        out.push('"');
        if let Some(q) = self.quantity {
            out.push_str(" quantity: \"");
            out.push_str(&q.value);
            out.push('"');
        }
    }

    pub fn body(&self, text: &[char], s: usize) -> String {
        let mut out = String::with_capacity(96);
        self.write(text, s, &mut out);
        out
    }
}

type Decs = std::rc::Rc<[Dec]>;

thread_local! {
    /// `wo_negative` readings of the text being tagged by start (decimal, measure and money all ask for the same
    /// starts); cleared when tagging starts at position 0 ([`reset`]).
    static MEMO: std::cell::RefCell<Vec<Option<Decs>>> = const { std::cell::RefCell::new(Vec::new()) };
}

/// Forget the previous text's readings.
pub fn reset(len: usize) {
    MEMO.with_borrow_mut(|m| {
        m.clear();
        m.resize(len, None);
    });
}

/// `final_graph_wo_negative`: `wo_sign | get_quantity(...)` (memoized per input text).
pub fn wo_negative(text: &[char], start: usize) -> Decs {
    if start >= text.len() || !is_digit(text[start]) {
        return Decs::from([]);
    }
    if let Some(v) = MEMO.with_borrow(|m| m.get(start).cloned().flatten()) {
        return v;
    }
    let v: Decs = wo_negative_uncached(text, start).into();
    MEMO.with_borrow_mut(|m| {
        if m.len() < text.len() {
            m.resize(text.len(), None);
        }
        m[start] = Some(v.clone());
    });
    v
}

fn wo_negative_uncached(text: &[char], start: usize) -> Vec<Dec> {
    let mut out = Vec::new();
    // final_graph_wo_sign: integer "," fractional (digits possibly interleaved with whitespace)
    for e in cardinal::number_ends(text, start) {
        if text.get(e) != Some(&',') {
            continue;
        }
        let Some(c) = cardinal::cost_at(text, start, e) else {
            continue;
        };
        let mut i = e + 1;
        while i < text.len() && (is_digit(text[i]) || is_nemo_space(text[i])) {
            i += 1;
            if is_digit(text[i - 1]) {
                if let Some(fc) = with_digits(text, e + 1, i, fractional_cost) {
                    out.push(Dec { end: i, cost: c + fc, int_end: e, frac_end: Some(i), quantity: None });
                }
            }
        }
    }
    let quantity = |d: Dec, out: &mut Vec<Dec>| {
        if text.get(d.end) != Some(&' ') {
            return;
        }
        for (row, qe) in prefix_static(&QUANTITIES, text, d.end + 1) {
            out.push(Dec { end: qe, cost: d.cost + row.cost, quantity: Some(row), ..d });
        }
    };
    for k in 0..out.len() {
        quantity(out[k], &mut out);
    }
    // integer + quantity: [0-9. ]* with separators deleted, 1-6 digits
    let mut digits = [0u8; 6];
    let mut n = 0;
    let mut i = start;
    while i < text.len() && (is_digit(text[i]) || text[i] == '.' || text[i] == ' ') {
        if is_digit(text[i]) {
            if n == 6 {
                break;
            }
            digits[n] = text[i] as u8;
            n += 1;
        }
        i += 1;
        if n == 0 {
            continue;
        }
        if let Some(c) = cardinal::number_cost(&digits[..n]) {
            quantity(Dec { end: i, cost: c, int_end: i, frac_end: None, quantity: None }, &mut out);
        }
    }
    out
}

pub struct Decimal;

impl Lazy for Decimal {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let (neg, s) = if text[start] == '-' { ("negative: \"true\" ", start + 1) } else { ("", start) };
        if s >= text.len() || !is_digit(text[s]) {
            return;
        }
        for d in wo_negative(text, s).iter() {
            out.push(d.end, cost(1.1) + d.cost, 0, || {
                format!("decimal {{ {neg}{} preserve_order: true }}", d.body(text, s))
            });
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn fractions() {
        assert_eq!(fractional(b"5332").unwrap().0, "cincuenta y tres treinta y dos");
        assert_eq!(fractional(b"25300").unwrap().0, "veinticinco treinta cero");
        assert_eq!(fractional(b"253002").unwrap().0, "veinticinco treinta cero dos");
        assert_eq!(fractional(b"002").unwrap().0, "cero cero dos");
        assert_eq!(fractional(b"53321").unwrap().0, "cincuenta y tres treinta y dos un");
    }
}

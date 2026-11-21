//! `it/taggers/cardinal.py`. The number graph is evaluated directly on the 24-digit padded string; where the FST
//! is non-functional (`tens + digit` vs `tens_one`/`tens_eight` at -0.01) the cheapest output is built, with its cost.

use std::sync::LazyLock;

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Table, cost};

crate::tsv!(ZERO, "it/numbers/zero.tsv");
crate::tsv!(DIGIT, "it/numbers/digit.tsv");
crate::tsv!(TEEN, "it/numbers/teen.tsv");
crate::tsv!(TENS, "it/numbers/tens.tsv");
crate::tsv!(TENS_ONE, "it/numbers/tens_one.tsv");
crate::tsv!(TENS_EIGHT, "it/numbers/tens_eight.tsv");
crate::tsv!(HUNDREDS, "it/numbers/hundreds.tsv");

/// The inverted number tables, indexed by the digit that distinguishes their rows.
pub struct Words {
    pub zero: String,
    pub digit: [String; 10],
    teen: [String; 10],
    tens: [String; 10],
    tens_one: [String; 10],
    tens_eight: [String; 10],
    hundreds: [String; 10],
}

/// `invert(string_file)`: word per digit string; `pos` selects the indexing digit of the key.
fn by_digit(t: &Table, pos: usize) -> [String; 10] {
    let mut out: [String; 10] = Default::default();
    for r in t.rows() {
        if let Some(d) = r.value.as_bytes().get(pos).filter(|d| d.is_ascii_digit()) {
            out[(d - b'0') as usize] = r.key.clone();
        }
    }
    out
}

pub static WORDS: LazyLock<Words> = LazyLock::new(|| Words {
    zero: ZERO.rows().first().map(|r| r.key.clone()).unwrap_or_default(),
    digit: by_digit(&DIGIT, 0),
    teen: by_digit(&TEEN, 1),
    tens: by_digit(&TENS, 0),
    tens_one: by_digit(&TENS_ONE, 0),
    tens_eight: by_digit(&TENS_EIGHT, 0),
    hundreds: by_digit(&HUNDREDS, 0),
});

const TENS_W: Cost = cost(-0.01);
const MILLION_W: Cost = cost(-0.001);

#[inline]
fn d(b: u8) -> usize {
    (b - b'0') as usize
}

/// `graph_digit`: 1-9.
pub fn digit(u: u8, out: &mut String) -> bool {
    if (b'1'..=b'9').contains(&u) {
        out.push_str(&WORDS.digit[d(u)]);
        true
    } else {
        false
    }
}

/// `self.tens` on two digits (cheapest output).
pub fn graph_tens(t: u8, u: u8, out: &mut String, c: &mut Cost) -> bool {
    let w = &*WORDS;
    match t {
        b'1' => out.push_str(&w.teen[d(u)]),
        b'2'..=b'9' => match u {
            b'0' => out.push_str(&w.tens[d(t)]),
            b'1' => {
                out.push_str(&w.tens_one[d(t)]);
                *c += TENS_W;
            }
            b'8' => {
                out.push_str(&w.tens_eight[d(t)]);
                *c += TENS_W;
            }
            _ => {
                out.push_str(&w.tens[d(t)]);
                out.push_str(&w.digit[d(u)]);
            }
        },
        _ => return false,
    }
    true
}

/// The weight [`graph_tens`] adds, or `None` where it rejects.
pub fn graph_tens_cost(t: u8, u: u8) -> Option<Cost> {
    match t {
        b'1' => Some(0),
        b'2'..=b'9' => Some(if matches!(u, b'1' | b'8') { TENS_W } else { 0 }),
        _ => None,
    }
}

/// `self.hundreds` on three digits.
pub fn hundreds(x: &[u8], out: &mut String, c: &mut Cost) -> bool {
    let (h, t, u) = (x[0], x[1], x[2]);
    if h == b'0' {
        return false;
    }
    if h == b'1' {
        out.push_str("cento ");
    } else {
        out.push_str(&WORDS.hundreds[d(h)]);
    }
    if t == b'0' && u == b'0' {
        return true;
    }
    if h != b'1' {
        out.push(' ');
    }
    if t == b'0' { digit(u, out) } else { graph_tens(t, u, out, c) }
}

/// `self.two_digit_no_zero`: graph_digit | graph_tens | "0" → " " + digit.
pub fn two_digit_no_zero(x: &[u8], out: &mut String, c: &mut Cost) -> bool {
    match x {
        [u] => digit(*u, out),
        [b'0', u] => {
            out.push(' ');
            digit(*u, out)
        }
        [t, u] => graph_tens(*t, *u, out, c),
        _ => false,
    }
}

/// `graph_hundreds_component_at_least_one_none_zero_digit[_no_one]`.
fn h1(x: &[u8], no_one: bool, out: &mut String, c: &mut Cost) -> bool {
    if x[0] != b'0' {
        return hundreds(x, out, c);
    }
    if x[1] != b'0' {
        return graph_tens(x[1], x[2], out, c);
    }
    if no_one && x[2] == b'1' {
        return false;
    }
    digit(x[2], out)
}

/// `graph_thousands_component_at_least_one_none_zero_digit[_no_one]` on six digits.
fn t1(x: &[u8], no_one: bool, out: &mut String, c: &mut Cost) -> bool {
    let (a, b) = x.split_at(3);
    if a == b"000" {
        return h1(b, no_one, out, c);
    }
    if a == b"001" {
        out.push_str("mille");
    } else {
        if !h1(a, true, out, c) {
            return false;
        }
        out.push_str("mila");
    }
    if b == b"000" {
        return true;
    }
    out.push(' ');
    h1(b, false, out, c)
}

fn big(x: &[u8], sing: &str, plur: &str, out: &mut String, c: &mut Cost) -> bool {
    if x == b"000001" {
        out.push_str("un ");
        out.push_str(sing);
        *c += MILLION_W;
    } else if x != b"000000" {
        if !t1(x, true, out, c) {
            return false;
        }
        out.push(' ');
        out.push_str(plur);
    }
    out.push(' ');
    true
}

thread_local! {
    /// Scratch for the raw (unnormalized-spacing) reading.
    static RAW: std::cell::RefCell<String> = const { std::cell::RefCell::new(String::new()) };
}

/// The weight of [`words_into`]'s reading, without writing it: every reading `readable` admits succeeds; its
/// weight is `TENS_W` per three-digit group (of the 24-digit pad) whose tens digit is 2-9 and units digit 1 or 8
/// (`tens_one`/`tens_eight`), plus `MILLION_W` per million/billion/trillion block equal to `000001`.
pub fn cost_of(digits: &[u8]) -> Option<Cost> {
    if !readable(digits) {
        return None;
    }
    let mut pad = [b'0'; 24];
    pad[24 - digits.len()..].copy_from_slice(digits);
    let mut c = 0;
    for g in pad.chunks_exact(3) {
        if g[1] >= b'2' && matches!(g[2], b'1' | b'8') {
            c += TENS_W;
        }
    }
    for b in pad[..18].chunks_exact(6) {
        if b == b"000001" {
            c += MILLION_W;
        }
    }
    Some(c)
}

/// Whether [`words_into`] may read `digits` (it rejects empty, over-long and zero-led strings up front).
pub fn readable(digits: &[u8]) -> bool {
    digits == b"0" || (!digits.is_empty() && digits.len() <= 24 && digits[0] != b'0')
}

/// `CardinalFst.graph` on a plain digit string: appends the cheapest output to `out` and returns its weight;
/// `None` (and `out` unchanged) when the graph rejects `digits`.
pub fn words_into(digits: &[u8], out: &mut String) -> Option<Cost> {
    if digits == b"0" {
        out.push_str(&WORDS.zero);
        return Some(0);
    }
    if !readable(digits) {
        return None;
    }
    let mut pad = [b'0'; 24];
    pad[24 - digits.len()..].copy_from_slice(digits);
    RAW.with_borrow_mut(|raw| {
        raw.clear();
        let mut c = 0;
        let ok = big(&pad[0..6], "trilione", "trilioni", raw, &mut c)
            && big(&pad[6..12], "miliardo", "miliardi", raw, &mut c)
            && big(&pad[12..18], "milione", "milioni", raw, &mut c)
            && (&pad[18..24] == b"000000" || t1(&pad[18..24], false, raw, &mut c));
        if !ok {
            return None;
        }
        // BOS/EOS delete_space, then runs of 2+ spaces between letters → one space
        let mut first = true;
        for w in raw.split(' ').filter(|w| !w.is_empty()) {
            if !first {
                out.push(' ');
            }
            first = false;
            out.push_str(w);
        }
        Some(c)
    })
}

/// A digit string read from the input (`filter_punctuation` removed the separators).
#[derive(Clone, Copy)]
pub struct Num {
    pub end: usize,
    pub len: usize,
    pub buf: [u8; 24],
}

impl Num {
    pub fn digits(&self) -> &[u8] {
        &self.buf[..self.len]
    }
}

/// `filter_punctuation`: every span at `start` that is a plain digit run or 1-3 digits followed by groups of
/// `[. ]ddd` (at most 24 digits, the cardinal graph's limit).
pub fn spans(text: &[char], start: usize, mut f: impl FnMut(&Num)) {
    let mut n = Num { end: start, len: 0, buf: [0; 24] };
    let mut p = start;
    while p < text.len() && text[p].is_ascii_digit() && n.len < 24 {
        n.buf[n.len] = text[p] as u8;
        n.len += 1;
        p += 1;
        n.end = p;
        f(&n);
    }
    if n.len == 0 || n.len > 3 || (p < text.len() && text[p].is_ascii_digit()) {
        return;
    }
    loop {
        if p + 4 > text.len()
            || !matches!(text[p], '.' | ' ')
            || !text[p + 1..p + 4].iter().all(char::is_ascii_digit)
            || n.len + 3 > 24
        {
            return;
        }
        for k in 0..3 {
            n.buf[n.len + k] = text[p + 1 + k] as u8;
        }
        n.len += 3;
        p += 4;
        n.end = p;
        f(&n);
    }
}

/// The plain digit runs at `start` only (`closure(NEMO_DIGIT)` restrictions).
pub fn plain_spans(text: &[char], start: usize, mut f: impl FnMut(&Num)) {
    let mut n = Num { end: start, len: 0, buf: [0; 24] };
    let mut p = start;
    while p < text.len() && text[p].is_ascii_digit() && n.len < 24 {
        n.buf[n.len] = text[p] as u8;
        n.len += 1;
        p += 1;
        n.end = p;
        f(&n);
    }
}

/// `strip_cardinal_apocope` (es/graph_utils.py): final "un"/"ún" → "uno".
pub fn strip_apocope(s: &str) -> String {
    let mut s = s.to_string();
    strip_apocope_from(&mut s, 0);
    s
}

/// [`strip_apocope`] applied to `s[from..]` in place.
pub fn strip_apocope_from(s: &mut String, from: usize) {
    for suf in ["un", "ún"] {
        if s[from..].ends_with(suf) {
            s.truncate(s.len() - suf.len());
            s.push_str("uno");
            return;
        }
    }
}

/// `shift_cardinal_gender` (es/graph_utils.py) as it acts on Italian cardinals: final "un"/"ún"/"uno" → "una".
/// (Its hundreds rules rewrite the Spanish "-cientos" forms, which no Italian number contains.)
pub fn shift_gender(s: &str) -> String {
    for suf in ["uno", "un", "ún"] {
        if let Some(head) = s.strip_suffix(suf) {
            return format!("{head}una");
        }
    }
    s.to_string()
}

/// An unsigned plain span the time tagger also emits (`hours: "…"`: 0-9, 10-24) whose cardinal reading has no
/// weight (all but "21", `tens_one` at -0.01): the time token has the same span, cost and digits and a lower
/// rank, so the cardinal never wins there.
fn time_shadows(start: usize, n: &Num) -> bool {
    n.end - start == n.len
        && match n.digits() {
            [_] => true,
            [a, b] => *a != b'0' && (*a - b'0') * 10 + (*b - b'0') <= 24 && *b"21" != [*a, *b],
            _ => false,
        }
}

pub struct Cardinal {
    pub cost: Cost,
}

impl Classifier for Cardinal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let neg = text[start] == '-';
        let p = start + neg as usize;
        if !text.get(p).is_some_and(char::is_ascii_digit) {
            return;
        }
        let prefix = if neg { "cardinal { negative: \"true\" integer: \"" } else { "cardinal { integer: \"" };
        spans(text, p, |n| {
            if (!neg && time_shadows(p, n)) || !readable(n.digits()) {
                return;
            }
            let Some(c) = cost_of(n.digits()) else { return };
            let tag = super::tag(n.end, prefix.len() + 14 * n.len + 4, |t| {
                t.push_str(prefix);
                words_into(n.digits(), t);
                t.push_str("\" }");
            });
            out.push(Candidate { end: n.end, cost: self.cost + c, rank: super::sign_rank(neg), tag });
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn w(s: &str) -> String {
        let mut o = String::new();
        words_into(s.as_bytes(), &mut o).unwrap();
        o
    }

    #[test]
    fn numbers() {
        assert_eq!(w("21"), "ventuno");
        assert_eq!(w("100000"), "cento mila");
        assert_eq!(w("1000000"), "un milione");
        assert_eq!(w("5328"), "cinquemila trecento ventotto");
        assert_eq!(w("2100566"), "due milioni cento mila cinquecento sessantasei");
        assert_eq!(w("1000"), "mille");
    }

    #[test]
    fn cost_of_matches_words_into() {
        let mut x: u64 = 0x9e37_79b9_7f4a_7c15;
        let mut cases: Vec<String> = (0..=100_000u32).map(|n| n.to_string()).collect();
        for _ in 0..200_000 {
            x ^= x << 13;
            x ^= x >> 7;
            x ^= x << 17;
            let len = 1 + (x % 24) as usize;
            let digits: String =
                (0..len).map(|i| [b'0', b'1', b'8', b'2', b'5'][((x >> (i * 2 % 60)) % 5) as usize] as char).collect();
            cases.push(digits);
        }
        for s in cases {
            let mut o = String::new();
            assert_eq!(cost_of(s.as_bytes()), words_into(s.as_bytes(), &mut o), "{s}");
        }
    }
}

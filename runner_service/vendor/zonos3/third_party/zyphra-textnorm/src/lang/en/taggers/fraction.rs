//! `en/taggers/fraction.py`.

use crate::lang::common::field;
use crate::lang::en::graph_utils::{Outs, row_index};
use crate::lang::en::lazy::{Reading, building};
use crate::lang::en::taggers::cardinal::{digit_comma_end, graph};
use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_digit, match_str};

crate::tsv!(FRACTION, "en/number/fraction.tsv");

/// One `FractionFst.graph` reading: the integer part `int` (char span into the text; empty when absent), the
/// numerator and denominator spans, into the text or, with `glyph`, into that `fraction.tsv` row's value.
#[derive(Clone, Copy, Debug, Default)]
pub struct FracR {
    int: (u32, u32),
    num: (u32, u32),
    den: (u32, u32),
    glyph: Option<u16>,
}

fn named(src: &[char], (s, e): (u32, u32)) -> String {
    let buf: String = src[s as usize..e as usize].iter().collect();
    graph(&buf).unwrap_or_default()
}

/// Appends the fields of reading `r` to `s` (`integer_part: "…" numerator: "…" denominator: "…"`).
pub fn write_into(s: &mut String, text: &[char], r: FracR) {
    if r.int.0 < r.int.1 {
        field(s, "integer_part", &named(text, r.int));
        s.push(' ');
    }
    let glyph: Vec<char>;
    let src = match r.glyph {
        Some(g) => {
            glyph = FRACTION.rows()[g as usize].value.chars().collect();
            &glyph[..]
        }
        None => text,
    };
    field(s, "numerator", &named(src, r.num));
    s.push(' ');
    field(s, "denominator", &named(src, r.den));
}

/// [`write_into`] a new string while tags are built, else empty.
fn text_of(text: &[char], r: FracR) -> String {
    let mut s = String::new();
    if building() {
        write_into(&mut s, text, r);
    }
    s
}

/// End of the digit/comma run at `start` if `cardinal.graph` accepts it (followed by "/", " ", an ending or the
/// token end: only the maximal run can be on a path).
fn cardinal(text: &[char], start: usize) -> Option<usize> {
    if start >= text.len() || !is_digit(text[start]) {
        return None;
    }
    let end = digit_comma_end(text, start);
    // `graph` accepts at most 15 digits (plus commas): longer runs are rejected without building a string
    let mut buf = [0u8; 32];
    if end - start > buf.len() {
        return None;
    }
    for (b, &c) in buf.iter_mut().zip(&text[start..end]) {
        *b = c as u8;
    }
    graph(std::str::from_utf8(&buf[..end - start]).ok()?).map(|_| end)
}

/// `numerator + denominator` on chars `text` from `start`: `f(end, numerator span, denominator span)`.
fn num_den(text: &[char], start: usize, out: &mut dyn FnMut(usize, (u32, u32), (u32, u32))) {
    if !pair_or_glyph_at(text, start) {
        return;
    }
    let Some(ne) = cardinal(text, start) else { return };
    for sep in ["/", " / "] {
        let Some(l) = match_str(text, ne, sep) else { continue };
        let Some(de) = cardinal(text, ne + l) else { continue };
        let (num, den) = ((start as u32, ne as u32), ((ne + l) as u32, de as u32));
        out(de, num, den);
        for end in ["rd", "th", "st", "nd", "RD", "TH", "ST", "ND"] {
            if let Some(k) = match_str(text, de, end) {
                out(de + k, num, den);
            }
        }
    }
}

/// Whether a numerator/denominator pair (`d+ "/" d` or `d+ " / " d`) or a `fraction.tsv` glyph starts at `p`.
fn pair_or_glyph_at(text: &[char], p: usize) -> bool {
    let Some(&c) = text.get(p) else { return false };
    if !is_digit(c) {
        // every fraction.tsv key is a single non-ASCII glyph
        return !c.is_ascii() && FRACTION.prefix_matches(text, p).next().is_some();
    }
    let e = digit_comma_end(text, p);
    ["/", " / "]
        .iter()
        .any(|sep| match_str(text, e, sep).is_some_and(|l| text.get(e + l).is_some_and(|&c| is_digit(c))))
}

/// `fraction.tsv` glyphs at `p` (whose values are whole `numerator + denominator` pairs): `(end, reading)`.
fn glyphs(text: &[char], p: usize, int: (u32, u32), out: &mut Outs<FracR>) {
    for (row, e) in FRACTION.prefix_matches(text, p) {
        let v: Vec<char> = row.value.chars().collect();
        let g = Some(row_index(&FRACTION, row));
        num_den(&v, 0, &mut |ve, num, den| {
            if ve == v.len() {
                let r = FracR { int, num, den, glyph: g };
                out.add_r(e, 0, text_of(text, r), r);
            }
        });
    }
}

/// `FractionFst.graph` on every span from `start` (no weights inside).
pub fn fraction_spans(text: &[char], start: usize, out: &mut Outs<FracR>) {
    // the fraction proper starts at `start`, after the integer, or after the integer and one space
    let ie = if start < text.len() && is_digit(text[start]) { digit_comma_end(text, start) } else { start };
    if !(pair_or_glyph_at(text, start)
        || pair_or_glyph_at(text, ie)
        || (text.get(ie) == Some(&' ') && pair_or_glyph_at(text, ie + 1)))
    {
        return;
    }
    let tails = |p: usize, int: (u32, u32), out: &mut Outs<FracR>| {
        num_den(text, p, &mut |e, num, den| {
            let r = FracR { int, num, den, glyph: None };
            out.add_r(e, 0, text_of(text, r), r);
        });
        glyphs(text, p, int, out);
    };
    tails(start, (0, 0), out);
    // an integer part is only used when a pair or glyph follows it (directly or after one space)
    let int_used = start < text.len()
        && is_digit(text[start])
        && (FRACTION.prefix_matches(text, ie).next().is_some()
            || (text.get(ie) == Some(&' ') && pair_or_glyph_at(text, ie + 1)));
    if let Some(ie) = cardinal(text, start).filter(|_| int_used) {
        let int = (start as u32, ie as u32);
        if text.get(ie) == Some(&' ') {
            // integer + " " + (numerator + denominator) or fraction.tsv
            tails(ie + 1, int, out);
        } else {
            // integer + insert(" ") + fraction.tsv only
            glyphs(text, ie, int, out);
        }
    }
}

pub struct Fraction;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 6;

fn tag(text: &[char], r: FracR) -> String {
    let mut s = String::with_capacity(96);
    s.push_str("fraction { ");
    write_into(&mut s, text, r);
    s.push_str(" }");
    s
}

impl Classifier for Fraction {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !is_digit(text[start]) && FRACTION.prefix_matches(text, start).next().is_none() {
            return;
        }
        let mut o = Outs::default();
        fraction_spans(text, start, &mut o);
        for (&(e, c, _), r) in o.entries() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            let cost = cost(1.1) + c;
            crate::lang::en::lazy::record(MEMBER, start, e, cost, 0, Reading::Fraction(r));
            let tag = if building() { tag(text, r) } else { String::new() };
            out.push(Candidate { end: e, cost, rank: 0, tag });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Fraction(r) => Some(tag(text, r)),
            _ => None,
        })
    }
}

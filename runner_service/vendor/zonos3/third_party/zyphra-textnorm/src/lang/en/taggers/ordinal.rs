//! `en/taggers/ordinal.py`.

use crate::lang::common::field;
use crate::lang::en::lazy::Reading;
use crate::lang::en::taggers::cardinal::{digit_comma_end, graph};
use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_digit, match_str};

const SUFFIXES: [(&str, u8); 12] = [
    ("st", 1),
    ("ST", 1),
    ("ˢᵗ", 1),
    ("nd", 2),
    ("ND", 2),
    ("ⁿᵈ", 2),
    ("rd", 3),
    ("RD", 3),
    ("ʳᵈ", 3),
    ("th", 0),
    ("TH", 0),
    ("ᵗʰ", 0),
];

/// Whether the number part `n` (digits/commas, ending in a digit) takes suffix class `k` (1, 2, 3 or 0 = th).
fn suffix_ok(n: &[char], k: u8) -> bool {
    let last = n[n.len() - 1];
    let prev = if n.len() >= 2 { Some(n[n.len() - 2]) } else { None };
    match k {
        1 | 2 | 3 => last == (b'0' + k) as char && prev.is_none_or(|p| is_digit(p) && p != '1'),
        _ => match prev {
            Some('1') => true,
            Some(p) if is_digit(p) => !matches!(last, '1' | '2' | '3'),
            Some(_) => false,
            None => !matches!(last, '1' | '2' | '3'),
        },
    }
}

/// `OrdinalFst.graph` on every span from `start`: `(end, cardinal words)`. A suffix starts with a letter, so the
/// number part is the whole digit/comma run (a shorter prefix is followed by a digit or a comma).
pub fn ordinal_spans(text: &[char], start: usize, f: &mut dyn FnMut(usize, String)) {
    let e = digit_comma_end(text, start);
    if e == start || !is_digit(text[e - 1]) || e == text.len() {
        return;
    }
    if !matches!(text[e], 's' | 'S' | 'n' | 'N' | 'r' | 'R' | 't' | 'T' | 'ˢ' | 'ⁿ' | 'ʳ' | 'ᵗ') {
        return;
    }
    let n = &text[start..e];
    for (suf, k) in SUFFIXES {
        let Some(l) = match_str(text, e, suf) else { continue };
        if !suffix_ok(n, k) {
            continue;
        }
        let buf: String = n.iter().collect();
        if let Some(w) = graph(&buf) {
            f(e + l, w);
        }
    }
}

pub struct Ordinal;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 8;

/// The tag of an ordinal whose number part is `text[start..e]` (the reading: `e`).
fn tag(text: &[char], start: usize, e: usize) -> String {
    let buf: String = text[start..e].iter().collect();
    let mut tag = String::with_capacity(48);
    tag.push_str("ordinal { ");
    field(&mut tag, "integer", &graph(&buf).unwrap_or_default());
    tag.push_str(" }");
    tag
}

impl Classifier for Ordinal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !is_digit(text[start]) {
            return;
        }
        let build = crate::lang::en::lazy::building();
        let ne = digit_comma_end(text, start);
        ordinal_spans(text, start, &mut |e, w| {
            if !crate::lang::en::lazy::keep_end(text, e) {
                return;
            }
            let mut tag = String::new();
            if build {
                tag.push_str("ordinal { ");
                field(&mut tag, "integer", &w);
                tag.push_str(" }");
            }
            crate::lang::en::lazy::record(MEMBER, start, e, cost(1.1), 0, Reading::Ordinal(ne as u32));
            out.push(Candidate { end: e, cost: cost(1.1), rank: 0, tag });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Ordinal(ne) => Some(tag(text, start, ne as usize)),
            _ => None,
        })
    }
}

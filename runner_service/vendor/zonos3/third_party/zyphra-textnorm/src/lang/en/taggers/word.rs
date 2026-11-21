//! `en/taggers/word.py` (deterministic).

use std::sync::LazyLock;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Table, cost, is_alpha, is_digit, is_nemo_space};

crate::tsv!(IPA_SYMBOLS, "en/whitelist/ipa_symbols.tsv");

/// Punctuation marks of `PunctuationFst` (weight irrelevant here).
pub static MARKS: LazyLock<Punctuation> = LazyLock::new(|| Punctuation::en(0));

fn excluded(c: char) -> bool {
    matches!(c, '$' | '€' | '₩' | '£' | '¥' | '#' | '%') || is_digit(c)
}

/// `phoneme`: `[` (alpha+ digit* " ")* alpha+ digit* `]`.
fn phoneme_end(text: &[char], start: usize) -> Option<usize> {
    if text.get(start) != Some(&'[') {
        return None;
    }
    let mut i = start + 1;
    loop {
        let a = i;
        while i < text.len() && is_alpha(text[i]) {
            i += 1;
        }
        if i == a {
            return None;
        }
        while i < text.len() && is_digit(text[i]) {
            i += 1;
        }
        match text.get(i) {
            Some(' ') => i += 1,
            Some(']') => return Some(i + 1),
            _ => return None,
        }
    }
}

/// All ends of `ipa_phonemes` (`[` stress? unit+ (stress|unit)* (delim+ ipa)* delim? `]`).
fn ipa_ends(text: &[char], start: usize, out: &mut Vec<usize>) {
    if text.get(start) != Some(&'[') {
        return;
    }
    let units: &Table = &IPA_SYMBOLS;
    let stress = |c: char| matches!(c, 'ˈ' | '\'' | 'ˌ');
    // NFA over positions with states: 0 = need (stress?) unit, 1 = in phoneme (after unit), 2 = in delim
    let n = text.len();
    let mut seen = std::collections::HashSet::new();
    let mut stack = vec![(start + 1, 0u8, false)];
    while let Some((i, st, stressed)) = stack.pop() {
        if !seen.insert((i, st, stressed)) {
            continue;
        }
        if i >= n {
            continue;
        }
        let c = text[i];
        match st {
            0 => {
                if stress(c) && !stressed {
                    stack.push((i + 1, 0, true));
                }
                for (_, e) in units.prefix_matches(text, i) {
                    if e > i {
                        stack.push((e, 1, false));
                    }
                }
            }
            1 => {
                if c == ']' {
                    out.push(i + 1);
                }
                if stress(c) {
                    stack.push((i + 1, 1, false));
                }
                for (_, e) in units.prefix_matches(text, i) {
                    if e > i {
                        stack.push((e, 1, false));
                    }
                }
                if c == ' ' || MARKS.is_mark(c) {
                    stack.push((i + 1, 2, false));
                }
            }
            _ => {
                if c == ']' {
                    out.push(i + 1);
                }
                if c == ' ' || MARKS.is_mark(c) {
                    stack.push((i + 1, 2, false));
                }
                if stress(c) {
                    stack.push((i + 1, 0, true));
                }
                for (_, e) in units.prefix_matches(text, i) {
                    if e > i {
                        stack.push((e, 1, false));
                    }
                }
            }
        }
    }
}

/// `WordFst.graph` on every span from `start`: `(end, cost)` (weight 100 not included).
pub fn word_spans(text: &[char], start: usize, mut f: impl FnMut(usize, Cost, &[char])) {
    let mut ph = Vec::new();
    if text[start] == '[' {
        if let Some(e) = phoneme_end(text, start) {
            ph.push(e);
        }
        ipa_ends(text, start, &mut ph);
        ph.sort_unstable();
        ph.dedup();
    }
    let is_mark = |c: char| if c.is_ascii() { crate::lang::en::boundary(c) } else { MARKS.is_mark(c) };
    let mut no_excl = true;
    let mut no_punct = true;
    let mut e = start;
    while e < text.len() && !is_nemo_space(text[e]) {
        no_excl &= !excluded(text[e]);
        no_punct &= !is_mark(text[e]);
        if !no_excl && !no_punct && ph.is_empty() {
            break;
        }
        e += 1;
        if ph.contains(&e) {
            continue;
        }
        if no_excl {
            f(e, cost(-0.0001), &text[start..e]);
        } else if no_punct {
            f(e, 0, &text[start..e]);
        }
    }
    for &p in &ph {
        f(p, 0, &text[start..p]);
    }
}

/// `WordFst`: candidates carry no tag; the winner's `name: "<span>"` is written by `materialize` (the tag is the span
/// itself, spaces of phoneme spans as NBSP).
pub struct Word;

impl Classifier for Word {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        word_spans(text, start, |end, c, _| {
            if crate::lang::en::end_ok(text, end) {
                out.push(Candidate { end, cost: cost(100.0) + c, rank: 0, tag: String::new() });
            }
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let s = &text[start..candidate.end];
        let mut tag = String::with_capacity(s.len() + 10);
        tag.push_str("name: \"");
        tag.extend(s.iter().map(|&c| if c == ' ' { '\u{a0}' } else { c }));
        tag.push('"');
        candidate.tag = tag;
    }
}

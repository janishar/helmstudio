//! Common character-level normalization and prefix utilities for Arabic.

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_nemo_space};

#[inline(always)]
pub fn is_digit(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '٠'..='٩' | '۰'..='۹')
}

#[inline(always)]
pub fn char_at(text: &[char], i: usize) -> char {
    let c = text[i];
    match c {
        '٠'..='٩' => (b'0' + (c as u32 - '٠' as u32) as u8) as char,
        '۰'..='۹' => (b'0' + (c as u32 - '۰' as u32) as u8) as char,
        '٫' => {
            if i > 0 && is_digit(text[i - 1]) && i + 1 < text.len() && is_digit(text[i + 1]) {
                '.'
            } else {
                '٫'
            }
        }
        '٬' => {
            if i > 0 && is_digit(text[i - 1]) && i + 1 < text.len() && is_digit(text[i + 1]) {
                ','
            } else {
                '٬'
            }
        }
        _ => c,
    }
}

fn prefix_mods(text: &[char], start: usize) -> impl Iterator<Item = (&'static str, usize)> + '_ {
    let double = (text.get(start) == Some(&'§') && text.get(start + 1) == Some(&'§')).then_some(("الفقرات", 2));
    let single = match text.get(start) {
        Some('~' | '≈') => Some(("حوالي", 1)),
        Some('±') => Some(("زائد\u{a0}أو\u{a0}ناقص", 1)),
        Some('©') => Some(("حقوق\u{a0}النشر", 1)),
        Some('§') => Some(("الفقرة", 1)),
        _ => None,
    };
    [double, single].into_iter().flatten().flat_map(move |(spoken, len)| {
        [Some((spoken, len)), (text.get(start + len) == Some(&' ')).then_some((spoken, len + 1))].into_iter().flatten()
    })
}

#[inline(always)]
pub fn boundary(c: char, punct: &Punctuation) -> bool {
    is_nemo_space(c) || punct.is_mark(c)
}

#[inline(always)]
pub fn token_start(text: &[char], start: usize, punct: &Punctuation) -> bool {
    start == 0 || boundary(text[start - 1], punct)
}

#[inline(always)]
pub fn token_end(text: &[char], end: usize, punct: &Punctuation) -> bool {
    end == text.len() || boundary(text[end], punct)
}

/// A wrapper around a semiotic classifier that also matches the Arabic `prefix_mod`
/// (`~`, `≈`, `±`, `©`, `§§`, `§`) before the semiotic token with weight -1.0.
pub struct Prefixed<C>(pub C);

thread_local! {
    static PREFIX_SCRATCH: std::cell::Cell<Vec<Candidate>> = const { std::cell::Cell::new(Vec::new()) };
}

impl<C: Classifier> Classifier for Prefixed<C> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        self.0.candidates(text, start, out);
        if !matches!(text.get(start), Some('~' | '≈' | '±' | '©' | '§')) {
            return;
        }
        for (_, skip) in prefix_mods(text, start) {
            let core_start = start + skip;
            if core_start < text.len() {
                let initial = out.len();
                self.0.candidates(&text[core_start..], 0, out);
                for c in &mut out[initial..] {
                    c.end += core_start;
                    c.cost += cost(-1.0);
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !matches!(text.get(start), Some('~' | '≈' | '±' | '©' | '§')) {
            self.0.materialize(text, start, candidate);
            return;
        }
        // Reproduce the first matching witness without encoding path identity
        // in rank: rank is a semantic tie-break, not metadata storage.
        let mut scratch = PREFIX_SCRATCH.take();
        scratch.clear();
        self.0.candidates(text, start, &mut scratch);
        let mut matched = false;
        if let Some(core) =
            scratch.iter_mut().find(|c| c.end == candidate.end && c.cost == candidate.cost && c.rank == candidate.rank)
        {
            self.0.materialize(text, start, core);
            candidate.tag = std::mem::take(&mut core.tag);
            matched = true;
        } else {
            for (spoken, skip) in prefix_mods(text, start) {
                let core_start = start + skip;
                if core_start == text.len() {
                    continue;
                }
                scratch.clear();
                self.0.candidates(&text[core_start..], 0, &mut scratch);
                if let Some(core) = scratch.iter_mut().find(|c| {
                    c.end + core_start == candidate.end
                        && c.cost + cost(-1.0) == candidate.cost
                        && c.rank == candidate.rank
                }) {
                    self.0.materialize(&text[core_start..], 0, core);
                    candidate.tag = format!("name: \"{spoken}\" }} tokens {{ {}", core.tag);
                    matched = true;
                    break;
                }
            }
        }
        scratch.clear();
        PREFIX_SCRATCH.set(scratch);
        assert!(matched, "recognized Arabic modifier path must materialize");
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese word classifier: non-space non-excluded symbols, with alpha bonus (-1.0).

use super::super::graph_utils::WEIGHT_WORD;
use crate::lattice::{Candidate, Classifier};
use crate::text::{cost, is_nemo_space};

pub struct Word;

#[inline]
pub fn is_word_symbol_to_exclude(c: char) -> bool {
    if c.is_ascii_digit() {
        return true;
    }
    matches!(
        c,
        '°' | '′'
            | '″'
            | '℃'
            | '℉'
            | '$'
            | '€'
            | '₩'
            | '£'
            | '¥'
            | '₹'
            | '₨'
            | '₽'
            | '₺'
            | '₪'
            | '₴'
            | '₫'
            | '৳'
            | '₦'
            | '₱'
            | '฿'
            | '₵'
            | '֏'
            | '¢'
            | '₣'
            | '#'
            | '%'
            | '‰'
            | '~'
            | '±'
            | '&'
            | '©'
            | '§'
            | '®'
            | '™'
            | '^'
            | 'Ω'
            | 'Ω'
            | '²'
            | '³'
            | '½'
            | '¼'
            | '¾'
            | '⅓'
            | '⅔'
            | '⅕'
            | '⅖'
            | '⅗'
            | '⅘'
            | '⅙'
            | '⅚'
            | '⅛'
            | '⅜'
            | '⅝'
            | '⅞'
            | '⅐'
            | '⅑'
            | '⅒'
    )
}

#[inline]
pub fn is_word_char(c: char) -> bool {
    !is_nemo_space(c) && !is_word_symbol_to_exclude(c)
}

impl Classifier for Word {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() || !is_word_char(text[start]) {
            return;
        }

        let mut end = start;
        let mut all_alpha = true;
        while end < text.len() && is_word_char(text[end]) {
            all_alpha &= text[end].is_ascii_alphabetic();
            end += 1;
            let final_cost = if all_alpha { WEIGHT_WORD + cost(-1.0) } else { WEIGHT_WORD };
            out.push(Candidate { end, cost: final_cost, rank: 0, tag: String::new() });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let mut tag = String::with_capacity(candidate.end - start + 8);
        tag.push_str("name: \"");
        tag.extend(&text[start..candidate.end]);
        tag.push('"');
        candidate.tag = tag;
    }
}

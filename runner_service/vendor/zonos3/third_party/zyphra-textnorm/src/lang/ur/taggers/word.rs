//! Word tagger for Urdu text normalization.

use super::Sink;
use super::punctuation::{URDU_PUNCTUATION_MARKS, is_single_mark};
use crate::lang::ur::graph_utils::{MIN_NEG_WEIGHT, is_urdu_char};
use crate::text::{cost, is_nemo_space, is_unicode_punct};

pub fn is_open_mark(c: char) -> bool {
    matches!(c, '"' | '\'' | '(' | '«' | '“' | '‘')
}

pub fn is_close_mark(c: char) -> bool {
    matches!(c, '"' | '\'' | ')' | '»' | '”' | '’')
}

pub fn is_excluded_symbol(c: char) -> bool {
    matches!(
        c,
        '$' | '€'
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
            | '֏'
            | '¢'
            | '#'
            | '%'
            | '٪'
            | '‰'
            | '~'
            | '≈'
            | '±'
            | '©'
            | '®'
            | '™'
            | '§'
    )
}

pub fn classify(input: &str, end: usize, out: &mut Sink) {
    if input.is_empty() {
        return;
    }
    let first = input.chars().next().unwrap();
    let last = input.chars().last().unwrap();
    // The Urdu ranges in graph_utils exclude every Unicode punctuation range,
    // singleton mark, and explicit Urdu punctuation character.
    let punct =
        |c: char| !is_urdu_char(c) && (is_single_mark(c) || is_unicode_punct(c) || URDU_PUNCTUATION_MARKS.contains(c));
    let mut best = None;
    for open in 0..=usize::from(is_open_mark(first)) {
        for close in 0..=usize::from(is_close_mark(last)) {
            let start = if open == 1 { first.len_utf8() } else { 0 };
            let end = input.len() - if close == 1 { last.len_utf8() } else { 0 };
            if start >= end {
                continue;
            }
            let core = &input[start..end];
            if punct(core.chars().next().unwrap()) || punct(core.chars().last().unwrap()) {
                continue;
            }
            let mut valid = true;
            let mut in_punct = false;
            let mut single = false;
            let mut urdu_count = 0;
            for c in core.chars() {
                if is_urdu_char(c) {
                    urdu_count += 1;
                    in_punct = false;
                    continue;
                }
                if is_nemo_space(c) {
                    valid = false;
                    break;
                }
                if punct(c) {
                    if in_punct && (single || is_single_mark(c)) {
                        valid = false;
                        break;
                    }
                    in_punct = true;
                    single = is_single_mark(c);
                } else {
                    if is_excluded_symbol(c) {
                        valid = false;
                        break;
                    }
                    in_punct = false;
                }
            }
            if valid {
                let weight = cost(100.0) + MIN_NEG_WEIGHT * urdu_count;
                best = Some(best.map_or(weight, |old: i64| old.min(weight)));
            }
        }
    }
    if let Some(weight) = best {
        out.push(end, weight, 13, || format!("name: \"{}\"", input.replace(' ', "\u{a0}")));
    }
}

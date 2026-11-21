//! `pt/taggers/decimal.py`. The three branches accept disjoint inputs, so each span has one output.

use std::sync::LazyLock;

use super::cardinal::{self, NUM};
use crate::lang::pt::graph_utils::{SpanTagger, is_numeric_word, words_while};
use crate::text::{Cost, is_digit, is_nemo_space};

crate::tsv!(QUANTITY, "pt/numbers/quantity_words.tsv");

static FRAC_WORDS: LazyLock<[String; 10]> = LazyLock::new(|| {
    let mut w: [String; 10] = Default::default();
    w[0] = NUM.zero.clone();
    for d in 1..10 {
        w[d] = NUM.digit[d].clone();
    }
    w
});

/// `quantity_words.tsv` (9 one-column rows).
fn quantity(s: &[char]) -> Option<&'static str> {
    let rows: &'static [crate::text::Row] = QUANTITY.rows();
    rows.iter().find(|r| r.key.chars().eq(s.iter().copied())).map(|r| r.value.as_str())
}

/// `decimal_core` (`<int>,<frac>`): appends its fields to `out` (nothing on rejection).
fn core(s: &[char], out: &mut String) -> Option<Cost> {
    let comma = s.iter().position(|&c| c == ',')?;
    let (int, frac) = (&s[..comma], &s[comma + 1..]);
    if frac.is_empty() || frac.len() > 15 || !frac.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let (words, c) = if int == ['0'] {
        (None, 0)
    } else {
        if int.is_empty() || int.len() > 12 || int[0] == '0' {
            return None;
        }
        let (w, wc) = cardinal::graph_digits(int)?;
        (Some(w), wc)
    };
    out.push_str("integer_part: \"");
    out.push_str(words.as_deref().unwrap_or("zero"));
    out.push_str("\"  fractional_part: \"");
    for (i, &d) in frac.iter().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        out.push_str(&FRAC_WORDS[(d as u8 - b'0') as usize]);
    }
    out.push('"');
    Some(c)
}

/// `final_graph_wo_negative`: decimal_core | integer_quantity | decimal_quantity.
pub fn body_wo_negative(s: &[char]) -> Option<(Cost, String)> {
    let mut out = String::new();
    if let Some(c) = core(s, &mut out) {
        return Some((c, out));
    }
    // <number> delete_space <quantity word>: the number ends at the last digit before the word
    let q = s.iter().rposition(|&c| is_digit(c))? + 1;
    let mut k = q;
    while k < s.len() && is_nemo_space(s[k]) {
        k += 1;
    }
    let qw = quantity(&s[k..])?;
    let num = &s[..q];
    out.clear();
    let c = if let Some(c) = core(num, &mut out) {
        c
    } else {
        if num.len() > 12 {
            return None;
        }
        let (w, c) = cardinal::graph_digits(num)?;
        out.push_str("integer_part: \"");
        out.push_str(&w);
        out.push_str("\" ");
        c
    };
    out.push_str("quantity: \"");
    out.push_str(qw);
    out.push('"');
    Some((c, out))
}

/// `DecimalFst.fst` body (with the optional minus).
pub fn body(s: &[char]) -> Option<(Cost, String)> {
    match s.first() {
        Some('-') => {
            let (c, b) = body_wo_negative(&s[1..])?;
            Some((c, format!("negative: \"true\" {b}")))
        }
        _ => body_wo_negative(s),
    }
}

pub fn tag(s: &[char]) -> Option<(Cost, String)> {
    let (c, b) = body(s)?;
    Some((c, format!("decimal {{ {b} }}")))
}

pub struct Decimal;

impl SpanTagger for Decimal {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) || c == '-' { 2 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        if self.max_words(text[start]) == 0 { 0 } else { 1 + words_while(text, start, 1, is_numeric_word) }
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        tag(span)
    }
}

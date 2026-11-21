//! `pt/taggers/fraction.py`.

use std::sync::LazyLock;

use super::cardinal;
use super::ordinal::{ORD_DIGIT, ORD_HUNDREDS};
use crate::lang::pt::graph_utils::{SpanTagger, is_numeric_word, words_while};
use crate::text::{Cost, Table, is_digit, is_nemo_space};

crate::labels!(ORD_EXCEPTIONS, "pt/fractions/ordinal_exceptions.tsv");
crate::labels!(POWERS_OF_TEN, "pt/fractions/powers_of_ten.tsv");

static EXCEPTIONS: LazyLock<Table> = LazyLock::new(|| {
    Table::from_pairs(ORD_EXCEPTIONS.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone())))
});
static POWERS: LazyLock<Table> = LazyLock::new(|| {
    Table::from_pairs(POWERS_OF_TEN.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone())))
});

fn map1(t: &Table, k: &str) -> Option<String> {
    t.exact(k).next().map(|r| r.value.clone())
}

/// `cdrewrite(ordinal_exceptions, "", "", Σ*)` on a single ordinal word (the keys are whole words).
fn exceptions(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut i = 0;
    'outer: while i < s.len() {
        for r in EXCEPTIONS.rows() {
            if s[i..].starts_with(r.key.as_str()) && !r.key.is_empty() {
                out.push_str(&r.value);
                i += r.key.len();
                continue 'outer;
            }
        }
        let c = s[i..].chars().next().unwrap();
        out.push(c);
        i += c.len_utf8();
    }
    out
}

/// `denominator`: `(cost, field text)` for a digit span.
fn denominator(d: &[char], out: &mut String) -> Option<Cost> {
    if d.is_empty() || !d.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let key: String = d.iter().collect();
    let two_to_ten = matches!(key.as_str(), "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" | "10");
    let (words, c) = cardinal::graph_digits(d)?;
    let (value, feat) = if two_to_ten {
        (exceptions(&map1(&ORD_DIGIT, &words)?), "ordinal")
    } else if key == "100" {
        (map1(&ORD_HUNDREDS, &words)?, "ordinal")
    } else if key == "1000" {
        (map1(&POWERS, &words)?, "ordinal")
    } else {
        (words, "avos")
    };
    out.push_str("denominator: \"");
    out.push_str(&value);
    out.push_str("\" morphosyntactic_features: \"");
    out.push_str(feat);
    out.push('"');
    Some(c)
}

/// `numerator + denominator`.
fn core(s: &[char], out: &mut String) -> Option<Cost> {
    let slash = s.iter().position(|&c| matches!(c, '/' | '\u{2044}' | '\u{2215}'))?;
    let mut num_end = slash;
    let mut den_start = slash + 1;
    if slash > 0 && s[slash - 1] == ' ' && s.get(slash + 1) == Some(&' ') {
        // " / " variant; the plain variant cannot match (cardinal.graph has no trailing space)
        num_end = slash - 1;
        den_start = slash + 2;
    }
    let den = &s[den_start..];
    if den.is_empty() || !den.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let (nw, nc) = cardinal::graph(&s[..num_end])?;
    let mark = out.len();
    out.push_str("numerator: \"");
    out.push_str(&nw);
    out.push_str("\" ");
    match denominator(&s[den_start..], out) {
        Some(dc) => Some(nc + dc),
        None => {
            out.truncate(mark);
            None
        }
    }
}

pub fn body(s: &[char]) -> Option<(Cost, String)> {
    let mut out = String::new();
    let s = match s.first() {
        Some('-') => {
            out.push_str("negative: \"true\" ");
            &s[1..]
        }
        _ => s,
    };
    let mark = out.len();
    if let Some(c) = core(s, &mut out) {
        return Some((c, out));
    }
    // mixed: integer_part WS+ fraction_core (the integer may itself hold space separators: try every run)
    let mut ws = 0;
    while ws < s.len() {
        if !is_nemo_space(s[ws]) {
            ws += 1;
            continue;
        }
        let mut f = ws;
        while f < s.len() && is_nemo_space(s[f]) {
            f += 1;
        }
        if let Some((iw, ic)) = cardinal::graph(&s[..ws]) {
            out.truncate(mark);
            out.push_str("integer_part: \"");
            out.push_str(&iw);
            out.push_str("\" ");
            // `closure(NEMO_WHITE_SPACE, 1)` is accepted, not deleted: it stays in the tag
            out.extend(&s[ws..f]);
            if let Some(c) = core(&s[f..], &mut out) {
                return Some((ic + c, out));
            }
        }
        ws = f;
    }
    None
}

pub fn tag(s: &[char]) -> Option<(Cost, String)> {
    // every branch holds a slash and ends in a denominator digit
    if !s.last().is_some_and(|c| is_digit(*c)) || !s.iter().any(|&c| matches!(c, '/' | '\u{2044}' | '\u{2215}')) {
        return None;
    }
    let (c, b) = body(s)?;
    Some((c, format!("fraction {{ {b} }}")))
}

pub struct Fraction;

impl SpanTagger for Fraction {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) || c == '-' { 20 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        if self.max_words(text[start]) == 0 { 0 } else { words_while(text, start, 20, is_numeric_word) }
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        tag(span)
    }
}

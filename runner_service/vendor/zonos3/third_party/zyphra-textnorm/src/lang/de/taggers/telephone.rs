//! `de/taggers/telephone.py`: `country_code " " number_part`.

use super::cardinal::{W, WEIGHT, digit_end, two_digit};
use crate::lang::de::{Sink, Spans};

fn singles(d: &[char]) -> String {
    let mut s = String::with_capacity(d.len() * 6);
    for (i, &c) in d.iter().enumerate() {
        if i > 0 {
            s.push(' ');
        }
        s.push_str(W.single(c as u8 - b'0'));
    }
    s
}

/// One country code reading: `+`? and two digits at the given position, `+`? and a lone `0`, or the `0`-led digit
/// run `text[a..b]`.
#[derive(Clone, Copy)]
enum Cc {
    Two(bool, usize),
    Zero(bool),
    Run(usize, usize),
}

impl Cc {
    fn words(self, text: &[char]) -> String {
        let plus = |p: bool| if p { "plus " } else { "" };
        match self {
            Cc::Two(p, at) => format!("{}{}", plus(p), two_digit(&text[at..at + 2]).expect("country code").0),
            Cc::Zero(p) => format!("{}{}", plus(p), W.zero),
            Cc::Run(a, b) => singles(&text[a..b]),
        }
    }
}

/// Country code spans: `f(end, reading)`.
fn country(text: &[char], s: usize, f: &mut dyn FnMut(usize, Cc)) {
    // ("+" → "plus ")? (two digits @ two_digit_non_zero | "0")
    let plus = text.get(s) == Some(&'+');
    let p = s + plus as usize;
    if let Some(d) = text.get(p..p + 2) {
        if d.iter().all(char::is_ascii_digit) && d[0] != '0' {
            f(p + 2, Cc::Two(plus, p));
        }
    }
    if text.get(p) == Some(&'0') {
        f(p + 1, Cc::Zero(plus));
    }
    if plus {
        return;
    }
    // "(" 0 digits+ ")" and 0 digits+
    let (q, paren) = if text.get(s) == Some(&'(') { (s + 1, true) } else { (s, false) };
    if text.get(q) != Some(&'0') {
        return;
    }
    let e = digit_end(text, q);
    for k in q + 2..=e {
        if paren {
            if k == e && text.get(e) == Some(&')') {
                f(e + 1, Cc::Run(q, k));
            }
        } else {
            f(k, Cc::Run(q, k));
        }
    }
}

pub struct Telephone;

impl Spans for Telephone {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit() || c == '+' || c == '('
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        // a country code needs a digit at `start` or after `+`/`(`
        let first_digit = if matches!(text[start], '+' | '(') { start + 1 } else { start };
        if !text.get(first_digit).is_some_and(char::is_ascii_digit) {
            return;
        }
        country(text, start, &mut |ce, cw| {
            if text.get(ce) != Some(&' ') {
                return;
            }
            // number_part: digits ((-|" ") digits)?, at least 7 chars
            let a = ce + 1;
            let ae = digit_end(text, a);
            let tag = |words: String| {
                format!(
                    "telephone {{ country_code: \"{}\" number_part: \"{words}\" preserve_order: true }}",
                    cw.words(text)
                )
            };
            for k in a + 7..=ae {
                out.push(k, WEIGHT, || tag(singles(&text[a..k])));
            }
            if ae > a && matches!(text.get(ae), Some('-' | ' ')) {
                let b = ae + 1;
                let be = digit_end(text, b);
                for k in (b + 1).max(a + 7)..=be {
                    out.push(k, WEIGHT, || tag(format!("{} {}", singles(&text[a..ae]), singles(&text[b..k]))));
                }
            }
        });
    }
}

//! `ja/taggers/fraction.py`.
//!
//! `num = cardinal | √cardinal | decimal_number | √decimal_number` with `decimal_number = cardinal + ("." → 点) +
//! digits*` (each digit spelled, possibly none);
//! slash: `[negative " "] [integer_part " " | integer_part(と|荷) "  "] numerator "/" " " denominator`;
//! word: `[…same prefix…] denominator " " morphosyntactic_features: "分の" " " numerator`.
//!
//! Numbers are kept as spans ([`Num`]) and spelled only when a candidate is emitted.

use super::Sink;
use super::cardinal::{SIGNED_RANK, digit_run, digit_word, ends, sign_end, spell_into};
use crate::text::{Cost, is_digit, starts_with};

/// One `num` reading: `√`?, the cardinal `t[a..b]`, and for a decimal number the spelled digits `t[b + 1..end]`.
#[derive(Clone, Copy)]
struct Num {
    root: bool,
    a: usize,
    b: usize,
    decimal: bool,
    end: usize,
}

impl Num {
    fn write(&self, t: &[char], out: &mut String) {
        if self.root {
            out.push('√');
        }
        spell_into(&t[self.a..self.b], out);
        if self.decimal {
            out.push('点');
            for &c in &t[self.b + 1..self.end] {
                out.push_str(digit_word(c));
            }
        }
    }
}

/// Every reading of `num` at `p`.
fn num(t: &[char], p: usize, mut f: impl FnMut(Num)) {
    let (q, root) = if t.get(p) == Some(&'√') { (p + 1, true) } else { (p, false) };
    for b in ends(t, q) {
        f(Num { root, a: q, b, decimal: false, end: b });
        if t.get(b) == Some(&'.') {
            let mut end = b + 1;
            loop {
                f(Num { root, a: q, b, decimal: true, end });
                if end < t.len() && is_digit(t[end]) {
                    end += 1;
                } else {
                    break;
                }
            }
        }
    }
}

/// Whether the first `num` at `p` can be followed by what every branch needs next (`/`, `分の`, or the integer
/// part's ` `, `と`, `荷`): a `num` ends at the end of its digit run, or after `.` and a second (possibly empty) run;
/// an end inside a run is followed by a digit.
fn marker_follows(t: &[char], p: usize) -> bool {
    let q = if t.get(p) == Some(&'√') { p + 1 } else { p };
    let marker = |e: usize| matches!(t.get(e), Some('/' | '分' | ' ' | 'と' | '荷'));
    let r = digit_run(t, q);
    r > q && (marker(r) || (t.get(r) == Some(&'.') && (marker(r + 1) || marker(digit_run(t, r + 1)))))
}

/// The optional `integer_part` prefix: the number and the `と`/`荷` it carries.
#[derive(Clone, Copy)]
struct Int {
    n: Num,
    char: Option<char>,
}

pub struct Fraction {
    pub cost: Cost,
}

impl Fraction {
    fn emit<S: Sink>(&self, t: &[char], neg: bool, int: Option<Int>, slash: bool, x: Num, y: Num, out: &mut S) {
        out.push(y.end, self.cost, if neg { SIGNED_RANK } else { 0 }, |tag| {
            tag.push_str("fraction { ");
            if neg {
                tag.push_str("negative: \"マイナス\" ");
            }
            if let Some(i) = int {
                tag.push_str("integer_part: \"");
                i.n.write(t, tag);
                match i.char {
                    Some(c) => {
                        tag.push(c);
                        tag.push_str("\"  ");
                    }
                    None => tag.push_str("\" "),
                }
            }
            if slash {
                tag.push_str("numerator: \"");
                x.write(t, tag);
                tag.push_str("\" denominator: \"");
            } else {
                tag.push_str("denominator: \"");
                x.write(t, tag);
                tag.push_str("\" morphosyntactic_features: \"分の\" numerator: \"");
            }
            y.write(t, tag);
            tag.push_str("\" }");
        });
    }

    /// Numerator/denominator part after the optional sign and integer prefix.
    fn body<S: Sink>(&self, t: &[char], p: usize, neg: bool, int: Option<Int>, out: &mut S) {
        num(t, p, |x| {
            let e = x.end;
            if t.get(e) == Some(&'/') {
                num(t, e + 1, |y| self.emit(t, neg, int, true, x, y, out));
            }
            if starts_with(t, e, "分の") {
                num(t, e + 2, |y| self.emit(t, neg, int, false, x, y, out));
            }
        });
    }

    fn unsigned<S: Sink>(&self, t: &[char], p: usize, neg: bool, out: &mut S) {
        if !marker_follows(t, p) {
            return;
        }
        self.body(t, p, neg, None, out);
        num(t, p, |n| match t.get(n.end) {
            Some(' ') => self.body(t, n.end + 1, neg, Some(Int { n, char: None }), out),
            Some(&c @ ('と' | '荷')) => self.body(t, n.end + 1, neg, Some(Int { n, char: Some(c) }), out),
            _ => {}
        });
    }

    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        if !matches!(t[start], '0'..='9' | '√' | '-' | 'マ') {
            return;
        }
        self.unsigned(t, start, false, out);
        if let Some(q) = sign_end(t, start) {
            self.unsigned(t, q, true, out);
        }
    }
}

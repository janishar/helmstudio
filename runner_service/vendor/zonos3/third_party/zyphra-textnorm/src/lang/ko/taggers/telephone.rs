//! `ko/taggers/telephone.py`.

use crate::lang::ko::graph_utils::*;
use crate::text::is_nemo_space;

const DIGIT: [&str; 10] = ["영", "일", "이", "삼", "사", "오", "육", "칠", "팔", "구"];

fn all_digits(t: &[char], p: usize, n: usize) -> bool {
    p + n <= t.len() && t[p..p + n].iter().all(char::is_ascii_digit)
}

fn digits_ko(t: &[char], p: usize, n: usize) -> Option<String> {
    if !all_digits(t, p, n) {
        return None;
    }
    Some(t[p..p + n].iter().map(|&c| DIGIT[(c as u8 - b'0') as usize]).collect())
}

fn is_sep(c: Option<&char>) -> bool {
    matches!(c, Some('-' | '.'))
}

/// `number_part: "<area> <mid> <last4>"`.
fn number_part(t: &[char], p: usize) -> Outs {
    let mut areas: Vec<(usize, String)> = Vec::new();
    if all_digits(t, p, 3) && is_sep(t.get(p + 3)) {
        areas.extend(digits_ko(t, p, 3).map(|a| (p + 4, a)));
    }
    if t.get(p) == Some(&'(') {
        if let Some(a) = digits_ko(t, p + 1, 3) {
            if t.get(p + 4) == Some(&')') {
                let mut ends = vec![p + 5];
                if t.get(p + 5) == Some(&' ') {
                    ends.push(p + 6);
                }
                for e in ends {
                    areas.push((e, a.clone()));
                    if is_sep(t.get(e)) {
                        areas.push((e + 1, a.clone()));
                    }
                }
            }
        }
    }
    let mut r = Outs::new();
    for (q, a) in areas {
        for n in [3, 4] {
            let Some(m) = digits_ko(t, q, n) else { continue };
            if !is_sep(t.get(q + n)) {
                continue;
            }
            if let Some(l) = digits_ko(t, q + n + 1, 4) {
                add(&mut r, q + n + 5, format!("number_part: \"{a} {m} {l}\""), 0);
            }
        }
    }
    r
}

pub fn tag(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    // every form has a digit within the first two chars and a separator (or ")") after 3 digits
    if !t.get(p).is_some_and(|c| c.is_ascii_digit() || matches!(c, '+' | '(')) {
        return r;
    }
    if t.get(p) == Some(&'+') {
        let de = run(t, p + 1, is_digit).min(p + 4);
        for e in p + 2..=de {
            let cc = digits_ko(t, p + 1, e - p - 1).unwrap_or_default();
            let mut afters = vec![e];
            if matches!(t.get(e), Some('-' | ' ')) {
                afters.push(e + 1);
            }
            for a in afters {
                for k in a..=run(t, a, is_nemo_space) {
                    for (e2, s, c) in number_part(t, k) {
                        add(&mut r, e2, format!("country_code: \"{cc}\" {s}"), c);
                    }
                }
            }
        }
    }
    for x in number_part(t, p) {
        add(&mut r, x.0, x.1, x.2);
    }
    r
}

//! `ko/taggers/fraction.py`.

use crate::lang::ko::graph_utils::*;
use crate::lang::ko::taggers::cardinal;
use crate::text::{is_nemo_space, match_str};

const DIGIT: [&str; 10] = ["영", "일", "이", "삼", "사", "오", "육", "칠", "팔", "구"];

/// `numeral = cardinal | √cardinal | decimal_number | √decimal_number`,
/// `decimal_number = cardinal + ("." → "점") + (digit | zero)*`.
fn numeral(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    let (q, root) = if t.get(p) == Some(&'√') { (p + 1, "√") } else { (p, "") };
    for (e, s, _) in cardinal::graph(t, q) {
        add(&mut r, e, format!("{root}{s}"), 0);
        if t.get(e) == Some(&'.') {
            let mut o = format!("{root}{s}점");
            add(&mut r, e + 1, o.clone(), 0);
            let fe = run(t, e + 1, is_digit);
            for k in e + 1..fe {
                o.push_str(DIGIT[(t[k] as u8 - b'0') as usize]);
                add(&mut r, k + 1, o.clone(), 0);
            }
        }
    }
    r
}

/// `integer_component_with_space`: `integer_part: "<numeral>과|와"` + delete_space + " ".
fn integer_with_space(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    for (e, s, _) in numeral(t, p) {
        let Some(&c) = t.get(e) else { continue };
        if c != '과' && c != '와' {
            continue;
        }
        let o = format!("integer_part: \"{s}{c}\" ");
        for k in e + 1..=run(t, e + 1, is_nemo_space) {
            add(&mut r, k, o.clone(), 0);
        }
    }
    r
}

fn body(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    let ints = opt(p, integer_with_space(t, p));
    // graph_fraction_slash
    for (a, ip, _) in &ints {
        for (e, num, _) in numeral(t, *a) {
            if t.get(e) != Some(&'/') {
                continue;
            }
            for (e2, den, _) in numeral(t, e + 1) {
                add(
                    &mut r,
                    e2,
                    format!("{ip}numerator: \"{num}\" denominator: \"{den}\" morphosyntactic_features: \"분의\""),
                    0,
                );
            }
        }
    }
    // graph_fraction_word
    for (a, ip, _) in &ints {
        for (e, den, _) in numeral(t, *a) {
            let Some(k) = match_str(t, e, "분의") else { continue };
            for (e2, num, _) in numeral(t, e + k) {
                add(
                    &mut r,
                    e2,
                    format!("{ip}denominator: \"{den}\" morphosyntactic_features: \"분의\" numerator: \"{num}\""),
                    0,
                );
            }
        }
    }
    r
}

/// The numeral/sign/integer-part alphabet a fraction's prefix is drawn from.
pub fn in_run(c: char) -> bool {
    c.is_ascii_digit() || is_nemo_space(c) || matches!(c, '.' | '√' | '-' | '마' | '이' | '너' | '스' | '과' | '와')
}

/// `/` (slash form) or `분의` (word form) at `e`.
pub fn run_end_ok(t: &[char], e: usize) -> bool {
    match t.get(e) {
        Some('/') => true,
        Some('분') => t.get(e + 1) == Some(&'의'),
        _ => false,
    }
}

/// The first char of every fraction span.
pub fn starts(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '√' | '-' | '마')
}

/// Necessary condition for any fraction from `p`: the first char outside [`in_run`] passes [`run_end_ok`].
pub fn possible(t: &[char], p: usize) -> bool {
    run_end_ok(t, run(t, p, in_run))
}

/// `self.graph`: optional sign + (slash | word) + optional particle.
pub fn graph(t: &[char], p: usize) -> Outs {
    if !possible(t, p) {
        return Outs::new();
    }
    let mut signs: Outs = vec![(p, String::new(), 0)];
    if let Some(k) = match_str(t, p, "마이너스") {
        signs.push((p + k, "negative: \"마이너스\" ".into(), 0));
    }
    if t.get(p) == Some(&'-') {
        add(&mut signs, p + 1, "negative: \"마이너스\" ".into(), 0);
    }
    let mut r = Outs::new();
    for (q, sg, _) in signs {
        for (e, b, _) in body(t, q) {
            add(&mut r, e, format!("{sg}{b}"), 0);
            let feat = match t.get(e) {
                Some('이' | '가') => "분의_subject",
                Some('은' | '는') => "분의_topic",
                Some('을' | '를') => "분의_object",
                _ => continue,
            };
            add(&mut r, e + 1, format!("{sg}{b} morphosyntactic_features: \"{feat}\""), 0);
        }
    }
    r
}

pub fn tag(t: &[char], p: usize) -> Outs {
    if t.get(p).is_some_and(|&c| starts(c)) { graph(t, p) } else { Outs::new() }
}

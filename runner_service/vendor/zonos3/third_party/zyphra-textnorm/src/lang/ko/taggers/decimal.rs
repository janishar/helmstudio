//! `ko/taggers/decimal.py`.

use crate::lang::ko::graph_utils::*;
use crate::lang::ko::taggers::cardinal;
use crate::text::match_str;

const DIGIT: [&str; 10] = ["영", "일", "이", "삼", "사", "오", "육", "칠", "팔", "구"];

/// `self.just_decimal`: `integer_part: "…" fractional_part: "…"`.
pub fn just_decimal(t: &[char], p: usize) -> Outs {
    just_decimal_if(t, p, |_| true)
}

/// `just_decimal` restricted to the ends `keep` accepts.
pub fn just_decimal_if(t: &[char], p: usize, keep: impl Fn(usize) -> bool) -> Outs {
    let mut r = Outs::new();
    let mut ints = Outs::new();
    if match_str(t, p, "10000").is_some() {
        ints.push((p + 5, "만".to_string(), 0));
    }
    for x in cardinal::graph_if(t, p, |e| t.get(e) == Some(&'.')) {
        add(&mut ints, x.0, x.1, x.2);
    }
    for (e, s, _) in ints {
        if t.get(e) != Some(&'.') {
            continue;
        }
        let f = e + 1;
        let fe = run(t, f, is_digit);
        let mut frac = String::with_capacity((fe - f) * 3);
        for k in f..fe {
            frac.push_str(DIGIT[(t[k] as u8 - b'0') as usize]);
            if !keep(k + 1) {
                continue;
            }
            add(&mut r, k + 1, format!("integer_part: \"{s}\" fractional_part: \"{frac}\""), 0);
        }
    }
    r
}

pub fn tag(t: &[char], p: usize) -> Outs {
    if t.get(p).is_some_and(char::is_ascii_digit) && t.get(run(t, p, is_digit)) != Some(&'.') {
        return Outs::new();
    }
    let mut r = just_decimal(t, p);
    let q = if t.get(p) == Some(&'-') { Some(p + 1) } else { match_str(t, p, "마이너스").map(|k| p + k) };
    if let Some(q) = q {
        for (e, s, c) in just_decimal(t, q) {
            add(&mut r, e, format!("negative: \"마이너스\" {s}"), c);
        }
    }
    r
}

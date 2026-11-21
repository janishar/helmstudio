//! The whole-split verbalizer for splits holding several fractions.
//!
//! NeMo verbalizes the concatenated split string, and de's fraction numerator (`change_one` over `NEMO_SIGMA`) reads
//! any text, quotes included: a fraction's numerator may run on through the following tokens up to a later
//! fraction's numerator, whose denominator then ends the token (`27/12/1720` → `sieben und zwanzig" denominator:
//! "eins" … numerator: "zwei ein tausend sieben hundert zwanzigstel`). The cheapest path wins; a crossing costs the
//! first fraction's sign/integer weights only, so it beats token-by-token reading when the swallowed tokens carry
//! weight (ordinal +0.01) and loses when they save some (`eins` → `ein` −0.0001). Among equal costs the oracle takes
//! the crossing, and the widest one.

use super::fraction_fields;
use super::verbalizers::fraction::{change_one, fraction_default};
use crate::tag::{Dict, Value, serialize};
use crate::text::Cost;

fn ser(t: &Dict) -> String {
    let mut s = String::new();
    serialize(t, &mut s);
    s
}

/// One ordering of a split: `None` when no path exists.
pub fn verbalize(ordering: &[Dict]) -> Option<String> {
    let n = ordering.len();
    // per-token cheapest output (None: this ordering of the token has no path)
    let single: Vec<Option<(String, Cost)>> = ordering
        .iter()
        .map(|t| match t.as_slice() {
            [(k, Value::Dict(body))] if k == "tokens" => super::verbalize_body(body),
            _ => None,
        })
        .collect();
    // best[k]: cheapest verbalization of tokens[..k]: (cost, -crossed tokens, pieces)
    let mut best: Vec<Option<(Cost, i64, Vec<String>)>> = vec![None; n + 1];
    best[0] = Some((0, 0, Vec::new()));
    let better =
        |old: &Option<(Cost, i64, Vec<String>)>, c: Cost, x: i64| old.as_ref().is_none_or(|o| (c, x) < (o.0, o.1));
    for k in 0..n {
        let Some((c0, x0, p0)) = best[k].clone() else { continue };
        if let Some((s, c)) = &single[k] {
            if better(&best[k + 1], c0 + c, x0) {
                let mut p = p0.clone();
                p.push(s.clone());
                best[k + 1] = Some((c0 + c, x0, p));
            }
        }
        let Some(fi) = fraction_fields(&ordering[k]) else { continue };
        let Some((head, head_cost, tail_i)) = start(fi, &ser(&ordering[k])) else { continue };
        let mut any = tail_i;
        for j in k + 1..n {
            let sj = ser(&ordering[j]);
            if let Some(fj) = fraction_fields(&ordering[j]) {
                if let Some((num_end, den)) = end(fj, &sj) {
                    let mut value = any.clone();
                    value.push_str(&sj[..num_end]);
                    let (num, nc) = change_one(&value);
                    let mut out = head.clone();
                    out.push_str(&fraction_default(num, den));
                    let c = c0 + head_cost + nc;
                    let x = x0 - (j - k + 1) as i64;
                    if better(&best[j + 1], c, x) {
                        let mut p = p0.clone();
                        p.push(out);
                        best[j + 1] = Some((c, x, p));
                    }
                }
            }
            any.push_str(&sj);
        }
    }
    best[n].take().map(|(_, _, p)| p.join(" ").replace('\u{a0}', " "))
}

/// A fraction that can start a crossing: its verbalized sign/integer prefix, that prefix's weight, and the serialized
/// text after `numerator: "`.
fn start(f: &Dict, s: &str) -> Option<(String, Cost, String)> {
    let mut head = String::new();
    let mut c = 0;
    let mut it = f.iter().peekable();
    if let Some((k, v)) = it.peek() {
        if k == "negative" {
            if *v != Value::Str("true".into()) {
                return None;
            }
            head.push_str("minus ");
            it.next();
        }
    }
    if let Some((k, Value::Str(v))) = it.peek() {
        if k == "integer_part" {
            let (w, k) = change_one(v);
            head.push_str(w);
            head.push(' ');
            c += k;
            it.next();
        }
    }
    let (k, _) = it.next()?;
    if k != "numerator" {
        return None;
    }
    let at = s.find("numerator: \"")? + "numerator: \"".len();
    Some((head, c, s[at..].to_string()))
}

/// A fraction that can end a crossing: the byte end of its numerator value in `s` and its denominator.
fn end<'a>(f: &'a Dict, s: &str) -> Option<(usize, &'a str)> {
    let mut num = None;
    let mut den = None;
    let mut rest = f.iter();
    for (k, v) in rest.by_ref() {
        if k == "numerator" {
            num = Some(v);
            break;
        }
    }
    let (k, Value::Str(d)) = rest.next()? else { return None };
    if k != "denominator" || d.contains('"') {
        return None;
    }
    den = den.or(Some(d.as_ref()));
    for (k, v) in rest {
        if k != "preserve_order" || *v != Value::True {
            return None;
        }
    }
    let Value::Str(nv) = num? else { return None };
    let at = s.find("numerator: \"")? + "numerator: \"".len() + nv.len();
    Some((at, den?))
}

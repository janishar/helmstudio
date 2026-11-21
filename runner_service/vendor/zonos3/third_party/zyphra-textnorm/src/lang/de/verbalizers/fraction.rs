//! `de/verbalizers/fraction.py`.
//!
//! `integer_part` and `numerator` are `change_one` = `cdrewrite("eins" → "ein" @ −0.0001, BOS, EOS, Σ*)`: any text,
//! quotes included (see `super::super::verbalize_split`). The optional `und ` (+0.001) is never cheapest.

use super::ordinal::stem;
use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::{Cost, cost};

const ONE: Cost = cost(-0.0001);

/// `change_one` on a field value.
pub fn change_one(s: &str) -> (&str, Cost) {
    if s == "eins" { ("ein", ONE) } else { (s, 0) }
}

/// The denominator: `cdrewrite(stem?, "", EOS) + "tel"`, then `zweitel` → `halbe`.
pub fn denominator(d: &str) -> String {
    let mut s = stem(d).unwrap_or_else(|| d.to_string());
    s.push_str("tel");
    if s == "zweitel" { "halbe".to_string() } else { s }
}

/// `numerator " " denominator` with `ein halbe` → `ein halb` on the whole.
pub fn fraction_default(num: &str, den: &str) -> String {
    let s = format!("{num} {}", denominator(den));
    if s == "ein halbe" { "ein halb".to_string() } else { s }
}

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let neg = f.opt("negative")?;
    if neg.is_some_and(|v| v != "true") {
        return None;
    }
    let mut out = String::new();
    let mut c = 0;
    if neg.is_some() {
        out.push_str("minus ");
    }
    if let Some(i) = f.opt("integer_part")? {
        let (w, k) = change_one(i);
        out.push_str(w);
        out.push(' ');
        c += k;
    }
    let (n, k) = change_one(f.take("numerator")?);
    c += k;
    let den = f.take("denominator")?;
    while f.preserve_order() {}
    if !f.done() {
        return None;
    }
    out.push_str(&fraction_default(n, den));
    Some((out, c))
}

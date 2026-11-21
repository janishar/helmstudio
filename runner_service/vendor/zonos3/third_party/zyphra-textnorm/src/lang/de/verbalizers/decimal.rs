//! `de/verbalizers/decimal.py`: `sign? (integer quantity | integer " komma " fractional quantity?)`.

use crate::lang::common::Fields;
use crate::lang::de::taggers::decimal::QUANTITIES;
use crate::tag::Dict;
use crate::text::Cost;

/// The decimal body read from `f` (shared with money and measure), leaving `f` after the optional
/// `preserve_order`.
pub fn body(f: &mut Fields) -> Option<String> {
    let neg = f.opt("negative")?;
    if neg.is_some_and(|v| v != "true") {
        return None;
    }
    let mut out = String::new();
    if neg.is_some() {
        out.push_str("minus ");
    }
    out.push_str(f.take("integer_part")?);
    let frac = f.opt("fractional_part")?;
    if let Some(fr) = frac {
        out.push_str(" komma ");
        out.push_str(fr);
    }
    let q = f.opt("quantity")?;
    if let Some(q) = q {
        if !QUANTITIES.contains_key(q) {
            return None;
        }
        out.push(' ');
        out.push_str(q);
    }
    if frac.is_none() && q.is_none() {
        return None;
    }
    while f.preserve_order() {}
    Some(out)
}

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let s = body(&mut f)?;
    f.done().then_some((s, 0))
}

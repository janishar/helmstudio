//! `es/verbalizers/decimals.py` (deterministic, LOCALIZATION "eu").

use super::{Outs, is_key, rest_is_po, s};
use crate::lang::es::graph_utils::{shift_cardinal_gender, shift_number_gender};
use crate::tag::Field;

pub struct Parts<'a> {
    pub neg: bool,
    pub integer: &'a str,
    pub fractional: Option<&'a str>,
    pub quantity: Option<&'a str>,
    /// Fields consumed (before any `preserve_order`).
    pub n: usize,
}

/// Parse `negative? integer_part fractional_part? quantity?` from the front of `d`.
pub fn parts<'t>(d: &'t [Field]) -> Option<Parts<'t>> {
    let mut i = 0;
    let neg = is_key(d, 0, "negative");
    if neg {
        if s(d, 0, "negative")? != "true" {
            return None;
        }
        i = 1;
    }
    let integer = s(d, i, "integer_part").filter(|v| !v.is_empty())?;
    i += 1;
    let fractional = if is_key(d, i, "fractional_part") {
        i += 1;
        Some(s(d, i - 1, "fractional_part").filter(|v| !v.is_empty())?)
    } else {
        None
    };
    let quantity = if is_key(d, i, "quantity") {
        i += 1;
        Some(s(d, i - 1, "quantity").filter(|v| !v.is_empty())?)
    } else {
        None
    };
    if fractional.is_none() && quantity.is_none() {
        return None;
    }
    Some(Parts { neg, integer, fractional, quantity, n: i })
}

fn sign(p: &Parts) -> &'static str {
    if p.neg { "menos " } else { "" }
}

/// `graph_masc` text (all forms accept a quantity).
pub fn masc(p: &Parts) -> String {
    let mut o = format!("{}{}", sign(p), p.integer);
    if let Some(f) = p.fractional {
        o.push_str(" coma ");
        o.push_str(f);
    }
    if let Some(q) = p.quantity {
        o.push(' ');
        o.push_str(q);
    }
    o
}

/// Whether `graph_fem` accepts (integer + fractional, no quantity).
pub fn fem_ok(p: &Parts) -> bool {
    p.fractional.is_some() && p.quantity.is_none()
}

/// `graph_fem` text (integer + fractional, no quantity).
pub fn fem(p: &Parts) -> Option<String> {
    let f = p.fractional?;
    if p.quantity.is_some() {
        return None;
    }
    Some(format!("{}{}{}", sign(p), shift_cardinal_gender(p.integer), shift_number_gender(&format!(" coma {f}"))))
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let p = parts(d)?;
    if !rest_is_po(d, p.n) {
        return None;
    }
    let mut o = Outs::default();
    o.push(0, || masc(&p));
    if fem_ok(&p) {
        o.push(0, || fem(&p).expect("fem_ok"));
    }
    o.best()
}

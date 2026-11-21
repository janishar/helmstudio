//! `es/verbalizers/cardinal.py`.

use super::{Outs, is_key, s};
use crate::lang::es::graph_utils::shift_cardinal_gender;
use crate::tag::Field;

/// `optional_sign + integer` as a masculine string; `(text, fields consumed)`.
pub fn masc(d: &[Field]) -> Option<(String, usize)> {
    let mut i = 0;
    let mut out = String::new();
    if is_key(d, 0, "negative") {
        if s(d, 0, "negative")? != "true" {
            return None;
        }
        out.push_str("menos ");
        i = 1;
    }
    let v = s(d, i, "integer")?;
    if v.is_empty() {
        return None;
    }
    out.push_str(v);
    Some((out, i + 1))
}

/// `graph_masc` / `graph_fem` over the whole dict.
pub fn graphs(d: &[Field]) -> Option<(String, String)> {
    let (m, n) = masc(d)?;
    if n != d.len() {
        return None;
    }
    let f = shift_cardinal_gender(&m);
    Some((m, f))
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let (m, f) = graphs(d)?;
    let mut o = Outs::default();
    o.push(0, || m);
    o.push(0, || f);
    o.best()
}

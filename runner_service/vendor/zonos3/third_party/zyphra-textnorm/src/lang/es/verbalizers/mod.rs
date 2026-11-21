//! `es/verbalizers/*`: each verbalizer pushes every output it accepts for one field ordering; the cheapest wins,
//! ties to the first pushed (masculine branches first: the oracle's choice, e.g. `1` → `un`, `1/2` → `medio`).

pub mod cardinal;
pub mod date;
pub mod decimals;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod telephone;
pub mod time;

use crate::tag::{Dict, Field, Value};
use crate::text::Cost;

/// Candidate outputs of one verbalizer: keeps the cheapest, ties to the first pushed. An output's text is built only
/// when it beats every output pushed before it.
#[derive(Default)]
pub struct Outs(Option<(String, Cost)>);

impl Outs {
    pub fn push(&mut self, c: Cost, s: impl FnOnce() -> String) {
        if self.0.as_ref().is_none_or(|b| c < b.1) {
            self.0 = Some((s(), c));
        }
    }
    pub fn best(self) -> Option<String> {
        self.0.map(|b| b.0)
    }
}

/// Field view: `(key, value)` slice with helpers.
pub fn s<'a>(d: &'a [Field], i: usize, key: &str) -> Option<&'a str> {
    match d.get(i)? {
        (k, Value::Str(v)) if k == key && !v.contains('"') => Some(v),
        _ => None,
    }
}

pub fn is_key(d: &[Field], i: usize, key: &str) -> bool {
    d.get(i).is_some_and(|(k, _)| k == key)
}

/// Trailing `preserve_order: true` fields (`delete_preserve_order` closure) from `i` to the end.
pub fn rest_is_po(d: &[Field], i: usize) -> bool {
    d[i.min(d.len())..].iter().all(|(k, v)| k == "preserve_order" && *v == Value::True)
}

pub fn dict<'t, 'a>(d: &'t [Field<'a>], i: usize, key: &str) -> Option<&'t Dict<'a>> {
    match d.get(i)? {
        (k, Value::Dict(v)) if k == key => Some(v),
        _ => None,
    }
}

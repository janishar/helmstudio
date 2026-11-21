//! `de/verbalizers/cardinal.py`: `("negative: \"true\" " → "minus ")? integer`.

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::Cost;

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let neg = f.opt("negative")?;
    if neg.is_some_and(|v| v != "true") {
        return None;
    }
    let i = f.take("integer")?;
    if !f.done() {
        return None;
    }
    Some((if neg.is_some() { format!("minus {i}") } else { i.to_string() }, 0))
}

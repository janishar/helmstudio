//! `fr/verbalizers/decimals.py`: sign? (integer quantity | integer " virgule " fractional quantity?) preserve_order*.

use crate::lang::common::Fields;
use crate::tag::Dict;

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let mut out = String::new();
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "true" {
            return None;
        }
        out.push_str("moins ");
    }
    out.push_str(f.take("integer_part")?);
    let frac = f.opt("fractional_part")?;
    if let Some(fr) = frac {
        out.push_str(" virgule ");
        out.push_str(fr);
    }
    let q = f.opt("quantity")?;
    if let Some(q) = q {
        out.push(' ');
        out.push_str(q);
    } else if frac.is_none() {
        return None;
    }
    while f.preserve_order() {}
    f.done().then_some(out)
}

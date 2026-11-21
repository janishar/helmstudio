//! `pt/verbalizers/decimal.py`.

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;

crate::labels!(SPECIALS, "pt/numbers/decimal_specials.tsv");

static SPEC: LazyLock<(String, String)> = LazyLock::new(|| {
    let get = |k: &str, d: &str| {
        SPECIALS.iter().find(|r| r.len() >= 2 && r[0] == k).map(|r| r[1].clone()).unwrap_or_else(|| d.to_string())
    };
    (get("separator", "vírgula"), get("minus", "menos"))
});

/// `numbers`: `optional_sign + (integer_quantity | decimal_part)`, consuming the fields.
pub fn numbers(f: &mut Fields) -> Option<String> {
    let (sep, minus) = &*SPEC;
    let mut out = String::new();
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "true" {
            return None;
        }
        out.push_str(minus);
        out.push(' ');
    }
    out.push_str(f.take("integer_part")?);
    let quantity_only = f.peek_key() == Some("quantity");
    if !quantity_only {
        out.push(' ');
        out.push_str(sep);
        out.push(' ');
        out.push_str(f.take("fractional_part")?);
    }
    if let Some(q) = f.opt("quantity")? {
        out.push(' ');
        out.push_str(q);
    } else if quantity_only {
        return None;
    }
    Some(out)
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let out = numbers(&mut f)?;
    f.done().then_some(out)
}

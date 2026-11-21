//! `it/verbalizers/decimal.py`.

use crate::lang::common::Fields;

/// `self.graph` before `delete_preserve_order`: sign, integer, (quantity | fractional quantity?). With `di`, the
/// money verbalizer's ` di` insertion after the quantity.
pub fn body(f: &mut Fields, di: bool) -> Option<String> {
    let neg = match f.peek_key() {
        Some("negative") => (f.take("negative")? == "true").then_some(true)?,
        _ => false,
    };
    let mut out = String::new();
    if neg {
        out.push_str("meno ");
    }
    out.push_str(f.take("integer_part")?);
    let frac = f.opt("fractional_part")?;
    if let Some(fr) = frac {
        out.push_str(" virgola ");
        out.push_str(fr);
    }
    let q = f.opt("quantity")?;
    match q {
        Some(q) => {
            out.push(' ');
            out.push_str(q);
            if di {
                out.push_str(" di");
            }
        }
        None if frac.is_none() => return None,
        None => {}
    }
    Some(out)
}

pub fn verbalize(f: &mut Fields) -> Option<String> {
    let s = body(f, false)?;
    super::end(f).then_some(s)
}

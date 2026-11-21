//! `es/verbalizers/telephone.py`.

use super::{is_key, s};
use crate::tag::Field;

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    let mut out = String::new();
    if is_key(d, 0, "country_code") {
        out.push_str(s(d, 0, "country_code").filter(|v| !v.is_empty())?);
        out.push(' ');
        i = 1;
    }
    let np = s(d, i, "number_part").filter(|v| !v.is_empty())?;
    // optional delete of one trailing " " at -0.0001 (preferred)
    let np = match np.strip_suffix(' ') {
        Some(r) if !r.is_empty() => r,
        _ => np,
    };
    out.push_str(np);
    i += 1;
    if is_key(d, i, "extension") {
        out.push(' ');
        out.push_str(s(d, i, "extension").filter(|v| !v.is_empty())?);
        i += 1;
    }
    (i == d.len()).then_some(out)
}

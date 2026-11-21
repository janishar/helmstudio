//! `en/verbalizers/telephone.py`.

use crate::lang::common::Fields;
use crate::tag::Dict;

pub fn telephone(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out = String::new();
    if let Some(c) = f.opt("country_code")? {
        if c.is_empty() {
            return None;
        }
        out.push_str(c);
        out.push(' ');
    }
    let n = f.take("number_part")?;
    // NOT_QUOTE+ then an optional deleted trailing space (−0.0001, taken when possible)
    let n = match n.strip_suffix(' ') {
        Some(p) if !p.is_empty() => p,
        _ => n,
    };
    if n.is_empty() {
        return None;
    }
    out.push_str(n);
    if let Some(e) = f.opt("extension")? {
        if e.is_empty() {
            return None;
        }
        out.push(' ');
        out.push_str(e);
    }
    f.done().then_some(out)
}

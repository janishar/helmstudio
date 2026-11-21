//! `pt/verbalizers/telephone.py`.

use crate::lang::common::Fields;
use crate::tag::Dict;

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out = String::new();
    if let Some(cc) = f.opt("country_code")? {
        out.push_str(cc);
        out.push(' ');
    }
    let np = f.take("number_part")?;
    // an optional trailing " " is deleted at −0.0001
    out.push_str(np.strip_suffix(' ').unwrap_or(np));
    if let Some(ext) = f.opt("extension")? {
        out.push(' ');
        out.push_str(ext);
    }
    f.done().then_some(out)
}

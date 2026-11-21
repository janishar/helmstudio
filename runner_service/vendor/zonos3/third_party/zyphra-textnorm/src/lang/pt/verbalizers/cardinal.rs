//! `pt/verbalizers/cardinal.py`.

use crate::lang::common::Fields;
use crate::lang::pt::graph_utils::shift_cardinal_gender;
use crate::tag::Dict;

/// `graph_masc` (`feminine` = `graph_fem`): `[negative: "true" ]integer: "…"`, consuming the fields.
pub fn number(f: &mut Fields, feminine: bool) -> Option<String> {
    let mut out = String::new();
    super::sign(f, &mut out)?;
    out.push_str(f.take("integer")?);
    Some(if feminine { shift_cardinal_gender(&out) } else { out })
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let out = number(&mut f, false)?;
    f.done().then_some(out)
}

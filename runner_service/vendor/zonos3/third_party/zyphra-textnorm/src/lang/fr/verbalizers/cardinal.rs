//! `fr/verbalizers/cardinal.py`: `(negative: "true" → "moins ")? integer`.

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
    out.push_str(f.take("integer")?);
    f.done().then_some(out)
}

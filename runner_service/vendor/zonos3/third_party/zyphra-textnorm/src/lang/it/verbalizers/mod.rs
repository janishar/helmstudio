//! `it/verbalizers/` (`verbalize.py` union + `verbalize_final.py`'s word branch).

pub mod cardinal;
pub mod decimal;
pub mod electronic;
pub mod measure;
pub mod money;
pub mod time;

use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

/// One token body in one field ordering; NBSP → space (`delete_tokens`).
pub fn verbalize(token: &Dict) -> Option<String> {
    let (class, value) = common::class(token)?;
    let fields = match value {
        Value::Dict(d) => d,
        Value::Str(_) if class == "name" => return common::verbalize_name(token),
        _ => return None,
    };
    let out = match class {
        "cardinal" => cardinal::verbalize(&mut Fields::new(fields)),
        "decimal" => decimal::verbalize(&mut Fields::new(fields)),
        "electronic" => electronic::verbalize(fields),
        "measure" => measure::verbalize(fields),
        "money" => money::verbalize(fields),
        "time" => time::verbalize(fields),
        _ => None,
    }?;
    Some(out.replace('\u{a0}', " "))
}

/// `delete_preserve_order` at the end of a field list.
pub(crate) fn end(f: &mut Fields) -> bool {
    f.preserve_order();
    f.done()
}

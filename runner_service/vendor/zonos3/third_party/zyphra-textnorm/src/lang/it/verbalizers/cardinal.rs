//! `it/verbalizers/cardinal.py`.

use crate::lang::common::Fields;

/// `optional_sign + integer` (the caller checks that every field was consumed).
pub fn body(f: &mut Fields) -> Option<String> {
    let neg = match f.peek_key() {
        Some("negative") => (f.take("negative")? == "true").then_some(true)?,
        _ => false,
    };
    let int = f.take("integer")?;
    Some(if neg { format!("meno {int}") } else { int.to_string() })
}

pub fn verbalize(f: &mut Fields) -> Option<String> {
    let s = body(f)?;
    f.done().then_some(s)
}

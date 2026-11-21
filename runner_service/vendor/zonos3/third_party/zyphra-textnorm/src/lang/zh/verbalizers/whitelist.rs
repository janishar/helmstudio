//! `zh/verbalizers/whitelist.py` (`erhua: "儿"` → nothing | `name: "<NOT_QUOTE*>"`) and `word.py`
//! (`name: "<one NOT_QUOTE>"`). Neither goes through `delete_tokens`, so an NBSP in the value stays.

use super::{Cur, push};

pub fn word(c: &mut Cur, out: &mut String) -> bool {
    match c.field("name: \"") {
        Some(v) if v.len() == 1 => {
            push(out, v);
            true
        }
        _ => false,
    }
}

pub fn whitelist(c: &mut Cur, out: &mut String) -> bool {
    if c.lit("erhua: \"儿\"") {
        return true;
    }
    match c.field("name: \"") {
        Some(v) => {
            push(out, v);
            true
        }
        None => false,
    }
}

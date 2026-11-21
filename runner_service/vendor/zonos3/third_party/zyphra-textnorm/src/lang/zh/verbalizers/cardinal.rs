//! `zh/verbalizers/cardinal.py`: `[negative: "-" → 负] integer [quantity]` (`numbers`, also used by measure).

use super::{Cur, push};

/// `delete("integer: ") + delete("\"") + NOT_QUOTE* + delete("\"") + delete_space`.
fn integer(c: &mut Cur, out: &mut String) -> bool {
    match c.field("integer: \"") {
        Some(v) => {
            push(out, v);
            c.ws();
            true
        }
        None => false,
    }
}

/// `numbers`: `integer | sign delete_space integer | integer quantity | sign delete_space integer quantity`.
pub fn numbers(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    if c.lit("negative: \"-\"") {
        out.push('负');
        c.ws();
    }
    if !integer(c, out) {
        *c = save;
        out.truncate(mark);
        return false;
    }
    if let Some(q) = c.field("quantity: \"") {
        push(out, q);
    }
    true
}

pub fn cardinal(c: &mut Cur, out: &mut String) -> bool {
    numbers(c, out)
}

//! `zh/verbalizers/fraction.py`: `[sign] (I 又 D 分之 N | D 分之 N | D 分之 integer_part)`, where the sign is
//! `positive:` or `negative:` passed through.

use super::{Cur, push};

fn part(c: &mut Cur, key_colon: &str, out: &mut String) -> bool {
    match c.field_ws(key_colon) {
        Some(v) => {
            push(out, v);
            true
        }
        None => false,
    }
}

/// `graph | graph_with_decimal` after the optional sign.
fn body(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    // graph_with_integer
    if part(c, "integer_part:", out) {
        out.push('又');
        c.ws();
        if part(c, "denominator:", out) {
            c.ws();
            out.push_str("分之");
            if part(c, "numerator:", out) {
                return true;
            }
        }
        *c = save;
        out.truncate(mark);
        return false;
    }
    if !part(c, "denominator:", out) {
        return false;
    }
    c.ws();
    out.push_str("分之");
    // graph_no_integer | graph_with_decimal
    if part(c, "numerator:", out) {
        return true;
    }
    if let Some(v) = c.field("integer_part: \"") {
        push(out, v);
        return true;
    }
    *c = save;
    out.truncate(mark);
    false
}

pub fn fraction(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    if part(c, "positive:", out) || part(c, "negative:", out) {
        c.ws();
        if body(c, out) {
            return true;
        }
        *c = save;
        out.truncate(mark);
        return false;
    }
    body(c, out)
}

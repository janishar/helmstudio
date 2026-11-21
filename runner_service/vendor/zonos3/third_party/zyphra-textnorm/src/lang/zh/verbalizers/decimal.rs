//! `zh/verbalizers/decimal.py`: `(negative: "负")* integer_part 点 fractional_part [quantity]` (`decimal_component`,
//! also used by fraction, money and measure).

use super::{Cur, push};

fn nonempty(c: &mut Cur, open: &str, out: &mut String) -> bool {
    let save = *c;
    match c.field(open) {
        Some(v) if !v.is_empty() => {
            push(out, v);
            true
        }
        _ => {
            *c = save;
            false
        }
    }
}

/// `sign`: `closure(delete("negative:") delete_space delete("\"") 负 delete("\""))`.
fn sign(c: &mut Cur, out: &mut String) {
    loop {
        let save = *c;
        if c.field_ws("negative:").is_some_and(|v| v == ['负']) {
            out.push('负');
            c.ws();
        } else {
            *c = save;
            return;
        }
    }
}

pub fn decimal(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    sign(c, out);
    if nonempty(c, "integer_part: \"", out) {
        c.ws();
        out.push('点');
        if nonempty(c, "fractional_part: \"", out) {
            let after = *c;
            c.ws();
            if c.lit("quantity:") {
                c.ws();
                if nonempty(c, "\"", out) {
                    return true;
                }
            }
            *c = after;
            return true;
        }
    }
    *c = save;
    out.truncate(mark);
    false
}

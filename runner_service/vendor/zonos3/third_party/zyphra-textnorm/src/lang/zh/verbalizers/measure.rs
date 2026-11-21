//! `zh/verbalizers/measure.py`: `cardinal { numbers } [units]` | `decimal { decimal_component } units` |
//! `fraction { fraction } units`. Values pass through.

use super::{Cur, cardinal, decimal, fraction, push};

fn units(c: &mut Cur, out: &mut String) -> bool {
    c.ws();
    match c.field("units: \"") {
        Some(v) => {
            push(out, v);
            true
        }
        None => false,
    }
}

/// `delete(" } ")` right after a field whose trailing `delete_space` may have taken the space it needs.
fn close(c: &mut Cur) -> bool {
    while c.i > 0 && c.s[c.i - 1] == ' ' && c.s[c.i - 1] != '"' {
        c.i -= 1;
    }
    c.sp_lit(" } ")
}

pub fn measure(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    if c.lit("cardinal { ") && cardinal::numbers(c, out) && close(c) {
        // graph_cardinal (units) | graph_math_cardinal (none)
        let after = *c;
        if units(c, out) {
            return true;
        }
        *c = after;
        return true;
    }
    *c = save;
    out.truncate(mark);
    for (open, f) in
        [("decimal {", decimal::decimal as fn(&mut Cur, &mut String) -> bool), ("fraction {", fraction::fraction)]
    {
        if c.lit(open) {
            c.ws();
            if f(c, out) {
                c.ws();
                if c.lit("}") && units(c, out) {
                    return true;
                }
            }
        }
        *c = save;
        out.truncate(mark);
    }
    false
}

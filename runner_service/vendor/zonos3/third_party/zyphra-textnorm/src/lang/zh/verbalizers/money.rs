//! `zh/verbalizers/money.py`: `graph_unit_money` (`(N U)*`, U = `currency | currency_maj | currency_min`; covers
//! `graph_regular_money`) | `decimal_component C` | `N quantity C`. Values pass through in field order.

use super::{Cur, decimal, push};

fn take(c: &mut Cur, open: &str, out: &mut String) -> bool {
    match c.field(open) {
        Some(v) => {
            push(out, v);
            true
        }
        None => false,
    }
}

fn units(c: &mut Cur, out: &mut String) -> bool {
    // closure(number delete_space unit closure(delete_space)); the token's `delete_space }` follows
    loop {
        let save = *c;
        let mark = out.len();
        if !take(c, "integer_part: \"", out) {
            return true;
        }
        c.ws();
        if !(take(c, "currency: \"", out) || take(c, "currency_maj: \"", out) || take(c, "currency_min: \"", out)) {
            *c = save;
            out.truncate(mark);
            return true;
        }
        c.ws();
    }
}

/// [`money`] on parsed fields whose values hold no `"`, for the `graph_unit_money` shape only (`(integer_part
/// currency|currency_maj|currency_min)*`): `Some(true)` with the values concatenated, `None` for any other shape.
pub fn direct(fields: &[(&str, &str)], out: &mut String) -> Option<bool> {
    if fields.len() % 2 != 0
        || !fields
            .chunks(2)
            .all(|p| p[0].0 == "integer_part" && matches!(p[1].0, "currency" | "currency_maj" | "currency_min"))
    {
        return None;
    }
    for (_, v) in fields {
        out.push_str(v);
    }
    Some(true)
}

pub fn money(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    let ends = |c: &Cur| {
        let mut p = *c;
        p.ws();
        p.peek("}")
    };
    if units(c, out) && ends(c) {
        return true;
    }
    *c = save;
    out.truncate(mark);
    if decimal::decimal(c, out) {
        c.ws();
        if take(c, "currency: \"", out) && ends(c) {
            return true;
        }
    }
    *c = save;
    out.truncate(mark);
    if take(c, "integer_part: \"", out) {
        c.ws();
        if take(c, "quantity: \"", out) {
            c.ws();
            if take(c, "currency: \"", out) && ends(c) {
                return true;
            }
        }
    }
    *c = save;
    out.truncate(mark);
    false
}

//! `zh/verbalizers/ordinal.py`: `integer` (−2.0) | `[range] integer range integer`, where a range value is `从*`,
//! `到*` or one of `- ~ —— —` (→ `到`).

use super::{Cur, push};

/// `delete("integer:") + delete_space + delete("\"") + 第? + NOT_QUOTE* + delete("\"")`.
fn integer(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    if !c.lit("integer:") {
        return false;
    }
    c.ws();
    match c.field("\"") {
        Some(v) => {
            push(out, v);
            true
        }
        None => {
            *c = save;
            false
        }
    }
}

/// `delete_morpheme`.
fn morpheme(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let Some(v) = c.field("range: \"") else { return false };
    if v.iter().all(|&ch| ch == '从') || v.iter().all(|&ch| ch == '到') {
        out.extend(v);
        return true;
    }
    if matches!(v, ['-'] | ['~'] | ['—', '—'] | ['—']) {
        out.push('到');
        return true;
    }
    *c = save;
    false
}

pub fn ordinal(c: &mut Cur, out: &mut String) -> bool {
    let save = *c;
    let mark = out.len();
    // graph_integer alone (the cheaper branch): the token must end right after it
    if integer(c, out) {
        let mut probe = *c;
        probe.ws();
        if probe.peek("}") {
            return true;
        }
    }
    *c = save;
    out.truncate(mark);
    // graph_range
    if morpheme(c, out) {
        c.ws();
    }
    if integer(c, out) {
        c.ws();
        if morpheme(c, out) {
            c.ws();
            if integer(c, out) {
                return true;
            }
        }
    }
    *c = save;
    out.truncate(mark);
    false
}

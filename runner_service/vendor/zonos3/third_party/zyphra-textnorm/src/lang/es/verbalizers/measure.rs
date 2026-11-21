//! `es/verbalizers/measure.py` (deterministic).

use super::{Outs, cardinal, decimals, dict, fraction, rest_is_po, s};
use crate::tag::Field;
use crate::text::{Cost, Table, cost, is_nemo_space};

crate::tsv!(PLURAL_FEM, "es/measures/measurements_plural_fem.tsv");
crate::tsv!(PLURAL_MASC, "es/measures/measurements_plural_masc.tsv");

fn in_table(t: &Table, w: &str) -> bool {
    t.contains_key(w) || t.rows().iter().any(|r| r.value == w)
}

/// Units still carry the tagger's NBSP here (`delete_tokens` maps it back only at the end), so a multi-word table
/// entry ("metros cúbicos") never matches and such units take the 0.01 `graph_alpha_num` path without " de".
/// `(U) (WS "por" NOT_QUOTE+)?` with `U` from the table, or (masc only) `"por" NOT_QUOTE+`.
fn unit_ok(t: &Table, u: &str, bare_por: bool) -> bool {
    if in_table(t, u) {
        return true;
    }
    if bare_por && u.len() > 3 && u.starts_with("por") {
        return true;
    }
    for (i, c) in u.char_indices() {
        if is_nemo_space(c) {
            let rest = &u[i + c.len_utf8()..];
            if rest.len() > 3 && rest.starts_with("por") && in_table(t, &u[..i]) {
                return true;
            }
        }
    }
    false
}

/// Number outputs `(text, is_fraction_mode)` for the inner dict, masc or fem.
fn numbers(key: &str, inner: &[Field], fem: bool, de: bool) -> Vec<String> {
    match key {
        "cardinal" => cardinal::graphs(inner).map(|(m, f)| vec![if fem { f } else { m }]).unwrap_or_default(),
        "decimal" => {
            let Some(p) = decimals::parts(inner) else {
                return Vec::new();
            };
            if !rest_is_po(inner, p.n) {
                return Vec::new();
            }
            let q = p.quantity.map(|q| if de { format!("{q} de") } else { q.to_string() });
            let p = decimals::Parts { quantity: q.as_deref(), ..p };
            if fem { decimals::fem(&p).into_iter().collect() } else { vec![decimals::masc(&p)] }
        }
        "fraction" => fraction::outputs(inner, fem),
        _ => Vec::new(),
    }
}

/// `ones → uno` before `WS "por"`.
fn ones_por(s: &str) -> String {
    let mut out = String::with_capacity(s.len() + 2);
    let mut i = 0;
    'outer: while i < s.len() {
        for w in ["un", "ún"] {
            if let Some(r) = s[i..].strip_prefix(w) {
                if let Some(c) = r.chars().next() {
                    if is_nemo_space(c) && r[c.len_utf8()..].starts_with("por") {
                        out.push_str("uno");
                        i += w.len();
                        continue 'outer;
                    }
                }
            }
        }
        let c = s[i..].chars().next().unwrap();
        out.push(c);
        i += c.len_utf8();
    }
    out
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut o = Outs::default();
    let push = |o: &mut Outs, s: String, c: Cost| o.push(c, || ones_por(&s).replace('\u{a0}', " "));
    // number first, units second
    if let Some((key, _)) = d.first() {
        if let Some(inner) = dict(d, 0, key) {
            if let Some(u) = s(d, 1, "units") {
                if rest_is_po(d, 2) && !u.is_empty() {
                    for (fem, t) in [(false, &*PLURAL_MASC), (true, &*PLURAL_FEM)] {
                        if !unit_ok(t, u, !fem) {
                            continue;
                        }
                        for n in numbers(key, inner, fem, true) {
                            if key == "fraction" {
                                push(&mut o, format!("{n} de {u}"), 0);
                                let half = if fem { ["media", "medias"] } else { ["medio", "medios"] };
                                if half.iter().any(|h| n.ends_with(h)) {
                                    push(&mut o, format!("{n} {u}"), cost(-0.001));
                                }
                            } else {
                                push(&mut o, format!("{n} {u}"), 0);
                            }
                        }
                    }
                }
                // alpha_num (masc cardinal/decimal, any unit)
                if rest_is_po(d, 2) && matches!(key.as_ref(), "cardinal" | "decimal") {
                    for n in numbers(key, inner, false, false) {
                        o.push(cost(0.01), || format!("{n} {u}").replace('\u{a0}', " "));
                    }
                }
            }
        }
    }
    // units first
    if let Some(u) = s(d, 0, "units") {
        if let Some((key, _)) = d.get(1) {
            if let Some(inner) = dict(d, 1, key) {
                if rest_is_po(d, 2) {
                    if u == "math" && key == "cardinal" {
                        for n in numbers(key, inner, false, false) {
                            o.push(cost(-1.0), || n);
                        }
                    }
                    if matches!(key.as_ref(), "cardinal" | "decimal") {
                        for n in numbers(key, inner, false, false) {
                            o.push(cost(0.01), || format!("{u} {n}").replace('\u{a0}', " "));
                        }
                    }
                }
            }
        }
    }
    o.best()
}

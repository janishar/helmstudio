//! `es/verbalizers/money.py` (deterministic).

use super::{Outs, decimals, rest_is_po, s};
use crate::lang::es::graph_utils::{shift_cardinal_gender, strip_cardinal_apocope};
use crate::tag::{Field, Value};

crate::tsv!(FEM, "es/money/currency_plural_fem.tsv");
crate::tsv!(MASC, "es/money/currency_plural_masc.tsv");

pub fn is_masc(w: &str) -> bool {
    MASC.contains_key(w) || MASC.rows().iter().any(|r| r.value == w)
}
pub fn is_fem(w: &str) -> bool {
    FEM.contains_key(w) || FEM.rows().iter().any(|r| r.value == w)
}

/// The `" de"` insertion after a quantity value (`cdrewrite` on the tagged text).
fn with_de(q: &str) -> String {
    format!("{q} de")
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut o = Outs::default();
    let ne = |i: usize, k: &str| s(d, i, k).filter(|v| !v.is_empty());
    // graph_integer: integer_part currency_maj (no preserve_order allowed)
    if let (Some(i), Some(m)) = (ne(0, "integer_part"), ne(1, "currency_maj")) {
        let (fem, masc) = (is_fem(m), is_masc(m));
        if d.len() == 2 {
            if fem {
                o.push(0, || format!("{} {m}", shift_cardinal_gender(i)));
            }
            if masc {
                o.push(0, || format!("{i} {m}"));
            }
        }
        // graph_integer_with_minor
        if let Some(f) = ne(2, "fractional_part") {
            // the currency_min tail: masculine then feminine
            let mut n = 3;
            let mut min: Option<(&str, bool, bool)> = None;
            if let Some(mn) = ne(3, "currency_min") {
                n = 4;
                min = Some((mn, is_masc(mn), is_fem(mn)));
            }
            let tails = |o: &mut Outs, head: &dyn Fn() -> String| match min {
                None => {}
                Some((mn, tm, tf)) => {
                    if tm {
                        o.push(0, || format!("{} {f} {mn}", head()));
                    }
                    if tf {
                        o.push(0, || format!("{} {} {mn}", head(), shift_cardinal_gender(f)));
                    }
                }
            };
            if rest_is_po(d, n) {
                if fem {
                    let head = || format!("{} {m}", shift_cardinal_gender(i));
                    if n == 3 {
                        o.push(0, || format!("{} {}", head(), shift_cardinal_gender(f)));
                    }
                    tails(&mut o, &head);
                }
                if masc {
                    let head = || format!("{i} {m}");
                    if n == 3 {
                        o.push(0, || format!("{} {}", head(), strip_cardinal_apocope(f)));
                    }
                    tails(&mut o, &head);
                }
            }
        }
    }
    // graph_decimal: decimal fields (+ preserve_order*) then currency_maj
    if let Some(p) = decimals::parts(d) {
        let mut n = p.n;
        while matches!(d.get(n), Some((k, Value::True)) if k == "preserve_order") {
            n += 1;
        }
        if let (Some(m), true) = (ne(n, "currency_maj"), n + 1 == d.len()) {
            let q = p.quantity.map(with_de);
            let p2 = decimals::Parts {
                neg: p.neg,
                integer: p.integer,
                fractional: p.fractional,
                quantity: q.as_deref(),
                n: p.n,
            };
            let with_m = |mut s: String| {
                s.push(' ');
                s.push_str(m);
                s
            };
            if is_fem(m) {
                if decimals::fem_ok(&p2) {
                    o.push(0, || with_m(decimals::fem(&p2).expect("fem_ok")));
                }
                // numbers_only_quantity: the masculine reading of a decimal with quantity
                if p2.quantity.is_some() {
                    o.push(0, || with_m(decimals::masc(&p2)));
                }
            }
            if is_masc(m) {
                o.push(0, || with_m(decimals::masc(&p2)));
            }
        }
    }
    // graph_minor: fractional_part currency_min
    if let (Some(f), Some(mn)) = (ne(0, "fractional_part"), ne(1, "currency_min")) {
        if rest_is_po(d, 2) {
            if is_fem(mn) {
                o.push(0, || format!("{} {mn}", shift_cardinal_gender(f)));
            }
            if is_masc(mn) {
                o.push(0, || format!("{f} {mn}"));
            }
        }
    }
    o.best()
}

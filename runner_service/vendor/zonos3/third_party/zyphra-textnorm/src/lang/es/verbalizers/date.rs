//! `es/verbalizers/date.py` (deterministic).

use super::{Outs, rest_is_po, s};
use crate::lang::es::graph_utils::strip_cardinal_apocope;
use crate::lang::es::taggers::date::ARTICLES;
use crate::tag::{Field, Value};
use crate::text::{Cost, cost};

fn day(v: &str) -> std::borrow::Cow<'_, str> {
    // strip_cardinal_apocope only rewrites a final "un"/"ún"
    if !v.ends_with("un") && !v.ends_with("ún") {
        return v.into();
    }
    let d = strip_cardinal_apocope(v);
    if d == "uno" { "primero".into() } else { d.into() }
}

/// The year readings `(article, rest, cost)`: each article the year words start with (kept, −0.001), then `"de"` +
/// the whole year.
fn years<'v>(v: &'v str) -> impl Iterator<Item = (&'static str, &'v str, Cost)> {
    let articled = ARTICLES.into_iter().filter_map(move |a| {
        let r = v.strip_prefix(a)?.strip_prefix(' ')?;
        (!r.is_empty()).then_some((a, r, cost(-0.001)))
    });
    articled.chain(std::iter::once(("de", v, 0)))
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut o = Outs::default();
    let nonempty = |i: usize, k: &str| s(d, i, k).filter(|v| !v.is_empty());
    // dmy
    if let (Some(dy), Some(m)) = (nonempty(0, "day"), nonempty(1, "month")) {
        if rest_is_po(d, 2) {
            o.push(0, || format!("{} de {m}", day(dy)));
        }
        if let Some(y) = nonempty(2, "year") {
            if rest_is_po(d, 3) {
                for (a, r, c) in years(y) {
                    o.push(c, || format!("{} de {m} {a} {r}", day(dy)));
                }
            }
        }
    }
    // mdy: requires an explicit preserve_order after it
    if let (Some(m), Some(dy)) = (nonempty(0, "month"), nonempty(1, "day")) {
        let po = |i: usize| matches!(d.get(i), Some((k, Value::True)) if k == "preserve_order") && rest_is_po(d, i + 1);
        if po(2) {
            o.push(0, || format!("{m} {}", day(dy)));
        }
        if let Some(y) = nonempty(2, "year") {
            if po(3) {
                for (a, r, c) in years(y) {
                    o.push(c, || format!("{m} {} {a} {r}", day(dy)));
                }
            }
        }
    }
    // roman centuries / year only
    if let Some(y) = nonempty(0, "year") {
        if rest_is_po(d, 1) {
            o.push(0, || y.to_string());
        }
    }
    o.best()
}

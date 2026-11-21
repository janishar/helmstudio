//! `en/verbalizers/verbalize_final.py`: `VerbalizeFst | WordFst` on one token body.

use crate::lang::common::{Fields, class, verbalize_name};
use crate::tag::{Dict, Value};

/// `cardinal.py`: `(negative: "true" → "minus ")? integer: "…"`.
pub fn cardinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let neg = f.opt("negative")?;
    if neg.is_some_and(|n| n != "true") {
        return None;
    }
    let i = f.take("integer")?;
    if !f.done() {
        return None;
    }
    Some(if neg.is_some() { format!("minus {i}") } else { i.to_string() })
}

/// The decimal tagger writes `fractional_part: "five"quantity: "million"` (no space before `quantity`); the
/// parser reads that as one value `five"quantity: "million`, which the verbalizer FST then reads back as the two
/// fields. Split such values (recursively) so the field readers see them.
const GLUE: &str = "\"quantity: \"";

/// Whether a `fractional_part` value holds a glued quantity (a value without `"` cannot).
fn glued(s: &str) -> bool {
    s.as_bytes().contains(&b'"') && s.contains(GLUE)
}

fn has_glued(d: &Dict) -> bool {
    d.iter().any(|(k, v)| match v {
        Value::Str(s) => k == "fractional_part" && glued(s),
        Value::Dict(inner) => has_glued(inner),
        _ => false,
    })
}

fn split_glued<'a>(d: &Dict<'a>) -> Option<Dict<'a>> {
    if !has_glued(d) {
        return None;
    }
    let mut out: Dict = Vec::with_capacity(d.len() + 1);
    let mut changed = false;
    for (k, v) in d {
        match v {
            Value::Str(s) if k == "fractional_part" && glued(s) => {
                let (a, b) = s.split_once(GLUE)?;
                out.push((k.clone(), Value::Str(a.to_string().into())));
                out.push(("quantity".into(), Value::Str(b.to_string().into())));
                changed = true;
            }
            Value::Dict(inner) => match split_glued(inner) {
                Some(n) => {
                    out.push((k.clone(), Value::Dict(n)));
                    changed = true;
                }
                None => out.push((k.clone(), v.clone())),
            },
            _ => out.push((k.clone(), v.clone())),
        }
    }
    changed.then_some(out)
}

pub fn verbalize(token: &Dict) -> Option<String> {
    if let Some(t) = split_glued(token) {
        return verbalize_inner(&t);
    }
    verbalize_inner(token)
}

fn verbalize_inner(token: &Dict) -> Option<String> {
    let (k, v) = class(token)?;
    if k == "name" {
        return verbalize_name(token);
    }
    let Value::Dict(d) = v else { return None };
    let s = match k {
        "cardinal" => cardinal(d)?,
        "date" => super::date::date(d)?,
        "time" => super::time::time(d)?,
        "money" => super::money::money(d)?,
        "electronic" => super::electronic::electronic(d)?,
        "measure" => super::measure::measure(d)?,
        "telephone" => super::telephone::telephone(d)?,
        "decimal" => super::decimal::decimal(d)?,
        "fraction" => super::decimal::fraction(d)?,
        "ordinal" => super::ordinal::ordinal(d)?,
        _ => return None,
    };
    // UTF-8 bytes C2 A0 occur only as NBSP (a lead byte C2 is never a continuation byte)
    Some(if s.as_bytes().windows(2).any(|w| w == [0xc2, 0xa0]) { s.replace('\u{a0}', " ") } else { s })
}

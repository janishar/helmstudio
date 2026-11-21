//! `en/verbalizers/measure.py` (deterministic).

use crate::lang::common::Fields;
use crate::lang::en::verbalizers::decimal::{fraction_graph, numbers};
use crate::tag::{Dict, Field, Value};

fn ne(s: &str) -> Option<&str> {
    (!s.is_empty()).then_some(s)
}

/// `graph_cardinal` / `graph_decimal` / `graph_fraction` on a nested dict.
fn number(k: &str, d: &Dict, allow_fraction: bool) -> Option<String> {
    let mut f = Fields::new(d);
    let s = match k {
        "cardinal" => {
            let neg = f.opt("negative")?;
            if neg.is_some_and(|n| n != "true") {
                return None;
            }
            let i = f.take("integer")?;
            if neg.is_some() { format!("minus {i}") } else { i.to_string() }
        }
        "decimal" => numbers(&mut f)?,
        "fraction" if allow_fraction => fraction_graph(&mut f)?,
        _ => return None,
    };
    f.done().then_some(s)
}

fn unit_ok(u: &str) -> Option<&str> {
    (u != "address" && u != "math").then(|| ne(u)).flatten()
}

pub fn measure(d: &Dict) -> Option<String> {
    let mut it = d.iter();
    let (k0, v0) = it.next()?;
    let rest: Vec<&Field> = it.collect();
    let po_tail = |r: &[&Field]| r.iter().all(|(k, v)| k == "preserve_order" && *v == Value::True) && r.len() <= 1;
    match (k0.as_ref(), v0) {
        (k, Value::Dict(inner)) => {
            // number + unit, or the `cardinal { integer: "-" }` + unit form
            let [(uk, Value::Str(u)), tail @ ..] = rest.as_slice() else { return None };
            if uk != "units" {
                return None;
            }
            if k == "cardinal" && matches!(inner.as_slice(), [(ik, Value::Str(iv))] if ik == "integer" && iv == "-") {
                if po_tail(tail) {
                    return Some(unit_ok(u)?.to_string());
                }
            }
            if !tail.is_empty() {
                return None;
            }
            let n = number(k, inner, true)?;
            Some(format!("{n} {}", unit_ok(u)?))
        }
        ("units", Value::Str(u)) => {
            let [(nk, Value::Dict(inner)), tail @ ..] = rest.as_slice() else { return None };
            if !po_tail(tail) {
                return None;
            }
            match u.as_ref() {
                "address" | "math" => {
                    if nk != "cardinal" {
                        return None;
                    }
                    number("cardinal", inner, false)
                }
                _ => {
                    let n = number(nk, inner, false)?;
                    Some(format!("{} {n}", ne(u)?))
                }
            }
        }
        _ => None,
    }
}

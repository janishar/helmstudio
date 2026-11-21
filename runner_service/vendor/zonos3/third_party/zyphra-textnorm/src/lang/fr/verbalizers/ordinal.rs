//! `fr/verbalizers/ordinal.py`: `OrdinalFst` (`strip_dashes=False`). `graph` is shared with the fraction verbalizer.

use crate::text::{Cost, cost};

crate::tsv!(IRREGULAR, "fr/ordinals/irregular_numbers.tsv");

/// `irregular_numbers`: `(NOT_QUOTE+ " ")* key → prefix + value` on the whole integer.
fn irregular(integer: &str) -> Vec<(String, Cost)> {
    let mut v = Vec::new();
    for r in IRREGULAR.rows() {
        let Some(prefix) = integer.strip_suffix(r.key.as_str()) else {
            continue;
        };
        // "" or (NOT_QUOTE+ " ")+: non-empty text before a final space
        if prefix.is_empty() || (prefix.len() > 1 && prefix.ends_with(' ')) {
            v.push((format!("{prefix}{}", r.value), r.cost));
        }
    }
    v
}

/// Every output (with cost) of `graph` on `integer` + `morphosyntactic_features`.
pub fn outputs(integer: &str, morph: &str) -> Vec<(String, Cost)> {
    let mut v = Vec::new();
    let irr = irregular(integer);
    if irr.is_empty() {
        if !morph.is_empty() {
            v.push((format!("{integer}{morph}"), 0));
            if integer == "un" {
                v.push((format!("prem{morph}"), cost(-0.0001)));
            }
        }
    } else if morph == "ième" || morph == "ièmes" {
        let s = if morph == "ièmes" { "s" } else { "" };
        for (o, c) in irr {
            v.push((format!("{o}{s}"), c));
        }
    }
    v
}

/// The cheapest output (ties: first).
pub fn best(v: Vec<(String, Cost)>) -> Option<String> {
    let mut best: Option<(String, Cost)> = None;
    for (o, c) in v {
        if best.as_ref().is_none_or(|(_, b)| c < *b) {
            best = Some((o, c));
        }
    }
    best.map(|(o, _)| o)
}

pub fn verbalize(fields: &crate::tag::Dict) -> Option<String> {
    let mut f = crate::lang::common::Fields::new(fields);
    let integer = f.take("integer")?;
    let morph = f.take("morphosyntactic_features")?;
    f.done().then_some(())?;
    best(outputs(integer, morph))
}

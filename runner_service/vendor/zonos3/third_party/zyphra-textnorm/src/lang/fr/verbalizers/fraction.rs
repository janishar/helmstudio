//! `fr/verbalizers/fraction.py`: `FractionFst`.

use super::ordinal;
use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::{Cost, cost};

crate::tsv!(IRREGULAR_DENOMINATORS, "fr/fractions/irregular_denominators.tsv");

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let mut head = String::new();
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "true" {
            return None;
        }
        head.push_str("moins ");
    }
    if f.peek_key() == Some("integer_part") {
        // closure(NOT_QUOTE) may be empty, but the parser maps "" to None which never reaches here
        head.push_str(f.take("integer_part")?);
        head.push_str(" et ");
    }
    let numerator = numerator(&mut f)?;
    let denominator = f.take("denominator")?;
    let morph = f.take("morphosyntactic_features")?;
    if !f.done() {
        return None;
    }
    let mut dens: Vec<(String, Cost)> = Vec::new();
    for (o, c) in ordinal::outputs(denominator, morph) {
        for r in IRREGULAR_DENOMINATORS.exact(&o) {
            dens.push((r.value.clone(), c + r.cost + cost(-0.01)));
        }
        dens.push((o, c));
    }
    let mut all: Vec<(String, Cost)> = Vec::new();
    for (d, c) in dens {
        let s = format!("{head}{numerator} {d}");
        if let Some(p) = s.strip_suffix("et un demi") {
            all.push((format!("{p}et demi"), c + cost(-0.01)));
        }
        all.push((s, c));
    }
    ordinal::best(all)
}

fn numerator<'t>(f: &mut Fields<'t, '_>) -> Option<&'t str> {
    f.take("numerator")
}

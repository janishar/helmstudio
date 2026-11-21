//! `pt/verbalizers/fraction.py`. The `mixed_um_meio` branch (−0.01) deletes `numerator: "um" " `, a string no
//! serialized ordering contains, so it never applies.

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;

crate::labels!(SPECIALS, "pt/fractions/specials.tsv");

struct Spec {
    connector: String,
    minus: String,
    plural: String,
    avos: String,
    between: String,
}

static SPEC: LazyLock<Spec> = LazyLock::new(|| {
    let get = |k: &str, d: &str| {
        SPECIALS.iter().find(|r| r.len() >= 2 && r[0] == k).map(|r| r[1].clone()).unwrap_or_else(|| d.to_string())
    };
    Spec {
        connector: get("connector", " e ").trim().to_string(),
        minus: get("minus", "menos ").trim_end().to_string(),
        plural: get("plural_suffix", "s"),
        avos: get("avos_suffix", " avos").trim().to_string(),
        between: get("avos_between", "").trim().to_string(),
    }
});

/// `inner_graph`, consuming the fields.
pub fn inner(f: &mut Fields) -> Option<String> {
    let s = &*SPEC;
    let mut out = String::new();
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "true" {
            return None;
        }
        out.push_str(&s.minus);
        out.push(' ');
    }
    if let Some(i) = f.opt("integer_part")? {
        out.push_str(i);
        out.push(' ');
        out.push_str(&s.connector);
        out.push_str("  ");
    }
    let num = f.take("numerator")?;
    let den = f.take("denominator")?;
    let feat = f.take("morphosyntactic_features")?;
    out.push_str(num);
    match feat {
        "ordinal" => {
            out.push(' ');
            out.push_str(den);
            if num != "um" {
                out.push_str(&s.plural);
            }
        }
        "avos" => {
            out.push(' ');
            if !s.between.is_empty() {
                out.push_str(&s.between);
                out.push(' ');
            }
            out.push_str(den);
            out.push(' ');
            out.push_str(&s.avos);
        }
        _ => return None,
    }
    Some(out)
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let out = inner(&mut f)?;
    f.done().then_some(out)
}

//! `es/verbalizers/fraction.py` (deterministic).

use super::{Outs, is_key, s};
use crate::lang::es::graph_utils::{shift_cardinal_gender, strip_cardinal_apocope};
use crate::tag::{Field, Value};

struct F<'a> {
    neg: bool,
    integer: Option<&'a str>,
    num: &'a str,
    den: &'a str,
    morph: Option<&'a str>,
}

fn parse<'t>(d: &'t [Field]) -> Option<F<'t>> {
    let mut i = 0;
    let neg = is_key(d, 0, "negative");
    if neg {
        if s(d, 0, "negative")? != "true" {
            return None;
        }
        i += 1;
    }
    let integer = if is_key(d, i, "integer_part") {
        i += 1;
        Some(s(d, i - 1, "integer_part")?)
    } else {
        None
    };
    let num = s(d, i, "numerator")?;
    let den = s(d, i + 1, "denominator")?;
    i += 2;
    let morph = if is_key(d, i, "morphosyntactic_features") {
        i += 1;
        Some(s(d, i - 1, "morphosyntactic_features")?)
    } else {
        None
    };
    (i == d.len()).then_some(F { neg, integer, num, den, morph })
}

fn strip_accent_char(c: char) -> char {
    match c {
        'á' => 'a',
        'é' => 'e',
        'í' => 'i',
        'ó' => 'o',
        'ú' => 'u',
        c => c,
    }
}

/// `merge_into_single_word`: " y " → "i", spaces deleted, accents dropped before a later avo/ava/ésimo/ésima,
/// then "aa" → "a", "oo" → "o".
fn merge(s: &str) -> String {
    let s = s.replace(" y ", "i");
    let s: String = s.chars().filter(|c| !crate::text::is_nemo_space(*c)).collect();
    let chars: Vec<char> = s.chars().collect();
    let mut out = String::with_capacity(s.len());
    for (i, &c) in chars.iter().enumerate() {
        let rest: String = chars[i + 1..].iter().collect();
        let ctx = ["avo", "ava", "ésimo", "ésima"].iter().any(|t| rest.contains(t));
        out.push(if ctx { strip_accent_char(c) } else { c });
    }
    let mut res = String::with_capacity(out.len());
    let oc: Vec<char> = out.chars().collect();
    let mut i = 0;
    while i < oc.len() {
        if i + 1 < oc.len() && oc[i] == oc[i + 1] && (oc[i] == 'a' || oc[i] == 'o') {
            res.push(oc[i]);
            i += 2;
        } else {
            res.push(oc[i]);
            i += 1;
        }
    }
    res
}

fn den_singular(f: &F) -> Option<String> {
    match f.morph? {
        "add_root" => Some(format!("{} avo", f.den)),
        "ordinal" => Some(f.den.to_string()),
        _ => None,
    }
}

/// Fraction outputs (without integer/sign) for the masculine (`fem = false`) or feminine graph.
fn fraction(f: &F, fem: bool) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(ds) = den_singular(f) {
        if f.num == "un" {
            let w = format!("un {}", merge(&ds));
            out.push(w.replace("un medio", "medio"));
            if fem && ds == "medio" {
                out.push("media".into());
            }
        } else {
            out.push(format!("{} {}", f.num, merge(&format!("{ds}s"))));
            if fem && format!("{ds}s") == "medios" {
                out.push(format!("{} medias", shift_cardinal_gender(f.num)));
            }
        }
    } else if f.morph.is_none() {
        out.push(format!("{} sobre {}", strip_cardinal_apocope(f.num), strip_cardinal_apocope(f.den)));
    }
    out
}

/// `graph_masc` / `graph_fem` outputs over a full fraction field list.
pub fn outputs(d: &[Field], fem: bool) -> Vec<String> {
    let Some(f) = parse(d) else { return Vec::new() };
    let pre = match f.integer {
        Some(i) => {
            let i = strip_cardinal_apocope(i);
            let i = if fem { shift_cardinal_gender(&i) } else { i };
            format!("{i} y ")
        }
        None => String::new(),
    };
    let sign = if f.neg { "menos " } else { "" };
    fraction(&f, fem).into_iter().map(|x| format!("{sign}{pre}{x}")).collect()
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut o = Outs::default();
    // equal-cost tie fitted to the oracle: "medias" wins when the numerator ends in "un" (`71/2` → `setenta y una
    // medias`), the masculine otherwise (`-15/2` → `menos quince medios`, `3 1/2` → `tres y medio`)
    let fem = outputs(d, true);
    let un_num = d.iter().any(|(k, v)| k == "numerator" && matches!(v, Value::Str(s) if s.ends_with("un")));
    for x in fem.iter().filter(|x| un_num && x.ends_with("medias")) {
        o.push(0, || x.clone());
    }
    for x in outputs(d, false).into_iter().chain(fem) {
        o.push(0, || x);
    }
    o.best()
}

#[cfg(test)]
mod tests {
    use crate::lang::es::test_util::normalize;

    #[test]
    fn medias_after_un() {
        // alternatives: "setenta y una medias" (feminine) vs "setenta y un medios" (masculine)
        assert_eq!(normalize("71/2"), "setenta y una medias");
    }

    #[test]
    fn medios_otherwise() {
        // alternatives: "menos quince medios" (masculine) vs "menos quince medias" (feminine)
        assert_eq!(normalize("-15/2"), "menos quince medios");
        // alternatives: "tres y medio" vs "tres y media"
        assert_eq!(normalize("3 1/2"), "tres y medio");
    }
}

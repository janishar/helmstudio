//! `pt/verbalizers/money.py`, then `cdrewrite(insert("de "), scale + " ", currency, Σ*)`.

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;

crate::labels!(SCALES, "pt/numbers/scales.tsv");
crate::labels!(MAJ_PLURAL, "pt/money/currency_major_plural.tsv");

/// (scale words each + " ", currency plural words)
static WORDS: LazyLock<(Vec<String>, Vec<String>)> = LazyLock::new(|| {
    let mut scales = Vec::new();
    for r in SCALES.iter().skip(1) {
        if r.len() < 2 {
            continue;
        }
        let (one, plural) = (r[0].trim(), r[1].trim());
        if one.is_empty() || plural.is_empty() {
            continue;
        }
        scales.push(format!("{} ", one.split_whitespace().last().unwrap()));
        scales.push(format!("{plural} "));
    }
    let currencies = MAJ_PLURAL
        .iter()
        .filter(|r| r.len() >= 2 && !r[1].trim().is_empty())
        .map(|r| r[1].trim().to_string())
        .collect();
    (scales, currencies)
});

fn insert_de(s: &str) -> String {
    let (scales, currencies) = &*WORDS;
    let mut out = String::with_capacity(s.len() + 6);
    for (i, c) in s.char_indices() {
        if scales.iter().any(|w| s[..i].ends_with(w.as_str()))
            && currencies.iter().any(|w| s[i..].starts_with(w.as_str()))
        {
            out.push_str("de ");
        }
        out.push(c);
    }
    out
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let out = match f.peek_key()? {
        "fractional_part" => {
            let frac = f.take("fractional_part")?;
            let min = f.take("currency_min")?;
            if !f.preserve_order() {
                return None;
            }
            format!("{frac} {min}")
        }
        "integer_part"
            if f.peek_key().is_some() && !d.iter().any(|(k, _)| k == "fractional_part" || k == "quantity") =>
        {
            let int = f.take("integer_part")?;
            format!("{int} {}", f.take("currency_maj")?)
        }
        "integer_part" if d.iter().any(|(k, _)| k == "currency_min") => {
            let int = f.take("integer_part")?;
            let maj = f.take("currency_maj")?;
            let frac = f.take("fractional_part")?;
            let min = f.take("currency_min")?;
            if !f.preserve_order() {
                return None;
            }
            format!("{int} {maj} e {frac} {min}")
        }
        _ => {
            let num = super::decimal::numbers(&mut f)?;
            format!("{num} {}", f.take("currency_maj")?)
        }
    };
    f.done().then(|| insert_de(&out))
}

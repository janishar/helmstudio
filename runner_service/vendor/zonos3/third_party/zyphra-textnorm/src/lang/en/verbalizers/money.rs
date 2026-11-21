//! `en/verbalizers/money.py` (deterministic).

use crate::lang::common::Fields;
use crate::lang::en::verbalizers::decimal::numbers;
use crate::tag::Dict;

crate::tsv!(PER_UNITS, "en/money/per_unit.tsv");

fn ne(s: &str) -> Option<&str> {
    (!s.is_empty()).then_some(s)
}

fn body(d: &[crate::tag::Field]) -> Option<String> {
    // graph_integer | graph_integer_with_minor | graph_minor (field order fixed by the FST)
    let mut f = Fields::new(d);
    let alt = (|| {
        let s = match f.peek_key()? {
            "fractional_part" => {
                let fr = ne(f.take("fractional_part")?)?;
                let m = ne(f.take("currency_min")?)?;
                f.preserve_order();
                format!("{fr} {m}")
            }
            "integer_part" => {
                let i = ne(f.take("integer_part")?)?;
                let m = ne(f.take("currency_maj")?)?;
                if f.peek_key() == Some("fractional_part") {
                    let fr = ne(f.take("fractional_part")?)?;
                    let mn = ne(f.take("currency_min")?)?;
                    f.preserve_order();
                    format!("{i} {m} {fr} {mn}")
                } else {
                    format!("{i} {m}")
                }
            }
            _ => return None,
        };
        f.done().then_some(s)
    })();
    if alt.is_some() {
        return alt;
    }
    // graph_decimal: decimal.numbers + maj
    let mut g = Fields::new(d);
    let n = numbers(&mut g)?;
    let m = ne(g.take("currency_maj")?)?;
    g.done().then(|| format!("{n} {m}"))
}

pub fn money(d: &Dict) -> Option<String> {
    // optional trailing morphosyntactic_features (its value must be a per-unit output; the FST deletes `" `
    // with a space that the permutation serialization always provides)
    if let Some((k, v)) = d.last() {
        if k == "morphosyntactic_features" {
            let crate::tag::Value::Str(p) = v else { return None };
            if !PER_UNITS.rows().iter().any(|r| r.value == *p) {
                return None;
            }
            return Some(format!("{} {p}", body(&d[..d.len() - 1])?));
        }
    }
    body(d)
}

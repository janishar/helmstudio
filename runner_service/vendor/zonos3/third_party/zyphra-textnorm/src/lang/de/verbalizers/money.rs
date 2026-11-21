//! `de/verbalizers/money.py`: `graph_integer | graph_integer_with_minor | graph_decimal | graph_minor`.

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::Cost;

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let out = match f.peek_key()? {
        "integer_part" => {
            let i = f.take("integer_part")?;
            let maj = f.take("currency_maj")?;
            match f.opt("fractional_part")? {
                None => format!("{i} {maj}"),
                Some(fr) => {
                    let min = f.opt("currency_min")?;
                    if !f.preserve_order() {
                        return None;
                    }
                    match min {
                        Some(m) => format!("{i} {maj} {fr} {m}"),
                        None => format!("{i} {maj} {fr}"),
                    }
                }
            }
        }
        "fractional_part" => {
            let fr = f.take("fractional_part")?;
            let m = f.take("currency_min")?;
            if !f.preserve_order() {
                return None;
            }
            format!("{fr} {m}")
        }
        "decimal" => {
            let inner = f.dict("decimal")?;
            let (dec, _) = super::decimal::verbalize(inner)?;
            format!("{dec} {}", f.take("currency_maj")?)
        }
        _ => return None,
    };
    f.done().then_some((out, 0))
}

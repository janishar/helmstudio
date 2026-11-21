//! `pt/verbalizers/measure.py`: number token, then `units` (feminine cardinal before `hora`/`horas`).

use crate::lang::common::Fields;
use crate::tag::Dict;

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out;
    let hours = |u: &str| u == "hora" || u == "horas";
    match f.peek_key()? {
        "cardinal" => {
            let inner = f.dict("cardinal")?;
            let units = f.take("units")?;
            let mut g = Fields::new(inner);
            out = super::cardinal::number(&mut g, hours(units))?;
            if !g.done() {
                return None;
            }
            out.push(' ');
            out.push_str(units);
        }
        "decimal" => {
            let inner = f.dict("decimal")?;
            let mut g = Fields::new(inner);
            out = super::decimal::numbers(&mut g)?;
            if !g.done() {
                return None;
            }
            out.push(' ');
            out.push_str(f.take("units")?);
        }
        "fraction" => {
            let inner = f.dict("fraction")?;
            let mut g = Fields::new(inner);
            out = super::fraction::inner(&mut g)?;
            if !g.done() {
                return None;
            }
            out.push(' ');
            out.push_str(f.take("units")?);
        }
        _ => return None,
    }
    f.preserve_order();
    f.done().then_some(out)
}

//! `de/verbalizers/measure.py`: `(cardinal | decimal | fraction) " " unit | unit " " (cardinal | decimal)`.

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::{Cost, nbsp_to_space};

fn number(f: &mut Fields, allow_fraction: bool) -> Option<(String, Cost)> {
    match f.peek_key()? {
        "cardinal" => super::cardinal::verbalize(f.dict("cardinal")?),
        "decimal" => super::decimal::verbalize(f.dict("decimal")?),
        "fraction" if allow_fraction => super::fraction::verbalize(f.dict("fraction")?),
        _ => None,
    }
}

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let (out, c) = if f.peek_key()? == "units" {
        let u = f.take("units")?;
        let (n, c) = number(&mut f, false)?;
        (format!("{} {n}", nbsp_to_space(u)), c)
    } else {
        let (n, c) = number(&mut f, true)?;
        let u = f.take("units")?;
        (format!("{n} {}", nbsp_to_space(u)), c)
    };
    while f.preserve_order() {}
    f.done().then_some((out, c))
}

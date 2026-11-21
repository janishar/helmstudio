//! `it/verbalizers/measure.py`: `(cardinal | decimal) " " unit | unit " " (cardinal | decimal)`.

use crate::lang::common::Fields;
use crate::tag::Dict;

fn number(f: &mut Fields) -> Option<String> {
    match f.peek_key()? {
        "cardinal" => super::cardinal::verbalize(&mut Fields::new(f.dict("cardinal")?)),
        "decimal" => super::decimal::verbalize(&mut Fields::new(f.dict("decimal")?)),
        _ => None,
    }
}

fn unit(f: &mut Fields) -> Option<String> {
    // closure(NEMO_NOT_QUOTE): an empty value never parses to a string, so any quote-free string
    f.take("units").map(str::to_string)
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let out = if matches!(fields.first(), Some((k, _)) if k == "units") {
        let u = unit(&mut f)?;
        let n = number(&mut f)?;
        format!("{u} {n}")
    } else {
        let n = number(&mut f)?;
        let u = unit(&mut f)?;
        format!("{n} {u}")
    };
    super::end(&mut f).then_some(out)
}

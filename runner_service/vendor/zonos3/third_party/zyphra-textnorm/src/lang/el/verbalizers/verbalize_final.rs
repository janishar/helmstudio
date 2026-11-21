use crate::lang::common::Fields;
use crate::tag::{Dict, Value};

pub(in crate::lang::el) fn verbalize(token: &Dict) -> Option<String> {
    if let Some(text) = super::word::verbalize(token) {
        return Some(text);
    }
    let [(key, Value::Dict(d))] = token.as_slice() else {
        return None;
    };
    if key != "date" {
        return None;
    }
    let mut f = Fields::new(d);
    let day = f.take("day")?;
    let month = f.take("month")?;
    let year = f.take("year")?;
    if !f.done() || day.is_empty() || month.is_empty() || year.is_empty() {
        return None;
    }
    Some(format!("{day} {month} {year}").replace('\u{a0}', " "))
}

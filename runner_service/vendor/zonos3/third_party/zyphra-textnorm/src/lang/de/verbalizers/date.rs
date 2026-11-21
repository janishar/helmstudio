//! `de/verbalizers/date.py`: `day " " month (" " year)? | month " " year | year`.

use super::ordinal::stem;
use crate::lang::common::Fields;
use crate::lang::de::taggers::date::is_month_name;
use crate::tag::Dict;
use crate::text::Cost;

fn ordinal_ter(s: &str) -> Option<String> {
    let mut o = stem(s)?;
    o.push_str("ter");
    Some(o)
}

fn month(m: &str) -> Option<String> {
    if is_month_name(m) { Some(m.to_string()) } else { ordinal_ter(m) }
}

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let out = match f.peek_key()? {
        "day" => {
            // the day's stem rewrite is obligatory: a day without a matching ending has no path
            let day = f.take("day")?;
            let day = ordinal_ter(day).or_else(|| Some(format!("{day}ter")))?;
            let m = month(f.take("month")?)?;
            match f.opt("year")? {
                Some(y) => format!("{day} {m} {y}"),
                None => format!("{day} {m}"),
            }
        }
        "month" => {
            let m = month(f.take("month")?)?;
            format!("{m} {}", f.take("year")?)
        }
        "year" => f.take("year")?.to_string(),
        _ => return None,
    };
    while f.preserve_order() {}
    f.done().then_some((out, 0))
}

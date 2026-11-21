//! `fr/verbalizers/date.py`: `day " " month (" " year)? | month " " year | year`, then `delete_preserve_order`.

use crate::lang::common::Fields;
use crate::tag::Dict;

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let out = match f.peek_key()? {
        "day" => {
            let d = f.take("day")?;
            let m = f.take("month")?;
            match f.opt("year")? {
                Some(y) => format!("{d} {m} {y}"),
                None => format!("{d} {m}"),
            }
        }
        "month" => {
            let m = f.take("month")?;
            let y = f.take("year")?;
            format!("{m} {y}")
        }
        "year" => f.take("year")?.to_string(),
        _ => return None,
    };
    while f.preserve_order() {}
    f.done().then_some(out)
}

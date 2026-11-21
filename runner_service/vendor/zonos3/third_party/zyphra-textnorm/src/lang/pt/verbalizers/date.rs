//! `pt/verbalizers/date.py`: day de month de year.

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;

crate::labels!(PHRASES, "pt/date/verbal_phrases.tsv");

static PREP: LazyLock<String> = LazyLock::new(|| {
    PHRASES
        .iter()
        .find(|r| r.len() >= 2 && r[0].trim() == "preposition")
        .map(|r| r[1].trim().to_string())
        .unwrap_or_else(|| "de".into())
});

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let (day, month, year) = (f.take("day")?, f.take("month")?, f.take("year")?);
    f.preserve_order();
    if !f.done() {
        return None;
    }
    let p = PREP.as_str();
    let mut out = String::with_capacity(day.len() + month.len() + year.len() + 2 * p.len() + 6);
    for (i, part) in [day, month, year].into_iter().enumerate() {
        if i > 0 {
            out.push(' ');
            out.push_str(p);
            out.push_str("  ");
        }
        out.push_str(part);
    }
    Some(out)
}

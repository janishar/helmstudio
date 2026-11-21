//! `en/verbalizers/date.py` (deterministic).

use crate::lang::common::Fields;
use crate::lang::en::verbalizers::ordinal::suffix;
use crate::tag::Dict;

fn nonempty(s: &str) -> Option<&str> {
    (!s.is_empty()).then_some(s)
}

/// `year` field value: `NOT_QUOTE+` then `delete_space` before the closing quote (trailing spaces dropped).
fn year_value(s: &str) -> Option<&str> {
    let t = s.trim_end_matches([' ', '\t', '\n', '\r', '\u{a0}']);
    nonempty(t)
}

fn finish(f: &mut Fields, out: String) -> Option<String> {
    f.preserve_order();
    f.done().then_some(out)
}

pub fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    match f.peek_key()? {
        "month" => {
            // graph_mdy wins over dmy by priority union whenever it accepts
            let m = nonempty(f.take("month")?)?;
            let mut out = m.to_string();
            if f.peek_key() == Some("day") {
                let day = nonempty(f.take("day")?)?;
                out.push(' ');
                out.push_str(&suffix(day));
            }
            if f.peek_key() == Some("year") {
                let y = year_value(f.take("year")?)?;
                out.push(' ');
                out.push_str(y);
            }
            finish(&mut f, out)
        }
        "day" => {
            let day = nonempty(f.take("day")?)?;
            let m = nonempty(f.take("month")?)?;
            let mut out = format!("the {} of {m}", suffix(day));
            if f.peek_key() == Some("year") {
                let y = year_value(f.take("year")?)?;
                out.push(' ');
                out.push_str(y);
            }
            finish(&mut f, out)
        }
        "year" => {
            let y = year_value(f.take("year")?)?;
            finish(&mut f, y.to_string())
        }
        "text" => {
            let t = nonempty(f.take("text")?)?;
            let mut out = format!("the {t} of ");
            if f.peek_key() == Some("year") {
                let y = year_value(f.take("year")?)?;
                out.push(' ');
                out.push_str(y);
            }
            finish(&mut f, out)
        }
        _ => None,
    }
}

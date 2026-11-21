//! Composite arithmetic, ratio, range and clock expressions.
use super::{datetime, number, numeric::Reading};
use crate::text::space_to_nbsp;

fn emit(s: String, weight: i64, out: &mut Vec<Reading>) {
    out.push((format!("name: \"{}\"", space_to_nbsp(&s)), 1_030_000 + weight, 1));
}
fn short(s: &str) -> Option<(String, i64)> {
    if s.is_empty() || s.chars().count() > 6 || !s.chars().all(|c| number::digit(c).is_some()) {
        None
    } else {
        number::final_cardinal(s)
    }
}
fn trim_one_left(s: &str) -> &str {
    s.strip_prefix(' ').unwrap_or(s)
}
fn trim_one_right(s: &str) -> &str {
    s.strip_suffix(' ').unwrap_or(s)
}
fn spoken_clock(s: &str) -> Option<(String, i64)> {
    let parts: Vec<_> = s.split(':').collect();
    if !(parts.len() == 2 || parts.len() == 3)
        || !(1..=2).contains(&parts[0].chars().count())
        || parts[1..].iter().any(|p| p.chars().count() != 2)
        || parts.iter().any(|p| !p.chars().all(|c| number::digit(c).is_some()))
    {
        return None;
    }
    let mut rs = Vec::new();
    datetime::time(s, &mut rs);
    let r = rs.into_iter().min_by_key(|r| r.1)?;
    let tagged = format!("tokens {{ {} }}", r.0);
    let parsed = crate::tag::parse(&tagged).ok()?;
    let (_, crate::tag::Value::Dict(body)) = crate::lang::common::class(parsed.first()?)? else { return None };
    Some((super::verbalize::one(body)?, r.1 - 1_020_000))
}

pub fn classify(s: &str, out: &mut Vec<Reading>) {
    for (prefix, spoken) in [("©", "പകർപ്പവകാശം"), ("§", "വിഭാഗം")] {
        if let Some(rest) = s.strip_prefix(prefix) {
            if let Some((n, c)) = short(trim_one_left(rest)) {
                emit(format!("{spoken} {n}"), c, out)
            }
        }
    }
    for sep in ["-", "–", "—"] {
        if let Some((a, b)) = s.split_once(sep) {
            let (a, b) = (trim_one_right(a), trim_one_left(b));
            if let (Some((a, ac)), Some((b, bc))) = (short(a), short(b)) {
                emit(format!("{a} മുതൽ {b} വരെ"), ac + bc, out)
            }
            if a.contains(':') && b.contains(':') {
                if let (Some((a, ac)), Some((b, bc))) = (spoken_clock(a), spoken_clock(b)) {
                    emit(format!("{a} മുതൽ {b} വരെ"), ac + bc, out)
                }
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        if let (Some((a, ac)), Some((b, bc))) = (short(a), short(b)) {
            let a = if let Some(a) = a.strip_suffix('്') { format!("{a}േ") } else { a };
            emit(format!("{a} {b}"), 100_000 + ac + bc, out)
        }
    }
    for (op, word) in [
        ("+", "അധികം"),
        ("-", "കുറയ്ക്കണം"),
        ("−", "കുറയ്ക്കണം"),
        ("x", "ഗുണം"),
        ("*", "ഗുണം"),
        ("×", "ഗുണം"),
        ("/", "ഹരണം"),
        ("÷", "ഹരണം"),
    ] {
        let Some((a, rest)) = s.split_once(op) else { continue };
        let a = trim_one_right(a);
        let rest = trim_one_left(rest);
        for eq in ["=", "സമം"] {
            if let Some((b, c)) = rest.split_once(eq) {
                if let (Some((a, ac)), Some((b, bc)), Some((c, cc))) =
                    (short(a), short(trim_one_right(b)), short(trim_one_left(c)))
                {
                    emit(format!("{a} {word} {b} സമം {c}"), ac + bc + cc, out)
                }
            }
        }
        if matches!(op, "x" | "*" | "×") {
            if let (Some((a, ac)), Some((b, bc))) = (short(a), short(rest)) {
                emit(format!("{a} {word} {b}"), ac + bc, out)
            }
        }
    }
}

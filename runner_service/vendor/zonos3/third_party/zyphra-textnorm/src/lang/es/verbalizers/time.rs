//! `es/verbalizers/time.py` (deterministic).

use super::{is_key, rest_is_po, s};
use crate::tag::Field;

crate::tsv!(ALT_MINUTES, "es/time/alt_minutes.tsv");
crate::tsv!(MORNING, "es/time/morning_times.tsv");
crate::tsv!(AFTERNOON, "es/time/afternoon_times.tsv");
crate::tsv!(EVENING, "es/time/evening_times.tsv");

fn minute(m: &str) -> &str {
    ALT_MINUTES.exact(m).next().map_or(m, |r| r.value.as_str())
}

/// The first `hour + suffix` phrase (every phrase costs 0, so the first in union order is the output).
fn phrase(h: &str, suf: &str) -> Option<&'static str> {
    if MORNING.contains_key(h) && suf == "am" {
        return Some("de la mañana");
    }
    if AFTERNOON.contains_key(h) && suf == "pm" {
        return Some("de la tarde");
    }
    if EVENING.contains_key(h) && suf == "pm" {
        return Some("de la noche");
    }
    match (h, suf) {
        ("doce", "pm") => Some("del mediodía"),
        ("doce", "am") => Some("de la noche"),
        _ => None,
    }
}

pub fn verbalize(d: &[Field]) -> Option<String> {
    let ne = |i: usize, k: &str| s(d, i, k).filter(|v| !v.is_empty());
    let h = ne(0, "hours")?;
    let mut i = 1;
    // every body costs 0: the output is the first one the union accepts (`None`: no body)
    let body: Option<String>;
    if let (Some(m), Some(sec)) = (ne(1, "minutes"), ne(2, "seconds")) {
        body = Some(format!("{h} horas {} minutos y {sec} segundos", minute(m)));
        i = 3;
    } else if let Some(m) = ne(1, "minutes") {
        i = 2;
        if let Some(suf) = ne(2, "suffix") {
            i = 3;
            body = phrase(h, suf).map(|p| format!("{h} y {} {p}", minute(m)));
        } else {
            body = Some(format!("{h} y {}", minute(m)));
        }
    } else if let Some(suf) = ne(1, "suffix") {
        i = 2;
        body = phrase(h, suf).map(|p| format!("{h} {p}"));
    } else {
        body = Some(h.to_string());
    }
    let mut zone = None;
    if is_key(d, i, "zone") {
        zone = Some(ne(i, "zone")?);
        i += 1;
    }
    if !rest_is_po(d, i) {
        return None;
    }
    let mut b = body?;
    if let Some(z) = zone {
        b.push(' ');
        b.push_str(z);
    }
    Some(b)
}

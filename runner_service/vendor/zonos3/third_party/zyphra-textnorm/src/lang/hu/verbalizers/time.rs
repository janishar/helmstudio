// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.

use super::{is_key, s};
use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut i = 0;
    let hour = s(d, i, "hours")?;
    if hour.is_empty() {
        return None;
    }
    i += 1;
    let minute = if is_key(d, i, "minutes") {
        let v = s(d, i, "minutes")?;
        i += 1;
        Some(v)
    } else {
        None
    };
    let second = if is_key(d, i, "seconds") {
        let v = s(d, i, "seconds")?;
        i += 1;
        Some(v)
    } else {
        None
    };
    let suffix = if is_key(d, i, "suffix") {
        let v = s(d, i, "suffix")?;
        i += 1;
        Some(v)
    } else {
        None
    };
    let zone = if is_key(d, i, "zone") {
        let v = s(d, i, "zone")?;
        i += 1;
        Some(v)
    } else {
        None
    };
    if [minute, second, suffix, zone].into_iter().flatten().any(str::is_empty) {
        return None;
    }
    let po = if i == d.len() {
        false
    } else if i + 1 == d.len() && d[i].0 == "preserve_order" && d[i].1 == Value::True {
        true
    } else {
        return None;
    };
    // These are precisely the field sequences in the source union.
    if po && zone.is_some() && suffix.is_some() {
        return None;
    }
    if po && suffix.is_some() && second.is_some() && zone.is_some() {
        return None;
    }
    if !po && minute.is_some() && second.is_none() && suffix.is_none() {
        return None;
    }
    if !po && minute.is_none() && second.is_some() {
        return None;
    }
    if !po && minute.is_some() && second.is_none() && suffix.is_some() && zone.is_none() {
        // hour_ora + minute + suffix + optional_zone also accepts this shape.
    }
    if !po && minute.is_some() && second.is_some() && suffix.is_some() && zone.is_none() {
        // The bare-hour HMS alternative accepts an optional suffix.
    }
    if po && minute.is_some() && second.is_some() && suffix.is_some() && zone.is_some() {
        return None;
    }
    let mut parts = Vec::new();
    parts.push(hour);
    match (minute, second, suffix, po) {
        (Some(m), Some(sec), Some(suf), true) => {
            parts.push("óra");
            if m != "nulla" {
                parts.push(m);
            }
            parts.push("perc");
            parts.push(sec);
            parts.push(suf);
        }
        (Some(m), Some(sec), None, true) => {
            parts.push("óra");
            if m != "nulla" {
                parts.push(m);
            }
            parts.push("perc");
            parts.push(sec);
            parts.push("masodperc");
        }
        (Some(m), Some(sec), suf, false) => {
            if m != "nulla" {
                parts.push(m);
            }
            parts.push(sec);
            if let Some(v) = suf {
                parts.push(v);
            }
        }
        (Some(m), None, Some(suf), _) => {
            parts.push("óra");
            if m != "nulla" {
                parts.push(m);
            }
            parts.push(suf);
        }
        (Some(m), None, None, true) => {
            parts.push("óra");
            if m != "nulla" {
                parts.push(m);
            }
            parts.push("perc");
        }
        (None, Some(sec), Some(suf), true) => {
            parts.push("óra");
            parts.push(sec);
            parts.push(suf);
        }
        (None, Some(sec), None, true) => {
            parts.push("óra");
            parts.push(sec);
            parts.push("masodperc");
        }
        (None, None, Some(suf), _) => parts.push(suf),
        (None, None, None, true) => parts.push("óra"),
        (None, None, None, false) => {}
        _ => return None,
    }
    if let Some(z) = zone {
        parts.push(z);
    }
    Some(parts.join(" "))
}

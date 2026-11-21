// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese time verbalizer: hours giờ [minutes phút] [seconds giây] [zone].

use crate::tag::Field;

crate::labels!(TIME_ZONES, "vi/time/time_zones.tsv");

pub fn verbalize(token: &[Field]) -> Option<String> {
    let fields = if token.last().is_some_and(|(k, v)| k == "preserve_order" && matches!(v, crate::tag::Value::True)) {
        &token[..token.len() - 1]
    } else {
        token
    };
    let keys = || fields.iter().map(|(k, _)| k.as_ref());
    let hours = keys().eq(["hours"])
        || keys().eq(["hours", "minutes"])
        || keys().eq(["hours", "seconds"])
        || keys().eq(["hours", "minutes", "seconds"])
        || keys().eq(["hours", "zone"])
        || keys().eq(["hours", "minutes", "zone"])
        || keys().eq(["hours", "seconds", "zone"])
        || keys().eq(["hours", "minutes", "seconds", "zone"]);
    if !hours
        && (fields.len() != token.len()
            || !(keys().eq(["minutes"]) || keys().eq(["minutes", "seconds"]) || keys().eq(["seconds"])))
    {
        return None;
    }
    if keys().eq(["hours", "seconds"]) || keys().eq(["hours", "seconds", "zone"]) {
        if !matches!(&fields[1].1, crate::tag::Value::Str(s) if s == "không") {
            return None;
        }
    }
    if !hours && matches!(&fields[0].1, crate::tag::Value::Str(s) if s == "không") {
        return None;
    }
    let mut hours = None;
    let mut minutes = None;
    let mut seconds = None;
    let mut zone = None;

    for (k, v) in token {
        match (k.as_ref(), v) {
            ("hours", crate::tag::Value::Str(s)) => hours = Some(s.as_ref()),
            ("minutes", crate::tag::Value::Str(s)) => minutes = Some(s.as_ref()),
            ("seconds", crate::tag::Value::Str(s)) => seconds = Some(s.as_ref()),
            ("zone", crate::tag::Value::Str(s))
                if TIME_ZONES.iter().any(|row| row.get(1).is_some_and(|v| v.as_str() == &**s)) =>
            {
                zone = Some(s.as_ref())
            }
            ("preserve_order", crate::tag::Value::True) => {}
            _ => return None,
        }
    }

    let mut out = String::new();

    if let Some(h) = hours {
        out.push_str(h);
        out.push_str(" giờ");
        if let Some(m) = minutes {
            if m != "không" {
                out.push(' ');
                out.push_str(m);
                out.push_str(" phút");
            }
        }
        if let Some(s) = seconds {
            if s != "không" {
                out.push(' ');
                out.push_str(s);
                out.push_str(" giây");
            }
        }
    } else if let Some(m) = minutes {
        out.push_str(m);
        out.push_str(" phút");
        if let Some(s) = seconds {
            if s != "không" {
                out.push(' ');
                out.push_str(s);
                out.push_str(" giây");
            }
        }
    } else if let Some(s) = seconds {
        out.push_str(s);
        out.push_str(" giây");
    } else {
        return None;
    }

    if let Some(z) = zone {
        out.push(' ');
        out.push_str(z);
    }

    Some(out)
}

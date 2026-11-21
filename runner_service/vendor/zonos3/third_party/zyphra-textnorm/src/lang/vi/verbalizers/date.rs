// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese date verbalizer: DMY, YMD, era phrases, decades.

use crate::tag::Field;

pub fn verbalize(token: &[Field]) -> Option<String> {
    let keys = || token.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["day", "month", "year"])
        && !keys().eq(["month", "year"])
        && !keys().eq(["day", "month"])
        && !keys().eq(["year"])
        && !keys().eq(["year", "era"])
        && !keys().eq(["ordinal", "era"])
        && !keys().eq(["text"])
    {
        return None;
    }
    let mut day = None;
    let mut month = None;
    let mut year = None;
    let mut era = None;
    let mut ordinal = None;
    let mut text = None;

    for (k, v) in token {
        match (k.as_ref(), v) {
            ("day", crate::tag::Value::Str(s)) => day = Some(s.as_ref()),
            ("month", crate::tag::Value::Str(s)) => month = Some(s.as_ref()),
            ("year", crate::tag::Value::Str(s)) => year = Some(s.as_ref()),
            ("era", crate::tag::Value::Str(s)) => era = Some(s.as_ref()),
            ("ordinal", crate::tag::Value::Str(s)) => ordinal = Some(s.as_ref()),
            ("text", crate::tag::Value::Str(s)) => text = Some(s.as_ref()),
            _ => return None,
        }
    }

    if let Some(t) = text {
        return Some(t.to_string());
    }

    if let (Some(ord), Some(e)) = (ordinal, era) {
        return Some(format!("năm thứ {} {}", ord, e));
    }

    match (day, month, year, era) {
        // day + month + year
        (Some(d), Some(m), Some(y), None) => Some(format!("ngày {} tháng {} năm {}", d, m, y)),
        // month + year
        (None, Some(m), Some(y), None) => Some(format!("tháng {} năm {}", m, y)),
        // day + month
        (Some(d), Some(m), None, None) => Some(format!("ngày {} tháng {}", d, m)),
        // year + era
        (None, None, Some(y), Some(e)) => Some(format!("năm {} {}", y, e)),
        // year only
        (None, None, Some(y), None) => Some(format!("năm {}", y)),
        _ => None,
    }
}

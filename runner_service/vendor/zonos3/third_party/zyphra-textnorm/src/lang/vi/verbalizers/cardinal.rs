// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese cardinal verbalizer: [âm ]integer.

use crate::tag::Field;

pub fn verbalize(token: &[Field]) -> Option<String> {
    let keys = || token.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["integer"]) && !keys().eq(["negative", "integer"]) {
        return None;
    }
    let mut negative = false;
    let mut integer = None;

    for (k, v) in token {
        match (k.as_ref(), v) {
            ("negative", crate::tag::Value::Str(s)) if s == "true" => negative = true,
            ("integer", crate::tag::Value::Str(s)) => integer = Some(s.as_ref()),
            _ => return None,
        }
    }

    let int_str = integer?;
    if negative { Some(format!("âm {}", int_str)) } else { Some(int_str.to_string()) }
}

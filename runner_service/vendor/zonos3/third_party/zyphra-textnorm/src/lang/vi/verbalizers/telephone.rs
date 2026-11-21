// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese telephone verbalizer.

use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    let keys = || d.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["number_part"]) && !keys().eq(["country_code", "number_part"]) {
        return None;
    }
    let mut country_code = None;
    let mut number_part = None;

    for (k, v) in d {
        match (k.as_ref(), v) {
            ("country_code", Value::Str(s)) => country_code = Some(s.as_ref()),
            ("number_part", Value::Str(s)) => number_part = Some(s.as_ref()),
            _ => return None,
        }
    }

    let num = number_part?;
    if let Some(cc) = country_code { Some(format!("{cc}, {num}")) } else { Some(num.to_string()) }
}

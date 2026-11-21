// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese roman verbalizer.

use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    let keys = || d.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["integer"]) && !keys().eq(["key_cardinal", "integer"]) {
        return None;
    }
    let mut key_cardinal = None;
    let mut integer = None;

    for (k, v) in d {
        match (k.as_ref(), v) {
            ("key_cardinal", Value::Str(s)) => key_cardinal = Some(s.as_ref()),
            ("integer", Value::Str(s)) => integer = Some(s.as_ref()),
            _ => return None,
        }
    }

    let int_str = integer?;
    if let Some(key) = key_cardinal { Some(format!("{key} {int_str}")) } else { Some(int_str.to_string()) }
}

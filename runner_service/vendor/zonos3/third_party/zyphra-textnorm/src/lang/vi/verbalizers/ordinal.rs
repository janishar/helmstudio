// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese ordinal verbalizer: thứ integer.

use crate::tag::Field;

pub fn verbalize(token: &[Field]) -> Option<String> {
    if token.len() != 1 {
        return None;
    }
    for (k, v) in token {
        if k == "integer" {
            if let crate::tag::Value::Str(s) = v {
                return Some(format!("thứ {}", s));
            }
        }
    }
    None
}

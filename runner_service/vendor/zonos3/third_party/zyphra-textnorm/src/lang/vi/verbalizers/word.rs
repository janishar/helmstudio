// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese word verbalizer: extracts `name` field, maps NBSP to space.

use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    for (k, v) in d {
        if *k == "name" {
            if let Value::Str(s) = v {
                return Some(s.replace('\u{a0}', " "));
            }
        }
    }
    None
}

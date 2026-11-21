// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese whitelist verbalizer: extracts `name` field, maps NBSP to space.

use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    let [(k, Value::Str(s))] = d else { return None };
    if k != "name" || s.is_empty() || s.contains('"') {
        return None;
    }
    Some(s.replace('\u{a0}', " "))
}

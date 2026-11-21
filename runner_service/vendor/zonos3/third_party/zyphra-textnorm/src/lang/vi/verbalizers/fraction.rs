// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese fraction verbalizer: [âm ][integer và ]num phần den.

use crate::tag::Field;

pub fn verbalize(token: &[Field]) -> Option<String> {
    let fields = if token.first().is_some_and(|(k, _)| k == "negative") { &token[1..] } else { token };
    let keys = || fields.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["numerator", "denominator"]) && !keys().eq(["integer_part", "numerator", "denominator"]) {
        return None;
    }
    let mut negative = false;
    let mut integer_part = None;
    let mut numerator = None;
    let mut denominator = None;

    for (k, v) in token {
        match (k.as_ref(), v) {
            ("negative", crate::tag::Value::Str(s)) if s == "true" => negative = true,
            ("integer_part", crate::tag::Value::Str(s)) => integer_part = Some(s.as_ref()),
            ("numerator", crate::tag::Value::Str(s)) => numerator = Some(s.as_ref()),
            ("denominator", crate::tag::Value::Str(s)) => denominator = Some(s.as_ref()),
            _ => return None,
        }
    }

    let num = numerator?;
    let den = denominator?;

    let mut out = String::new();
    if negative {
        out.push_str("âm ");
    }
    if let Some(int) = integer_part {
        out.push_str(int);
        out.push_str(" và ");
    }
    out.push_str(num);
    out.push_str(" phần ");
    out.push_str(den);
    Some(out)
}

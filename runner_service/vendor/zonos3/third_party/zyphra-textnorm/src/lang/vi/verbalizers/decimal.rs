// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese decimal verbalizer: [âm ]integer [phẩy fractional] [quantity].

use crate::tag::Field;

pub fn verbalize(token: &[Field]) -> Option<String> {
    let fields = if token.first().is_some_and(|(k, _)| k == "negative") { &token[1..] } else { token };
    let keys = || fields.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["integer_part"])
        && !keys().eq(["integer_part", "quantity"])
        && !keys().eq(["integer_part", "fractional_part"])
        && !keys().eq(["integer_part", "fractional_part", "quantity"])
        && !keys().eq(["fractional_part"])
    {
        return None;
    }
    let mut negative = false;
    let mut integer_part = None;
    let mut fractional_part = None;
    let mut quantity = None;

    for (k, v) in token {
        match (k.as_ref(), v) {
            ("negative", crate::tag::Value::Str(s)) if s == "true" => negative = true,
            ("integer_part", crate::tag::Value::Str(s)) => integer_part = Some(s.as_ref()),
            ("fractional_part", crate::tag::Value::Str(s)) => fractional_part = Some(s.as_ref()),
            ("quantity", crate::tag::Value::Str(s)) => quantity = Some(s.as_ref()),
            _ => return None,
        }
    }

    let mut out = String::new();
    if negative {
        out.push_str("âm ");
    }

    match (integer_part, fractional_part, quantity) {
        // 1. Full: integer + phẩy + fractional + quantity
        (Some(int), Some(frac), Some(qty)) => {
            out.push_str(int);
            out.push_str(" phẩy ");
            out.push_str(frac);
            out.push(' ');
            out.push_str(qty);
            Some(out)
        }
        // 2. Comma decimal: integer + phẩy + fractional
        (Some(int), Some(frac), None) => {
            out.push_str(int);
            out.push_str(" phẩy ");
            out.push_str(frac);
            Some(out)
        }
        // 3. Integer with quantity: integer + quantity
        (Some(int), None, Some(qty)) => {
            out.push_str(int);
            out.push(' ');
            out.push_str(qty);
            Some(out)
        }
        // 4. Fractional only: [phẩy ]fractional
        (None, Some(frac), None) => {
            out.push_str("phẩy ");
            out.push_str(frac);
            Some(out)
        }
        (None, Some(frac), Some(qty)) => {
            out.push_str("phẩy ");
            out.push_str(frac);
            out.push(' ');
            out.push_str(qty);
            Some(out)
        }
        // 5. Integer only
        (Some(int), None, None) => {
            out.push_str(int);
            Some(out)
        }
        _ => None,
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.

use super::find_str;
use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    for (i, (key, value)) in d.iter().enumerate() {
        if d[..i].iter().any(|(earlier, _)| earlier == key) {
            return None;
        }
        match (key.as_ref(), value) {
            ("preserve_order", Value::True) => {}
            ("negative", Value::Str(v)) if v == "true" => {}
            (
                "integer_part"
                | "fractional_part"
                | "quantity"
                | "currency_maj"
                | "currency_min"
                | "morphosyntactic_features",
                Value::Str(v),
            ) if !v.is_empty() => {}
            _ => return None,
        }
    }
    let negative = find_str(d, "negative").is_some();
    let integer = find_str(d, "integer_part");
    let fraction = find_str(d, "fractional_part");
    let quantity = find_str(d, "quantity");
    let major = find_str(d, "currency_maj");
    let minor = find_str(d, "currency_min");
    let morpho = find_str(d, "morphosyntactic_features");
    let preserve = d.iter().any(|(k, _)| k == "preserve_order");
    let position = |name: &str| d.iter().position(|(k, _)| k == name);

    // With preserve_order the source verbalizer consumes fields in their
    // original order. Without it, the normalizer can permute the fields.
    let minor_graph = major.is_some()
        && fraction.is_some()
        && quantity.is_none()
        && (minor.is_some() || (preserve && position("currency_maj") < position("fractional_part")));
    let mut expected = Vec::new();
    if negative {
        expected.push("negative");
    }
    if minor_graph {
        if integer.is_none() {
            return None;
        }
        expected.extend(["integer_part", "currency_maj", "fractional_part"]);
        if minor.is_some() {
            expected.push("currency_min");
        }
    } else if major.is_some() {
        if minor.is_some() || (integer.is_none() && fraction.is_none()) {
            return None;
        }
        if integer.is_some() {
            expected.push("integer_part");
        }
        if fraction.is_some() {
            expected.push("fractional_part");
        }
        if quantity.is_some() {
            expected.push("quantity");
        }
        expected.push("currency_maj");
    } else {
        if integer.is_some() || quantity.is_some() || fraction.is_none() || minor.is_none() {
            return None;
        }
        expected.extend(["fractional_part", "currency_min"]);
    }
    if preserve {
        expected.push("preserve_order");
    }
    if morpho.is_some() {
        expected.push("morphosyntactic_features");
    }
    if expected.len() != d.len() {
        return None;
    }
    if preserve && !d.iter().zip(&expected).all(|((key, _), expected)| key == *expected) {
        return None;
    }

    let mut out = String::new();
    if negative {
        out.push_str("mínusz ");
    }
    if minor_graph {
        out.push_str(integer?);
        out.push(' ');
        out.push_str(major?);
        out.push(' ');
        out.push_str(fraction?);
        if let Some(min) = minor {
            out.push(' ');
            out.push_str(min);
        }
    } else if let Some(maj) = major {
        if let Some(int) = integer {
            out.push_str(int);
        }
        if let Some(frac) = fraction {
            if integer.is_some() {
                out.push(' ');
            }
            out.push_str("egész ");
            out.push_str(frac);
        }
        if let Some(q) = quantity {
            out.push(' ');
            out.push_str(q);
        }
        out.push(' ');
        out.push_str(maj);
    } else {
        out.push_str(fraction?);
        out.push(' ');
        out.push_str(minor?);
    }
    if let Some(unit) = morpho {
        out.push(' ');
        out.push_str(unit);
    }
    Some(out)
}

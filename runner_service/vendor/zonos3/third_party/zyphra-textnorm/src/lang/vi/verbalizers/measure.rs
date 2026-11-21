// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese measure verbalizer.

use super::{cardinal, decimal, fraction};
use crate::tag::{Field, Value};

pub fn verbalize(d: &[Field]) -> Option<String> {
    let mut fields = d;
    if fields.first().is_some_and(|(k, _)| k == "negative") {
        fields = &fields[1..];
    }
    let unit_first = fields.first().is_some_and(|(k, _)| k == "units");
    if fields.last().is_some_and(|(k, v)| k == "preserve_order" && matches!(v, Value::True)) {
        if !unit_first {
            return None;
        }
        fields = &fields[..fields.len() - 1];
    }
    if fields.len() != 2
        || if unit_first {
            fields[1].0 != "cardinal" && fields[1].0 != "decimal" && fields[1].0 != "fraction"
        } else {
            fields[1].0 != "units"
                || (fields[0].0 != "cardinal" && fields[0].0 != "decimal" && fields[0].0 != "fraction")
        }
    {
        return None;
    }
    let mut negative = false;
    let preserve_order = unit_first;
    let mut units: Option<&str> = None;
    let mut number_str: Option<String> = None;

    for (k, v) in d {
        match (k.as_ref(), v) {
            ("negative", Value::Str(s)) if s == "true" => negative = true,
            ("preserve_order", Value::True) => {}
            ("units", Value::Str(s)) => units = Some(s.as_ref()),
            ("cardinal", Value::Dict(sub)) => {
                if number_str.is_none() {
                    number_str = cardinal::verbalize(sub);
                }
            }
            ("decimal", Value::Dict(sub)) => {
                if number_str.is_none() {
                    number_str = decimal::verbalize(sub);
                }
            }
            ("fraction", Value::Dict(sub)) => {
                if number_str.is_none() {
                    number_str = fraction::verbalize(sub);
                }
            }
            _ => return None,
        }
    }

    let u = units?;
    let num = number_str?;

    let mut out = String::new();
    if negative {
        out.push_str("âm ");
    }

    if preserve_order {
        out.push_str(u);
        out.push(' ');
        out.push_str(&num);
    } else {
        out.push_str(&num);
        out.push(' ');
        out.push_str(u);
    }

    Some(out)
}

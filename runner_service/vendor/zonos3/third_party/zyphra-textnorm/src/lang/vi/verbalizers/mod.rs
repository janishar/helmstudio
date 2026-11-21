// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese verbalizers module.

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod post_processing;
pub mod range;
pub mod roman;
pub mod telephone;
pub mod time;
pub mod whitelist;
pub mod word;

use crate::lang::common;
use crate::tag::{Dict, Value};

pub fn verbalize(token: &Dict) -> Option<String> {
    if let [(k, Value::Str(s))] = token.as_slice() {
        if k == "name" {
            // RangeFst also accepts non-whitespace names, including literal quotes.
            return (!s.is_empty() && (!s.contains('"') || !s.chars().any(crate::text::is_nemo_space)))
                .then(|| s.replace('\u{a0}', " "));
        }
    }

    let (class, val) = common::class(token)?;
    let fields = match val {
        Value::Dict(d) => d.as_slice(),
        _ => return None,
    };

    let res = match class {
        "cardinal" => cardinal::verbalize(fields),
        "ordinal" => ordinal::verbalize(fields),
        "decimal" => decimal::verbalize(fields),
        "fraction" => fraction::verbalize(fields),
        "date" => date::verbalize(fields),
        "time" => time::verbalize(fields),
        "money" => money::verbalize(fields),
        "measure" => measure::verbalize(fields),
        "telephone" => telephone::verbalize(fields),
        "electronic" => electronic::verbalize(fields),
        "roman" => roman::verbalize(fields),
        "whitelist" => whitelist::verbalize(fields),
        _ => None,
    };

    res.map(|s| if s.contains('\u{a0}') { s.replace('\u{a0}', " ") } else { s })
}

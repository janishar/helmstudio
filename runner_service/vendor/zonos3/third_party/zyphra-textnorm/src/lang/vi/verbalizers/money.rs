// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese money verbalizer: major/minor units, quantities, decimals, per-units.

use crate::tag::Field;

crate::labels!(PER_UNIT_NON_METRIC, "vi/money/per_unit_non_metric.tsv");
crate::labels!(PER_UNIT_PREFIXES, "vi/money/per_unit_prefixes.tsv");
crate::labels!(PER_UNIT_BASES, "vi/money/per_unit_bases.tsv");

fn valid_per_unit(value: &str) -> bool {
    if PER_UNIT_NON_METRIC.iter().any(|row| row.get(1).is_some_and(|s| s == value)) {
        return true;
    }
    let Some(rest) = value.strip_prefix("một ") else { return false };
    PER_UNIT_BASES.iter().any(|base| {
        let Some(base) = base.get(1) else { return false };
        rest == base
            || PER_UNIT_PREFIXES.iter().any(|prefix| {
                prefix.get(1).is_some_and(|prefix| {
                    rest.strip_prefix(prefix.as_str()).and_then(|s| s.strip_prefix(' ')) == Some(base.as_str())
                })
            })
    })
}

pub fn verbalize(token: &[Field]) -> Option<String> {
    let mut fields = token;
    if fields.first().is_some_and(|(k, _)| k == "negative") {
        fields = &fields[1..];
    }
    if fields.last().is_some_and(|(k, v)| k == "preserve_order" && matches!(v, crate::tag::Value::True)) {
        fields = &fields[..fields.len() - 1];
    }
    if fields.last().is_some_and(|(k, _)| k == "morphosyntactic_features") {
        fields = &fields[..fields.len() - 1];
    }
    if fields.last().is_some_and(|(k, v)| k == "preserve_order" && matches!(v, crate::tag::Value::True)) {
        if fields.len() < 2 || fields[fields.len() - 2].0 != "currency_min" {
            return None;
        }
        fields = &fields[..fields.len() - 1];
    }
    let keys = || fields.iter().map(|(k, _)| k.as_ref());
    if !keys().eq(["fractional_part", "currency_min"])
        && !keys().eq(["integer_part", "currency_maj", "fractional_part", "currency_min"])
        && !keys().eq(["integer_part", "currency_maj"])
        && !keys().eq(["integer_part", "quantity", "currency_maj"])
        && !keys().eq(["integer_part", "fractional_part", "currency_maj"])
        && !keys().eq(["integer_part", "fractional_part", "quantity", "currency_maj"])
    {
        return None;
    }
    let mut negative = false;
    let mut integer_part = None;
    let mut fractional_part = None;
    let mut quantity = None;
    let mut currency_maj = None;
    let mut currency_min = None;
    let mut morph_features = None;

    for (k, v) in token {
        match (k.as_ref(), v) {
            ("negative", crate::tag::Value::Str(s)) if s == "true" => negative = true,
            ("integer_part", crate::tag::Value::Str(s)) => integer_part = Some(s.as_ref()),
            ("fractional_part", crate::tag::Value::Str(s)) => fractional_part = Some(s.as_ref()),
            ("quantity", crate::tag::Value::Str(s)) => quantity = Some(s.as_ref()),
            ("currency_maj", crate::tag::Value::Str(s)) => currency_maj = Some(s.as_ref()),
            ("currency_min", crate::tag::Value::Str(s)) => currency_min = Some(s.as_ref()),
            ("morphosyntactic_features", crate::tag::Value::Str(s)) if valid_per_unit(s.as_ref()) => {
                morph_features = Some(s.as_ref())
            }
            ("preserve_order", crate::tag::Value::True) => {}
            _ => return None,
        }
    }

    let mut out = String::new();
    if negative {
        out.push_str("âm ");
    }

    // Pattern priority from source money verbalizer:
    if let (None, Some(frac), Some(c_min)) = (integer_part, fractional_part, currency_min) {
        // 1. Minor only: fractional + minor
        out.push_str(frac);
        out.push(' ');
        out.push_str(c_min);
    } else if let (Some(int), Some(c_maj), Some(frac), Some(c_min)) =
        (integer_part, currency_maj, fractional_part, currency_min)
    {
        // 2. Major + minor: integer + major + fractional + minor
        out.push_str(int);
        out.push(' ');
        out.push_str(c_maj);
        out.push(' ');
        out.push_str(frac);
        out.push(' ');
        out.push_str(c_min);
    } else if let (Some(int), Some(c_maj), Some(frac), Some(qty)) =
        (integer_part, currency_maj, fractional_part, quantity)
    {
        // 3. Decimal with quantity: integer + phẩy + fractional + quantity + currency
        out.push_str(int);
        out.push_str(" phẩy ");
        out.push_str(frac);
        out.push(' ');
        out.push_str(qty);
        out.push(' ');
        out.push_str(c_maj);
    } else if let (Some(int), Some(c_maj), None, Some(qty)) = (integer_part, currency_maj, fractional_part, quantity) {
        // 4. With quantity: integer + quantity + currency
        out.push_str(int);
        out.push(' ');
        out.push_str(qty);
        out.push(' ');
        out.push_str(c_maj);
    } else if let (Some(int), Some(c_maj), Some(frac), None) = (integer_part, currency_maj, fractional_part, quantity) {
        // 5. Decimal: integer + phẩy + fractional + currency
        out.push_str(int);
        out.push_str(" phẩy ");
        out.push_str(frac);
        out.push(' ');
        out.push_str(c_maj);
    } else if let (Some(int), Some(c_maj)) = (integer_part, currency_maj) {
        // 6. Simple: integer + currency
        out.push_str(int);
        out.push(' ');
        out.push_str(c_maj);
    } else {
        return None;
    }

    if let Some(mf) = morph_features {
        out.push(' ');
        out.push_str(mf);
    }

    Some(out)
}

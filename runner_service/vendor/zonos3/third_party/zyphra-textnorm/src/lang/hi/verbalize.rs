//! Verbalization of Hindi token fields.
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t'))
}
fn sign(f: &mut Fields<'_, '_>, extended: bool) -> Option<&'static str> {
    Some(match f.opt("negative")? {
        None => "",
        Some("true") => " ऋणात्मक ",
        Some("approx") => " लगभग ",
        Some("plus_minus") => " धन या ऋण ",
        Some("section") if extended => " धारा ",
        Some("copyright") if extended => " कॉपीराइट ",
        _ => return None,
    })
}
fn cardinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let prefix = sign(&mut f, true)?;
    let integer = f.take("integer")?;
    f.done().then(|| format!("{prefix}{integer}"))
}
fn decimal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let prefix = sign(&mut f, false)?;
    let integer = f.take("integer_part")?;
    let fraction = f.opt("fractional_part")?;
    let quantity = f.opt("quantity")?;
    if !f.done() || (fraction.is_none() && quantity.is_none()) {
        return None;
    }
    let mut out = format!("{prefix}{integer}");
    if let Some(frac) = fraction {
        out.push_str(" दशमलव ");
        out.push_str(frac);
    }
    if let Some(q) = quantity {
        let value =
            rows(include_str!("../../../data/hi/numbers/thousands.tsv")).find_map(|(k, v)| (k == q).then_some(v))?;
        out.push(' ');
        out.push_str(value);
    }
    Some(out)
}
fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let prefix = sign(&mut f, false)?;
    if let Some(q) = f.opt("morphosyntactic_features")? {
        return f.done().then(|| format!("{prefix}{q}"));
    }
    let integer = f.opt("integer_part")?;
    let num = f.take("numerator")?;
    let den = f.take("denominator")?;
    if !f.done() {
        return None;
    }
    Some(match integer {
        Some(i) => format!("{prefix}{i}  और {num} बटा {den}"),
        None => format!("{prefix}{num} बटा {den}"),
    })
}
fn date(d: &Dict) -> Option<String> {
    let mut fields = d.as_slice();
    while let Some((key, value)) = fields.last() {
        if (key == "preserve_order" && matches!(value, Value::True))
            || (key == "field_order" && common::str_value(value).is_some_and(|s| s.chars().count() == 1))
        {
            fields = &fields[..fields.len() - 1];
        } else {
            break;
        }
    }
    let keys: Vec<&str> = fields.iter().map(|(k, _)| k.as_ref()).collect();
    if !matches!(
        keys.as_slice(),
        ["day", "month"]
            | ["month", "day"]
            | ["day", "month", "year"]
            | ["month", "day", "year"]
            | ["year", "month", "day"]
            | ["month", "year"]
            | ["era"]
    ) {
        return None;
    }
    let mut out = String::new();
    for (_, v) in fields {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(common::str_value(v)?);
    }
    Some(out)
}
fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if let Some(q) = f.opt("morphosyntactic_features")? {
        return f.done().then(|| q.to_owned());
    }
    let hour = f.take("hours")?;
    let minute = f.opt("minutes")?;
    let second = f.opt("seconds")?;
    if !f.done() {
        return None;
    }
    match (minute, second) {
        (None, None) => Some(format!("{hour} बजे")),
        (Some(m), None) => Some(format!("{hour} बजकर {m} मिनट")),
        (Some(m), Some(s)) => Some(format!("{hour} बजकर {m} मिनट {s} सेकंड")),
        _ => None,
    }
}
fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if f.peek_key() == Some("units") {
        if f.take("units")? != "address" {
            return None;
        }
        let value = cardinal(f.dict("cardinal")?)?;
        while f.preserve_order() {}
        return f.done().then_some(value);
    }
    let number = match f.peek_key()? {
        "cardinal" => cardinal(f.dict("cardinal")?)?,
        "decimal" => decimal(f.dict("decimal")?)?,
        _ => return None,
    };
    let unit = f.take("units")?;
    if unit == "address" || !f.done() {
        return None;
    }
    Some(format!("{number} {unit}"))
}
fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let prefix = sign(&mut f, false)?;
    let major_first = f.peek_key() == Some("currency_maj");
    let (major, integer, fraction, minor) = if major_first {
        let major = f.take("currency_maj")?;
        let integer = f.take("integer_part")?;
        let fraction = f.opt("fractional_part")?;
        let minor = f.opt("currency_min")?;
        (major, integer, fraction, minor)
    } else {
        let integer = f.take("integer_part")?;
        let fraction = f.opt("fractional_part")?;
        let major = f.take("currency_maj")?;
        let minor = f.opt("currency_min")?;
        (major, integer, fraction, minor)
    };
    if !major_first {
        while f.preserve_order() {}
    }
    let per_unit = f.opt("per_unit")?;
    if !f.done() {
        return None;
    }
    let known = rows(include_str!("../../../data/hi/money/major_minor_currencies.tsv")).any(|(k, _)| k == major)
        || matches!(major, "पाकिस्तानी रुपए" | "पाकिस्तानी रुपया" | "रूसी रूबल");
    let mut out = match (fraction, minor) {
        (None, None) if !major_first => format!("{prefix}{integer} {major}"),
        (Some(frac), Some(min)) if known => {
            if integer == "शून्य" {
                format!("{prefix}{frac} {min}")
            } else {
                format!("{prefix}{integer} {major} {frac} {min}")
            }
        }
        (Some(frac), None) if major_first && known => format!("{prefix}{integer} दशमलव {frac} {major}"),
        _ => return None,
    };
    if let Some(unit) = per_unit {
        out.push(' ');
        out.push_str(unit);
    }
    Some(out)
}
fn telephone(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let cc = f.opt("country_code")?;
    let num = f.take("number_part")?;
    let ext = f.opt("extension")?;
    if !f.done() {
        return None;
    }
    let mut out = String::new();
    if let Some(cc) = cc {
        out.push_str(cc);
        out.push(' ');
    }
    out.push_str(num.strip_suffix(' ').unwrap_or(num));
    if let Some(ext) = ext {
        out.push(' ');
        out.push_str(ext);
    }
    Some(out)
}
fn electronic_content(s: &str, chemical: bool) -> Option<String> {
    let mut out = String::new();
    let mut chars = s.chars().peekable();
    while let Some(c) = chars.next() {
        if c.is_ascii_alphabetic() && !chemical {
            out.push(c);
            while chars.peek().is_some_and(char::is_ascii_alphabetic) {
                out.push(chars.next()?);
            }
            out.push(' ');
            continue;
        }
        let val = if c.is_ascii_alphabetic() && chemical {
            let lower = c.to_ascii_lowercase().to_string();
            rows(include_str!("../../../data/hi/address/letters.tsv")).find_map(|(k, v)| (k == lower).then_some(v))
        } else {
            super::numbers::digit(c)
                .or_else(|| {
                    let key = c.to_string();
                    rows(include_str!("../../../data/hi/electronic/subscript_digit.tsv"))
                        .find_map(|(k, v)| (k == key).then_some(v))
                })
                .or_else(|| {
                    let key = c.to_string();
                    rows(include_str!("../../../data/hi/electronic/symbols.tsv"))
                        .find_map(|(k, v)| (k.strip_prefix('\\').unwrap_or(k) == key).then_some(v))
                })
        }?;
        out.push_str(val);
        out.push(' ');
    }
    Some(out)
}
fn electronic(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out = String::new();
    if let Some(protocol) = f.opt("protocol")? {
        out.push_str(
            rows(include_str!("../../../data/hi/electronic/protocols.tsv"))
                .find_map(|(k, v)| (k == protocol).then_some(v))?,
        );
        out.push(' ');
        out.push_str(&electronic_content(f.take("domain")?, false)?);
    } else if let Some(username) = f.opt("username")? {
        out.push_str(&electronic_content(username, false)?);
        out.push_str("एट ");
        out.push_str(&electronic_content(f.take("domain")?, false)?);
    } else if let Some(domain) = f.opt("domain")? {
        out.push_str(&electronic_content(domain, false)?);
    } else if let Some(path) = f.opt("path")? {
        out.push_str(&electronic_content(path, false)?);
    } else {
        out.push_str(&electronic_content(f.take("fragment_id")?, true)?);
    }
    f.done().then_some(out)
}
fn roman(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let out = if f.peek_key() == Some("key_cardinal") {
        let key = f.take("key_cardinal")?;
        let numeral = f.take("integer")?;
        let spoken = rows(include_str!("../../../data/hi/roman/roman_to_spoken.tsv"))
            .find_map(|(k, v)| (k == numeral).then_some(v))?;
        format!("{key} {spoken}")
    } else {
        let numeral = f.take("integer")?;
        if let Some(ord) = f.opt("default_ordinal")? {
            match f.opt("key_cardinal")? {
                Some(key) => format!("{ord} {key}"),
                None => ord.to_owned(),
            }
        } else {
            let spoken = rows(include_str!("../../../data/hi/roman/roman_to_spoken.tsv"))
                .find_map(|(k, v)| (k == numeral).then_some(v))?;
            format!("{spoken} {}", f.take("key_cardinal")?)
        }
    };
    f.preserve_order();
    f.done().then_some(out)
}
pub fn verbalize(token: &Dict) -> Option<String> {
    if let [(key, Value::Str(value))] = token.as_slice() {
        if key == "name" && !value.is_empty() && !value.contains(' ') {
            return Some(value.to_string());
        }
    }
    let (class, Value::Dict(fields)) = common::class(token)? else { return None };
    let out = match class {
        "cardinal" => cardinal(fields),
        "decimal" => decimal(fields),
        "fraction" => fraction(fields),
        "date" => date(fields),
        "time" => time(fields),
        "measure" => measure(fields),
        "money" => money(fields),
        "telephone" => telephone(fields),
        "electronic" => electronic(fields),
        "roman" => roman(fields),
        "ordinal" => {
            let mut f = Fields::new(fields);
            let v = f.take("integer")?;
            f.done().then(|| v.to_owned())
        }
        _ => None,
    }?;
    Some(if out.contains('\u{a0}') { out.replace('\u{a0}', " ") } else { out })
}

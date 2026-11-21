//! Tamil token verbalizers.
use crate::lang::common::{Fields, class};
use crate::tag::{Dict, Value};
fn signed(f: &mut Fields<'_, '_>) -> Option<String> {
    match f.opt("negative")? {
        Some("true") => Some("மைனஸ் ".into()),
        Some(_) => None,
        None => Some(String::new()),
    }
}
fn cardinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut s = signed(&mut f)?;
    s.push_str(f.take("integer")?);
    f.done().then_some(s)
}
fn decimal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut s = signed(&mut f)?;
    s.push_str(f.take("integer_part")?);
    let fr = f.opt("fractional_part")?;
    if let Some(fr) = fr {
        s.push_str(" புள்ளி ");
        s.push_str(fr);
    }
    let quantity = f.opt("quantity")?;
    if let Some(q) = quantity {
        if !include_str!("../../../data/ta/numbers/quantities.tsv")
            .lines()
            .filter_map(|l| l.split_once('\t'))
            .any(|(k, _)| k == q)
        {
            return None;
        }
        s.push(' ');
        s.push_str(q);
    }
    if fr.is_none() && quantity.is_none() {
        return None;
    }
    f.done().then_some(s)
}
fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut s = signed(&mut f)?;
    if let Some(v) = f.opt("morphosyntactic_features")? {
        s.push_str(v);
        return f.done().then_some(s);
    }
    if let Some(i) = f.opt("integer_part")? {
        s.push_str(i);
        s.push(' ');
    }
    let den = f.take("denominator")?;
    if let Some(v) = den.strip_suffix('ு') {
        s.push_str(v);
        s.push_str("ில்")
    } else if let Some(v) = den.strip_suffix("ம்") {
        s.push_str(v);
        s.push_str("த்தில்")
    } else {
        s.push_str(den);
        s.push_str("இல்")
    }
    s.push(' ');
    s.push_str(f.take("numerator")?);
    f.done().then_some(s)
}
fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut pieces = Vec::new();
    let mut keys = Vec::new();
    while let Some(k) = f.peek_key() {
        if k == "preserve_order" {
            f.preserve_order();
            break;
        }
        if k == "field_order" {
            if f.take("field_order")?.chars().count() != 1 {
                return None;
            }
            break;
        }
        if !["day", "month", "year", "era"].contains(&k) {
            return None;
        }
        keys.push(k);
        pieces.push(f.take(k)?.to_owned());
    }
    if !f.done() {
        return None;
    }
    if !matches!(
        keys.as_slice(),
        ["day", "month", "year"]
            | ["day", "month", "year", "era"]
            | ["year", "month", "day"]
            | ["day", "month"]
            | ["month", "year"]
            | ["month", "year", "era"]
            | ["year", "era"]
            | ["year"]
    ) {
        return None;
    }
    Some(pieces.join(" "))
}
fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut v = Vec::new();
    if let Some(p) = f.opt("day_period")? {
        v.push(p.to_owned())
    }
    let h = f.take("hours")?;
    v.push(if h == "ஒன்று" { "ஒரு மணி".into() } else { format!("{h} மணி") });
    let minute = f.opt("minutes")?;
    if let Some(m) = minute {
        v.push(if m == "ஒன்று" {
            "ஒரு நிமிடம்".into()
        } else {
            format!("{m} நிமிடங்கள்")
        });
    }
    if let Some(s) = f.opt("seconds")? {
        if minute.is_none() {
            return None;
        }
        v.push(if s == "ஒன்று" {
            "ஒரு விநாடி".into()
        } else {
            format!("{s} விநாடிகள்")
        });
    }
    if let Some(z) = f.opt("zone")? {
        v.push(z.into())
    }
    f.done().then(|| v.join(" "))
}
fn table<'a>(t: &'a str, k: &str) -> Option<&'a str> {
    t.lines().filter_map(|l| l.split_once('\t')).find_map(|(a, b)| (a == k).then_some(b))
}
fn electronic_content(s: &str, chemical: bool) -> Option<String> {
    let mut parts = Vec::new();
    let mut run = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() && !chemical {
            run.push(c);
            continue;
        }
        if !run.is_empty() {
            parts.push(std::mem::take(&mut run))
        }
        let key = c.to_string();
        let value = if chemical && c.is_ascii_alphabetic() {
            table(include_str!("../../../data/ta/address/letters.tsv"), &c.to_ascii_lowercase().to_string())
        } else {
            table(include_str!("../../../data/ta/electronic/symbols.tsv"), &key)
                .or_else(|| table(include_str!("../../../data/ta/electronic/subscript_digit.tsv"), &key))
                .or_else(|| {
                    super::number::digit(c)
                        .and_then(|d| table(include_str!("../../../data/ta/telephone/number.tsv"), &d.to_string()))
                })
        }?;
        parts.push(value.to_owned());
    }
    if !run.is_empty() {
        parts.push(run)
    }
    Some(parts.join(" "))
}
fn electronic(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut out = Vec::new();
    if let Some(p) = f.opt("protocol")? {
        out.push(table(include_str!("../../../data/ta/electronic/protocols.tsv"), p)?.to_owned());
    }
    if let Some(u) = f.opt("username")? {
        out.push(electronic_content(u, false)?);
        out.push("அட்".into())
    }
    if let Some(v) = f.opt("domain")? {
        out.push(electronic_content(v, false)?)
    } else if let Some(v) = f.opt("path")? {
        out.push(electronic_content(v, false)?)
    } else if let Some(v) = f.opt("fragment_id")? {
        out.push(electronic_content(v, true)?)
    } else {
        return None;
    }
    f.done().then(|| out.join(" "))
}
fn roman(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let mut words = Vec::new();
    if let Some(k) = f.opt("key_cardinal")? {
        words.push(k.replace('\u{a0}', " "))
    }
    let n = f.take("integer")?;
    if let Some(v) = f.opt("default_ordinal")? {
        words.push(v.to_owned())
    } else {
        words.push(table(include_str!("../../../data/ta/roman/roman_to_spoken.tsv"), n)?.to_owned())
    }
    if let Some(k) = f.opt("key_cardinal")? {
        words.push(k.replace('\u{a0}', " "))
    }
    f.preserve_order();
    f.done().then(|| words.join(" "))
}
fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut parts = Vec::new();
    if let Some(p) = f.opt("morphosyntactic_features")? {
        parts.push(p.to_owned())
    }
    let n =
        if f.peek_key() == Some("cardinal") { cardinal(f.dict("cardinal")?)? } else { decimal(f.dict("decimal")?)? };
    parts.push(n);
    parts.push(f.take("units")?.to_owned());
    f.preserve_order();
    f.done().then(|| parts.join(" "))
}
fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut prefix = signed(&mut f)?;
    let pre = f.opt("morphosyntactic_features")?;
    if let Some(v) = pre {
        prefix.push_str(v);
        prefix.push(' ')
    }
    let first = f.peek_key()?;
    let (major, int, frac, minor) = if first == "currency_maj" {
        let m = f.take("currency_maj")?;
        let i = f.take("integer_part")?;
        let a = f.opt("fractional_part")?;
        let b = f.opt("currency_min")?;
        (m, i, a, b)
    } else {
        let i = f.take("integer_part")?;
        let a = f.opt("fractional_part")?;
        let m = f.take("currency_maj")?;
        let b = f.opt("currency_min")?;
        (m, i, a, b)
    };
    if (first == "currency_maj") != frac.is_some() || (frac.is_none() && minor.is_some()) {
        return None;
    }
    let post = f.opt("morphosyntactic_features")?;
    if !f.done() {
        return None;
    }
    if let Some(v) = post {
        prefix.push_str(v);
        prefix.push(' ')
    }
    if let Some(fr) = frac {
        if let Some(mi) = minor {
            if int == "பூஜ்யம்" {
                prefix.push_str(&format!("{fr} {mi}"))
            } else {
                prefix.push_str(&format!("{int} {major} {fr} {mi}"))
            }
        } else {
            prefix.push_str(&format!("{int}  புள்ளி {fr} {major}"))
        }
    } else {
        prefix.push_str(&format!("{int} {major}"))
    }
    Some(prefix)
}
pub fn verbalize(token: &Dict) -> Option<String> {
    // Tamil's word/whitelist verbalizer copies NBSP verbatim. The wrapper,
    // not the grammar, subsequently normalizes it for end-to-end output.
    if let [(key, Value::Str(value))] = token.as_slice() {
        if key == "name" && !value.is_empty() && !value.contains(' ') {
            return Some(value.to_string());
        }
    }
    let (name, value) = class(token)?;
    let Value::Dict(d) = value else { return None };
    match name {
        "cardinal" => cardinal(d),
        "ordinal" => {
            let mut f = Fields::new(d);
            let v = f.take("integer")?.to_owned();
            f.done().then_some(v)
        }
        "decimal" => decimal(d),
        "fraction" => fraction(d),
        "date" => date(d),
        "time" => time(d),
        "measure" => measure(d),
        "money" => money(d),
        "electronic" => electronic(d),
        "roman" => roman(d),
        "telephone" => {
            let mut f = Fields::new(d);
            let mut v = Vec::new();
            if let Some(c) = f.opt("country_code")? {
                v.push(c.to_owned())
            }
            let n = f.take("number_part")?;
            v.push(n.strip_suffix(' ').unwrap_or(n).to_owned());
            if let Some(e) = f.opt("extension")? {
                v.push(e.to_owned())
            }
            f.done().then(|| v.join(" "))
        }
        _ => None,
    }
}

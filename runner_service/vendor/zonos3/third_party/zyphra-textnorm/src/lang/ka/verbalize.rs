use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn signed(f: &mut Fields<'_, '_>) -> Option<&'static str> {
    match f.opt("negative")? {
        Some("true") => Some("მინუს "),
        None => Some(""),
        _ => None,
    }
}
fn simple(kind: &str, d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = if kind == "ordinal" { "" } else { signed(&mut f)? };
    let integer = f.take("integer")?;
    if integer.is_empty() || !f.done() {
        return None;
    }
    Some(format!("{sign}{integer}"))
}
fn decimal(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = signed(&mut f)?;
    let i = f.take("integer_part")?;
    let frac = f.opt("fractional_part")?;
    let q = f.opt("quantity")?;
    if frac.is_none() && q.is_none() {
        return None;
    }
    f.preserve_order();
    if !f.done() {
        return None;
    }
    let mut out = format!("{sign}{i}");
    if let Some(v) = frac {
        out.push_str(" მთელი ");
        out.push_str(v);
    }
    if let Some(v) = q {
        out.push(' ');
        out.push_str(v);
    }
    Some(out)
}
fn fraction(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = signed(&mut f)?;
    let whole = f.opt("integer_part")?;
    let n = f.take("numerator")?;
    let den = f.take("denominator")?;
    if !f.done() {
        return None;
    }
    Some(format!("{sign}{}{n} {den}", whole.map(|x| format!("{x} მთელი ")).unwrap_or_default()))
}
fn time(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let period = f.opt("period")?;
    let hour = f.take("hours")?;
    let min = f.opt("minutes")?;
    let sec = f.opt("seconds")?;
    let zone = f.opt("zone")?;
    f.preserve_order();
    if !f.done() {
        return None;
    }
    let capacity = hour.len()
        + period.map_or(0, |v| v.len() + 1)
        + min.map_or(0, |v| v.len() + " და ".len())
        + sec.map_or(0, |v| v.len() + " და ".len() + " წამი".len())
        + zone.map_or(0, |v| v.len() + 1);
    let mut out = String::with_capacity(capacity);
    if let Some(p) = period {
        out.push_str(p);
        out.push(' ');
    }
    out.push_str(hour);
    if let Some(v) = min {
        out.push_str(" და ");
        out.push_str(v);
    }
    if let Some(v) = sec {
        out.push_str(" და ");
        out.push_str(v);
        out.push_str(" წამი");
    }
    if let Some(v) = zone {
        out.push(' ');
        out.push_str(v);
    }
    Some(out)
}
fn date(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let year = f.opt("year")?;
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let era = f.opt("era")?;
    f.preserve_order();
    if !f.done() {
        return None;
    }
    match (year, day, month, era) {
        (Some(y), Some(d), Some(m), None) => Some(format!("{y} წლის {d} {m}")),
        (None, Some(d), Some(m), None) => Some(format!("{d} {m}")),
        (Some(y), None, None, Some(e)) => Some(format!("{y} {e}")),
        (Some(y), None, None, None) => Some(y.to_owned()),
        _ => None,
    }
}
fn measure(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let key = f.peek_key()?;
    if key != "cardinal" && key != "decimal" {
        return None;
    }
    let nested = f.dict(key)?;
    let u = f.take("units")?;
    if !f.done() {
        return None;
    }
    if key == "cardinal" {
        let mut number = Fields::new(nested);
        let sign = signed(&mut number)?;
        let integer = number.take("integer")?;
        if integer.is_empty() || !number.done() {
            return None;
        }
        let mut out = String::with_capacity(sign.len() + integer.len() + 1 + u.len());
        out.push_str(sign);
        out.push_str(integer);
        out.push(' ');
        out.push_str(u);
        Some(out)
    } else {
        let mut out = decimal(nested)?;
        out.reserve(1 + u.len());
        out.push(' ');
        out.push_str(u);
        Some(out)
    }
}
fn money(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = signed(&mut f)?;
    let i = f.opt("integer_part")?;
    let mut out = String::from(sign);
    if let Some(i) = i {
        out.push_str(i);
        if f.peek_key() == Some("currency") || f.peek_key() == Some("currency_maj") {
            let key = f.peek_key()?;
            let currency = f.take(key)?;
            out.push(' ');
            out.push_str(currency);
            if let Some(min) = f.opt("fractional_part")? {
                let unit = f.take("currency_min")?;
                out.push_str(&format!(" და {min} {unit}"));
            }
        } else {
            if let Some(v) = f.opt("fractional_part")? {
                out.push_str(" მთელი ");
                out.push_str(v);
            }
            if let Some(v) = f.opt("quantity")? {
                out.push(' ');
                out.push_str(v);
            }
            let key = f.peek_key()?;
            if key != "currency" && key != "currency_maj" {
                return None;
            }
            let c = f.take(key)?;
            out.push(' ');
            out.push_str(c);
        }
    } else {
        let v = f.take("fractional_part")?;
        let u = f.take("currency_min")?;
        out.push_str(&format!("{v} {u}"));
    }
    if let Some(v) = f.opt("morphosyntactic_features")? {
        out.push(' ');
        out.push_str(v);
    }
    f.preserve_order();
    if !f.done() {
        return None;
    }
    Some(out)
}
fn telephone(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let c = f.opt("country_code")?;
    let n = f.take("number_part")?;
    f.preserve_order();
    if !f.done() {
        return None;
    }
    Some(match c {
        Some(c) => format!("{c}, {n}"),
        None => n.to_owned(),
    })
}
fn spell(s: &str) -> String {
    let tables =
        [include_str!("../../../data/ka/electronic/words.tsv"), include_str!("../../../data/ka/electronic/domain.tsv")];
    let symbols = include_str!("../../../data/ka/electronic/symbols.tsv");
    let digits = include_str!("../../../data/ka/numbers/digit.tsv");
    // Each lexical chunk and each single character has unit weight.  Dynamic
    // programming preserves the globally shortest path when words overlap.
    let mut best: Vec<Option<(usize, String)>> = vec![None; s.len() + 1];
    best[s.len()] = Some((0, String::new()));
    for (pos, c) in s.char_indices().rev() {
        let next = pos + c.len_utf8();
        let key = c.to_string();
        let v = super::lookup(symbols, &key).or_else(|| super::lookup(digits, &key)).unwrap_or(&key);
        let (count, rest) = best[next].as_ref().unwrap();
        let mut choice = (*count + 1, if rest.is_empty() { v.to_owned() } else { format!("{v} {rest}") });
        for table in tables {
            for line in table.lines() {
                if let Some((k, v)) = line.split_once('\t') {
                    if !k.is_empty() && s[pos..].starts_with(k) {
                        if let Some(Some((n, rest))) = best.get(pos + k.len()) {
                            if n + 1 < choice.0 {
                                choice = (n + 1, if rest.is_empty() { v.to_owned() } else { format!("{v} {rest}") });
                            }
                        }
                    }
                }
            }
        }
        best[pos] = Some(choice);
    }
    best[0].take().map(|v| v.1).unwrap_or_default()
}
fn electronic(d: &Dict<'_>) -> Option<String> {
    let mut f = Fields::new(d);
    let user = f.opt("username")?;
    let protocol = f.opt("protocol")?;
    if user.is_some() && protocol.is_some() {
        return None;
    }
    let domain = f.take("domain")?;
    f.preserve_order();
    if !f.done() {
        return None;
    }
    let mut out = String::new();
    if let Some(u) = user {
        out.push_str(&spell(u));
        out.push_str(" ეთ ");
    }
    if let Some(p) = protocol {
        out.push_str(&spell(p));
        out.push(' ');
    }
    out.push_str(&spell(domain));
    Some(out)
}

pub fn verbalize(token: &Dict<'_>) -> Option<String> {
    if let Some(s) = common::verbalize_name(token) {
        return Some(s);
    }
    let (kind, value) = common::class(token)?;
    let Value::Dict(d) = value else {
        return None;
    };
    match kind {
        "cardinal" | "ordinal" => simple(kind, d),
        "decimal" => decimal(d),
        "fraction" => fraction(d),
        "measure" => measure(d),
        "money" => money(d),
        "time" => time(d),
        "date" => date(d),
        "telephone" => telephone(d),
        "electronic" => electronic(d),
        _ => None,
    }
}

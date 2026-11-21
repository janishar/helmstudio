//! Gujarati verbalizer union.
use super::{electronic, roman};
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};
fn sign(f: &mut Fields<'_, '_>) -> Option<String> {
    Some(match f.opt("negative")? {
        Some("true") => "નકારાત્મક ".to_owned(),
        Some(x) if !x.is_empty() => format!("{x} "),
        Some(_) => return None,
        None => String::new(),
    })
}
fn cardinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = sign(&mut f)?;
    let n = f.take("integer")?;
    if !f.done() {
        return None;
    }
    Some(format!("{s}{n}"))
}
fn decimal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = sign(&mut f)?;
    let n = f.take("integer_part")?;
    if n.is_empty() {
        return None;
    }
    let frac = f.opt("fractional_part")?;
    let q = f.opt("quantity")?;
    if !f.done() || frac.is_none() && q.is_none() {
        return None;
    }
    let mut o = format!("{s}{n}");
    if let Some(v) = frac {
        if v.is_empty() {
            return None;
        }
        o.push_str(" દશાંશ ");
        o.push_str(v);
    }
    if let Some(v) = q {
        o.push(' ');
        o.push_str(v);
    }
    Some(o)
}
fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut o = sign(&mut f)?;
    if let Some(x) = f.opt("morphosyntactic_features")? {
        if x.is_empty() || !f.done() {
            return None;
        }
        o.push_str(x);
        return Some(o);
    }
    if let Some(x) = f.opt("integer_part")? {
        o.push_str(x);
        o.push_str(" પૂર્ણાંક ");
    }
    let n = f.take("numerator")?;
    let den = f.take("denominator")?;
    if !f.done() {
        return None;
    }
    o.push_str(n);
    o.push_str(" ના છેદમાં ");
    o.push_str(den);
    Some(o)
}
fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if f.peek_key() == Some("era") {
        let e = f.take("era")?;
        let y = f.take("year")?;
        if !f.preserve_order() || !f.done() {
            return None;
        }
        return Some(format!("{e} {y}"));
    }
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    if !f.done()
        || !(day.is_some() && month.is_some() || day.is_none() && year.is_some())
        || era.is_some() && year.is_none()
    {
        return None;
    }
    let v: Vec<_> = [day, month, year, era].into_iter().flatten().collect();
    if v.iter().any(|x| x.is_empty()) {
        return None;
    }
    Some(v.join(" "))
}
fn clock(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if f.peek_key() == Some("start") {
        let a = f.dict("start")?;
        let b = f.dict("end")?;
        if !f.preserve_order() || !f.done() {
            return None;
        }
        return Some(format!("{} થી {}", clock(a)?, clock(b)?));
    }
    let period = f.opt("period")?;
    let h = f.take("hours")?;
    let m = f.opt("minutes")?;
    let sec = f.opt("seconds")?;
    let z = f.opt("zone")?;
    if !f.done() || sec.is_some() && m.is_none() {
        return None;
    }
    let mut o = String::new();
    if let Some(v) = period {
        o.push_str(v);
        o.push(' ');
    }
    o.push_str(h);
    if let Some(v) = m {
        o.push_str(" વાગીને ");
        o.push_str(v);
        o.push_str(" મિનિટ");
    } else {
        o.push_str(" વાગ્યે");
    }
    if let Some(v) = sec {
        o.push(' ');
        o.push_str(v);
        o.push_str(" સેકન્ડ");
    }
    if let Some(v) = z {
        o.push(' ');
        o.push_str(v);
    }
    Some(o)
}
fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if f.peek_key() == Some("units") {
        if f.take("units")? != "address" {
            return None;
        }
        let n = cardinal(f.dict("cardinal")?)?;
        f.preserve_order();
        return f.done().then_some(n);
    }
    let value =
        if f.peek_key() == Some("cardinal") { cardinal(f.dict("cardinal")?)? } else { decimal(f.dict("decimal")?)? };
    let unit = f.take("units")?;
    if unit == "address" || !f.done() {
        return None;
    }
    Some(format!("{value} {unit}"))
}
fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = sign(&mut f)?;
    if f.peek_key() == Some("integer_part") {
        let n = f.take("integer_part")?;
        let currency = f.take("currency_maj")?;
        let per = f.opt("morphosyntactic_features")?;
        if !f.done() {
            return None;
        }
        return Some(format!("{s}{n} {currency}{}", per.map(|p| format!(" {p}")).unwrap_or_default()));
    }
    let major = f.take("currency_maj")?;
    let n = f.take("integer_part")?;
    let frac = f.opt("fractional_part")?;
    let minor = f.opt("currency_min")?;
    let per = f.opt("morphosyntactic_features")?;
    if !f.done() {
        return None;
    }
    let core = match (frac, minor) {
        (None, None) => format!("{n} {major}"),
        (Some(x), Some(m)) if n == "શૂન્ય" => format!("{x} {m}"),
        (Some(x), Some(m)) => format!("{n} {major} {x} {m}"),
        (Some(x), None) => format!("{n} દશાંશ {x} {major}"),
        _ => return None,
    };
    Some(format!("{s}{core}{}", per.map(|p| format!(" {p}")).unwrap_or_default()))
}
fn telephone(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let country = f.opt("country_code")?;
    let n = f.take("number_part")?.trim_end_matches(' ');
    let ext = f.opt("extension")?;
    if !f.done() {
        return None;
    }
    Some(format!(
        "{}{}{}",
        country.map(|x| format!("{x} ")).unwrap_or_default(),
        n,
        ext.map(|x| format!(" {x}")).unwrap_or_default()
    ))
}
fn roman_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let mut out = String::new();
    if let Some(k) = f.opt("key_cardinal")? {
        out.push_str(k);
        out.push(' ');
        out.push_str(roman::spoken(f.take("integer")?)?);
    } else {
        let n = f.take("integer")?;
        if let Some(o) = f.opt("default_ordinal")? {
            out.push_str(o);
        } else {
            out.push_str(roman::spoken(n)?);
        }
        if let Some(k) = f.opt("key_cardinal")? {
            out.push(' ');
            out.push_str(k);
        }
    }
    f.preserve_order();
    f.done().then_some(out)
}
pub fn verbalize(token: &Dict) -> Option<String> {
    let (class, value) = common::class(token)?;
    if class == "name" {
        let Value::Str(name) = value else { return None };
        return (!name.is_empty() && !name.contains(' ')).then(|| name.to_string());
    }
    let Value::Dict(d) = value else { return None };
    let result = match class {
        "cardinal" => cardinal(d),
        "decimal" => decimal(d),
        "fraction" => fraction(d),
        "date" => date(d),
        "time" => clock(d),
        "measure" => measure(d),
        "money" => money(d),
        "telephone" => telephone(d),
        "roman" => roman_v(d),
        "electronic" => electronic::verbalize(d),
        "ordinal" => {
            let mut f = Fields::new(d);
            let n = f.take("integer")?;
            if f.done() && !n.is_empty() { Some(n.to_owned()) } else { None }
        }
        _ => None,
    }?;
    Some(result)
}

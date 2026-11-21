use super::graph_utils::{digit, digit_name, table};
use crate::lang::common::{Fields, class, verbalize_name};
use crate::tag::{Dict, Value};
fn signed(f: &mut Fields<'_, '_>) -> Option<String> {
    match f.opt("negative")? {
        Some("true") => Some("ಋಣ ".to_owned()),
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
    if let Some(v) = f.opt("fractional_part")? {
        s.push_str(" ದಶಮಾಂಶ ");
        s.push_str(v);
    } else if f.peek_key() != Some("quantity") {
        return None;
    }
    if let Some(v) = f.opt("quantity")? {
        s.push(' ');
        s.push_str(table(include_str!("../../../data/kn/numbers/quantities.tsv"), v)?);
    }
    f.done().then_some(s)
}
fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    if !f.done()
        || (day.is_some() && month.is_none())
        || (day.is_none() && month.is_some() && year.is_none())
        || (day.is_none() && month.is_none() && year.is_none())
        || (era.is_some() && year.is_none())
    {
        return None;
    }
    Some([day, month, year, era].into_iter().flatten().collect::<Vec<_>>().join(" "))
}
fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let period = f.opt("day_period")?;
    let h = f.take("hours")?;
    let m = f.opt("minutes")?;
    let sec = f.opt("seconds")?;
    let zone = f.opt("zone")?;
    if !f.done() || (sec.is_some() && m.is_none()) {
        return None;
    }
    let mut s = String::new();
    if let Some(v) = period {
        s.push_str(v);
        s.push(' ');
    }
    s.push_str(h);
    s.push_str(" ಗಂಟೆ");
    if let Some(v) = m {
        s.push(' ');
        s.push_str(v);
        s.push_str(" ನಿಮಿಷ");
    }
    if let Some(v) = sec {
        s.push(' ');
        s.push_str(v);
        s.push_str(" ಸೆಕೆಂಡ್");
    }
    if let Some(v) = zone {
        s.push(' ');
        s.push_str(v);
    }
    Some(s)
}
fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut s = signed(&mut f)?;
    if let Some(v) = f.opt("morphosyntactic_features")? {
        s.push_str(v);
        return f.done().then_some(s);
    }
    if let Some(v) = f.opt("integer_part")? {
        s.push_str(v);
        s.push_str(" ಮತ್ತು ");
    }
    let den = f.take("denominator")?;
    let num = f.take("numerator")?;
    if !f.done() {
        return None;
    }
    if den == "ಸಾವಿರ" {
        s.push_str("ಸಾವಿರದಲ್ಲಿ");
    } else if let Some(v) = den.strip_suffix('ು') {
        s.push_str(v);
        s.push_str("ರಲ್ಲಿ");
    } else {
        s.push_str(den);
        s.push_str("ರಲ್ಲಿ");
    }
    s.push(' ');
    s.push_str(num);
    Some(s)
}
fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let prefix = f.opt("morphosyntactic_features")?;
    let val = match f.peek_key()? {
        "cardinal" => cardinal(f.dict("cardinal")?)?,
        "decimal" => decimal(f.dict("decimal")?)?,
        _ => return None,
    };
    let unit = f.take("units")?;
    f.preserve_order();
    if !f.done() {
        return None;
    }
    Some(format!("{}{val} {unit}", prefix.map(|p| format!("{p} ")).unwrap_or_default()))
}
fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = signed(&mut f)?;
    let prefix = f.opt("morphosyntactic_features")?;
    let body = if f.peek_key() == Some("integer_part") {
        let int = f.take("integer_part")?;
        let maj = f.take("currency_maj")?;
        format!("{int} {maj}")
    } else {
        let maj = f.take("currency_maj")?;
        let int = f.take("integer_part")?;
        let frac = f.take("fractional_part")?;
        let minor = f.opt("currency_min")?;
        let known = maj == "ಯುಎಸ್ ಡಾಲರ್"
            || [
                include_str!("../../../data/kn/money/currency.tsv"),
                include_str!("../../../data/kn/money/currency_singular.tsv"),
            ]
            .iter()
            .any(|t| t.lines().filter_map(|l| l.split_once('\t')).any(|(_, v)| v == maj))
            || table(include_str!("../../../data/kn/money/major_minor_currencies.tsv"), maj).is_some();
        if !known {
            return None;
        }
        match minor {
            Some(m) if int == "ಸೊನ್ನೆ" => format!("{frac} {m}"),
            Some(m) => format!("{int} {maj} {frac} {m}"),
            None => format!("{int}  ದಶಮಾಂಶ {frac} {maj}"),
        }
    };
    let suffix = f.opt("morphosyntactic_features")?;
    if !f.done() || prefix.is_some() && suffix.is_some() {
        return None;
    }
    if let Some(p) = suffix {
        if !["ಗಂಟೆಗೆ", "ಸೆಕೆಂಡಿಗೆ", "ದಿನಕ್ಕೆ", "ವಾರಕ್ಕೆ", "ತಿಂಗಳಿಗೆ", "ವರ್ಷಕ್ಕೆ", "ಮೀಟರ್‌ಗೆ", "ಕಿಲೋಮೀಟರ್‌ಗೆ", "ಕಿಲೋಗ್ರಾಂಗೆ", "ಗ್ರಾಂಗೆ", "ಲೀಟರ್‌ಗೆ"]
            .contains(&p)
        {
            return None;
        }
    }
    Some(format!("{sign}{}{body}", prefix.or(suffix).map(|p| format!("{p} ")).unwrap_or_default()))
}
fn electronic_content(s: &str) -> Option<String> {
    let symbols = include_str!("../../../data/kn/electronic/symbols.tsv");
    let subs = include_str!("../../../data/kn/electronic/subscript_digit.tsv");
    let mut out = Vec::new();
    let mut run = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            run.push(c);
            continue;
        }
        if !run.is_empty() {
            out.push(std::mem::take(&mut run));
        }
        let word = if let Some(d) = digit(c) {
            digit_name(d).to_owned()
        } else {
            let k = c.to_string();
            table(symbols, &k).or_else(|| table(subs, &k))?.to_owned()
        };
        out.push(word);
    }
    if !run.is_empty() {
        out.push(run);
    }
    Some(out.join(" "))
}
fn electronic(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let a = match f.peek_key()? {
        "username" => {
            let u = f.take("username")?;
            let domain = f.take("domain")?;
            format!("{} ಅಟ್ {}", electronic_content(u)?, electronic_content(domain)?)
        }
        "protocol" => {
            let p = f.take("protocol")?;
            let domain = f.take("domain")?;
            format!(
                "{} {}",
                table(include_str!("../../../data/kn/electronic/protocols.tsv"), p)?,
                electronic_content(domain)?
            )
        }
        "domain" => electronic_content(f.take("domain")?)?,
        "path" => electronic_content(f.take("path")?)?,
        "fragment_id" => {
            let mut words = Vec::new();
            for c in f.take("fragment_id")?.chars() {
                let k = c.to_ascii_lowercase().to_string();
                if c.is_ascii_alphabetic() {
                    words.push(table(include_str!("../../../data/kn/address/letters.tsv"), &k)?.to_owned());
                } else {
                    words.push(electronic_content(&c.to_string())?);
                }
            }
            words.join(" ")
        }
        _ => return None,
    };
    f.done().then_some(a)
}
fn roman(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let s = if let Some(k) = f.opt("key_cardinal")? {
        let i = f.take("integer")?;
        format!("{k} {}", table(include_str!("../../../data/kn/roman/roman_to_spoken.tsv"), i)?)
    } else {
        let i = f.take("integer")?;
        if let Some(o) = f.opt("default_ordinal")? {
            let k = f.opt("key_cardinal")?;
            format!("{o}{}", k.map(|k| format!(" {k}")).unwrap_or_default())
        } else {
            let k = f.take("key_cardinal")?;
            format!("{} {k}", table(include_str!("../../../data/kn/roman/roman_to_spoken.tsv"), i)?)
        }
    };
    f.preserve_order();
    f.done().then_some(s)
}
fn nonempty(d: &Dict) -> bool {
    d.iter().all(|(_, v)| match v {
        Value::Str(s) => !s.is_empty(),
        Value::Dict(fields) => nonempty(fields),
        Value::True => true,
        _ => false,
    })
}
pub fn verbalize(token: &Dict) -> Option<String> {
    if !nonempty(token) {
        return None;
    }
    if verbalize_name(token).is_some() {
        let [(_, Value::Str(value))] = token.as_slice() else { return None };
        return Some(value.to_string());
    }
    let (key, value) = class(token)?;
    let Value::Dict(d) = value else { return None };
    let s = match key {
        "cardinal" => cardinal(d),
        "decimal" => decimal(d),
        "date" => date(d),
        "time" => time(d),
        "fraction" => fraction(d),
        "measure" => measure(d),
        "money" => money(d),
        "electronic" => electronic(d),
        "roman" => roman(d),
        "ordinal" => {
            let mut f = Fields::new(d);
            let x = f.take("integer")?;
            f.done().then(|| x.to_owned())
        }
        "telephone" => {
            let mut f = Fields::new(d);
            let c = f.opt("country_code")?;
            let n = f.take("number_part")?;
            let e = f.opt("extension")?;
            if !f.done() {
                return None;
            }
            Some([c.map(str::trim_end), Some(n.trim_end()), e].into_iter().flatten().collect::<Vec<_>>().join(" "))
        }
        _ => None,
    }?;
    Some(s.replace('\u{a0}', " "))
}

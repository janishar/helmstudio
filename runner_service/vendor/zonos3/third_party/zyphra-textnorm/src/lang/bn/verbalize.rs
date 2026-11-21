//! Bengali Verbalizer.
use super::{electronic, roman};
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn sign(f: &mut Fields<'_, '_>) -> Option<String> {
    Some(match f.opt("negative")? {
        Some("true") => "ঋণ ".to_owned(),
        Some("approx") => "প্রায় ".to_owned(),
        Some("pm") => "যোগ বা বিয়োগ ".to_owned(),
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
    if !f.done() || (frac.is_none() && q.is_none()) {
        return None;
    }
    let mut o = format!("{s}{n}");
    if let Some(v) = frac {
        if v.is_empty() {
            return None;
        }
        o.push_str(" দশমিক ");
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
    if let Some(feat) = f.opt("morphosyntactic_features")? {
        if feat.is_empty() || !f.done() {
            return None;
        }
        o.push_str(feat);
        return Some(o);
    }

    if let Some(int_part) = f.opt("integer_part")? {
        o.push_str(int_part);
        o.push_str(" পূর্ণ ");
    }

    // In Bengali: denominator + " ভাগের " + numerator
    let (num, den) = if f.peek_key() == Some("denominator") {
        let den = f.take("denominator")?;
        let num = f.take("numerator")?;
        (num, den)
    } else {
        let num = f.take("numerator")?;
        let den = f.take("denominator")?;
        (num, den)
    };

    if !f.done() {
        return None;
    }
    o.push_str(den);
    o.push_str(" ভাগের ");
    o.push_str(num);
    Some(o)
}

fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();

    if f.peek_key() == Some("era") {
        let era = f.take("era")?;
        f.preserve_order();
        if f.done() {
            return Some(era.to_owned());
        }
    }

    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    f.preserve_order();

    if !f.done() {
        return None;
    }

    let mut parts = Vec::new();
    if let Some(d) = day {
        parts.push(d);
    }
    if let Some(m) = month {
        parts.push(m);
    }
    if let Some(y) = year {
        parts.push(y);
    }
    if let Some(e) = era {
        parts.push(e);
    }

    if parts.is_empty() {
        return None;
    }
    Some(parts.join(" "))
}

fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if let Some(feat) = f.opt("morphosyntactic_features")? {
        let zone = f.opt("zone")?;
        if !f.done() {
            return None;
        }
        return Some(format!("{feat}{}", zone.map(|z| format!(" {z}")).unwrap_or_default()));
    }

    let period = f.opt("suffix")?;
    let h = f.take("hours")?;
    let m = f.opt("minutes")?;
    let s = f.opt("seconds")?;
    let zone = f.opt("zone")?;

    if !f.done() {
        return None;
    }

    let mut out = String::new();
    if let Some(p) = period {
        out.push_str(p);
        out.push(' ');
    }
    out.push_str(h);
    out.push('ট');
    out.push('া');

    if let Some(sec) = s {
        if let Some(min) = m {
            out.push(' ');
            out.push_str(min);
            out.push_str(" মিনিট ");
            out.push_str(sec);
            out.push_str(" সেকেন্ড");
        } else {
            out.push(' ');
            out.push_str(sec);
            out.push_str(" সেকেন্ড");
        }
    } else if let Some(min) = m {
        out.push(' ');
        out.push_str(min);
        out.push_str(" মিনিট");
    }

    if let Some(z) = zone {
        out.push(' ');
        out.push_str(z);
    }

    Some(out)
}

fn ordinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let int_w = f.take("integer")?;
    if !f.done() {
        return None;
    }
    Some(int_w.to_owned())
}

fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let val_str = if f.peek_key() == Some("cardinal") {
        cardinal(f.dict("cardinal")?)?
    } else if f.peek_key() == Some("decimal") {
        decimal(f.dict("decimal")?)?
    } else {
        return None;
    };

    let unit = f.take("units")?;
    if !f.done() {
        return None;
    }
    Some(format!("{val_str} {unit}"))
}

fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = sign(&mut f)?;
    let maj = f.take("currency_maj")?;
    let int_part = f.take("integer_part")?;
    let frac = f.opt("fractional_part")?;
    let min = f.opt("currency_min")?;
    let per = f.opt("morphosyntactic_features")?;

    if !f.done() {
        return None;
    }

    let core = match (frac, min) {
        (None, None) => format!("{int_part} {maj}"),
        (Some(f_val), Some(m_val)) if int_part == "শূন্য" => format!("{f_val} {m_val}"),
        (Some(f_val), Some(m_val)) => format!("{int_part} {maj} {f_val} {m_val}"),
        (Some(f_val), None) => format!("{int_part} দশমিক {f_val} {maj}"),
        _ => return None,
    };

    Some(format!("{s}{core}{}", per.map(|p| format!(" {p}")).unwrap_or_default()))
}

fn telephone(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let country = f.opt("country_code")?;
    let num = f.take("number_part")?.trim_end_matches(' ');
    let ext = f.opt("extension")?;

    if !f.done() {
        return None;
    }

    Some(format!(
        "{}{}{}",
        country.map(|c| format!("{c} ")).unwrap_or_default(),
        num,
        ext.map(|e| format!(" {e}")).unwrap_or_default()
    ))
}

fn electronic_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    match f.peek_key()? {
        "fragment_id" => {
            let content = f.take("fragment_id")?;
            if !f.done() {
                return None;
            }
            electronic::verbalize_content(content, true)
        }
        "protocol" => {
            let protocol = electronic::protocol_word(f.take("protocol")?)?;
            let domain = electronic::verbalize_content(f.take("domain")?, false)?;
            if !f.done() {
                return None;
            }
            Some(format!("{protocol} {domain}"))
        }
        "username" => {
            let user = electronic::verbalize_content(f.take("username")?, false)?;
            let domain = electronic::verbalize_content(f.take("domain")?, false)?;
            if !f.done() {
                return None;
            }
            Some(format!("{user} অ্যাট {domain}"))
        }
        "domain" | "path" => {
            let key = f.peek_key()?;
            let result = electronic::verbalize_content(f.take(key)?, false)?;
            f.done().then_some(result)
        }
        _ => None,
    }
}

fn roman_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();

    let mut out = String::new();
    if f.peek_key() == Some("integer") && d.iter().any(|(key, _)| key == "default_ordinal") {
        let integer = f.take("integer")?;
        if integer.is_empty() {
            return None;
        }
        let ord = f.take("default_ordinal")?;
        out.push_str(ord);
        if let Some(key) = f.opt("key_cardinal")? {
            out.push(' ');
            out.push_str(key);
        }
        f.preserve_order();
        return f.done().then_some(out);
    }

    if f.peek_key() == Some("key_cardinal") {
        let key = f.take("key_cardinal")?;
        let int_val = f.take("integer")?;
        f.preserve_order();
        if !f.done() {
            return None;
        }
        out.push_str(key);
        out.push(' ');
        out.push_str(roman::spoken(int_val)?);
    } else {
        let int_val = f.take("integer")?;
        let key = f.opt("key_cardinal")?;
        f.preserve_order();
        if !f.done() {
            return None;
        }
        out.push_str(roman::spoken(int_val)?);
        if let Some(k) = key {
            out.push(' ');
            out.push_str(k);
        }
    }
    Some(out)
}

pub fn verbalize(token: &Dict) -> Option<String> {
    let (class, value) = common::class(token)?;
    if class == "name" {
        let Value::Str(name) = value else { return None };
        return (!name.is_empty() && !name.contains(' ')).then(|| name.to_string());
    }

    let Value::Dict(fields) = value else { return None };
    match class {
        "cardinal" => cardinal(fields),
        "decimal" => decimal(fields),
        "fraction" => fraction(fields),
        "date" => date(fields),
        "time" => time(fields),
        "ordinal" => ordinal(fields),
        "measure" => measure(fields),
        "money" => money(fields),
        "telephone" => telephone(fields),
        "electronic" => electronic_v(fields),
        "roman" => roman_v(fields),
        _ => None,
    }
}

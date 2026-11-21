//! Urdu verbalizer.

use super::taggers::roman;
use super::taggers::serial;
use super::taggers::telephone;
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

/// Write only already-valid identity orderings. Other shapes must fall back
/// to permutation and the general verbalizer.
pub(crate) fn append_simple(class: &str, fields: &Dict, out: &mut String) -> bool {
    fn append_time(fields: &Dict, out: &mut String) -> Option<()> {
        let mut f = Fields::new(fields);
        if let Some(feat) = f.opt("morphosyntactic_features")? {
            let zone = f.opt("zone")?;
            if !f.done() || feat.is_empty() || zone.is_some_and(str::is_empty) {
                return None;
            }
            out.push_str(feat);
            if let Some(zone) = zone {
                out.push(' ');
                out.push_str(zone);
            }
            return Some(());
        }
        let suffix = f.opt("suffix")?;
        let hours = f.take("hours")?;
        let minutes = f.opt("minutes")?;
        let seconds = f.opt("seconds")?;
        let zone = f.opt("zone")?;
        if !f.done()
            || hours.is_empty()
            || (seconds.is_some() && minutes.is_none())
            || [suffix, minutes, seconds, zone].into_iter().flatten().any(str::is_empty)
        {
            return None;
        }
        if let Some(suffix) = suffix {
            out.push_str(suffix);
            out.push(' ');
        }
        out.push_str(hours);
        if let Some(minutes) = minutes {
            out.push_str(" بج کر ");
            out.push_str(minutes);
            out.push_str(" منٹ");
            if let Some(seconds) = seconds {
                out.push(' ');
                out.push_str(seconds);
                out.push_str(" سیکنڈ");
            }
        } else {
            out.push_str(" بجے");
        }
        if let Some(zone) = zone {
            out.push(' ');
            out.push_str(zone);
        }
        Some(())
    }

    fn append_measure(fields: &Dict, out: &mut String) -> Option<()> {
        let mut f = Fields::new(fields);
        let card = f.dict("cardinal")?;
        let units = f.take("units")?;
        if !f.done() || units.is_empty() || units == "address" {
            return None;
        }
        let mut n = Fields::new(card);
        let negative = n.opt("negative")?;
        if negative.is_some_and(|value| value != "true") {
            return None;
        }
        let integer = n.take("integer")?;
        if !n.done() {
            return None;
        }
        if negative.is_some() {
            out.push_str("منفی ");
        }
        out.push_str(integer);
        out.push(' ');
        out.push_str(units);
        Some(())
    }

    fn append_money(fields: &Dict, out: &mut String) -> Option<()> {
        // With exactly two fields the first successful permutation swaps
        // leading currency into the source's integer-first ordering.
        if let [(major_key, Value::Str(major)), (integer_key, Value::Str(integer))] = fields.as_slice() {
            if major_key == "currency_maj"
                && integer_key == "integer_part"
                && !major.is_empty()
                && !integer.is_empty()
                && !major.contains('"')
                && !integer.contains('"')
            {
                out.push_str(integer);
                out.push(' ');
                out.push_str(major);
                return Some(());
            }
        }
        let mut f = Fields::new(fields);
        let negative = f.opt("negative")?;
        if negative.is_some_and(|value| value != "true") {
            return None;
        }
        let integer = f.take("integer_part")?;
        let major = f.take("currency_maj")?;
        let unit = f.opt("morphosyntactic_features")?;
        if !f.done() || integer.is_empty() || major.is_empty() || unit.is_some_and(str::is_empty) {
            return None;
        }
        if negative.is_some() {
            out.push_str("منفی ");
        }
        out.push_str(integer);
        out.push(' ');
        out.push_str(major);
        if let Some(unit) = unit {
            out.push(' ');
            out.push_str(unit);
        }
        Some(())
    }

    match class {
        "time" => append_time(fields, out),
        "measure" => append_measure(fields, out),
        "money" => append_money(fields, out),
        _ => None,
    }
    .is_some()
}

fn sign(f: &mut Fields<'_, '_>) -> Option<String> {
    Some(match f.opt("negative")? {
        Some("true") => "منفی ".to_owned(),
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
        o.push_str(" اعشاریہ ");
        o.push_str(v);
    }
    if let Some(v) = q {
        if !["ہزار", "لاکھ", "کروڑ", "ارب", "کھرب"].contains(&v) {
            return None;
        }
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
    let int_part = f.opt("integer_part")?;
    let num = f.take("numerator")?;
    let den = f.take("denominator")?;
    if !f.done() {
        return None;
    }
    if let Some(ip) = int_part {
        if !ip.is_empty() {
            o.push_str(ip);
            o.push_str("  اور ");
        }
    }
    o.push_str(num);
    o.push_str(" بٹا ");
    o.push_str(den);
    Some(o)
}

fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    let valid = match (day, month, year, era) {
        (Some(_), Some(_), None, None)
        | (Some(_), Some(_), Some(_), _)
        | (None, Some(_), Some(_), _)
        | (None, None, None, Some(_)) => true,
        _ => false,
    };
    if !valid || [day, month, year, era].into_iter().flatten().any(str::is_empty) {
        return None;
    }
    loop {
        if f.preserve_order() {
            continue;
        }
        if f.peek_key() == Some("field_order") {
            if f.take("field_order")?.chars().count() != 1 {
                return None;
            }
            continue;
        }
        break;
    }
    if !f.done() {
        return None;
    }
    Some([day, month, year, era].into_iter().flatten().collect::<Vec<_>>().join(" "))
}

fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if let Some(feat) = f.opt("morphosyntactic_features")? {
        let zone = f.opt("zone")?;
        if !f.done() || feat.is_empty() || zone.is_some_and(str::is_empty) {
            return None;
        }
        let mut o = feat.to_string();
        if let Some(z) = zone {
            o.push(' ');
            o.push_str(z);
        }
        return Some(o);
    }

    let suffix = f.opt("suffix")?;
    let hours = f.take("hours")?;
    let minutes = f.opt("minutes")?;
    let seconds = f.opt("seconds")?;
    let zone = f.opt("zone")?;

    if !f.done()
        || hours.is_empty()
        || (seconds.is_some() && minutes.is_none())
        || [suffix, minutes, seconds, zone].into_iter().flatten().any(str::is_empty)
    {
        return None;
    }

    let mut o = String::new();
    if let Some(sfx) = suffix {
        o.push_str(sfx);
        o.push(' ');
    }

    if let (Some(m), Some(s)) = (minutes, seconds) {
        o.push_str(hours);
        o.push_str(" بج کر ");
        o.push_str(m);
        o.push_str(" منٹ ");
        o.push_str(s);
        o.push_str(" سیکنڈ");
    } else if let Some(m) = minutes {
        o.push_str(hours);
        o.push_str(" بج کر ");
        o.push_str(m);
        o.push_str(" منٹ");
    } else {
        o.push_str(hours);
        o.push_str(" بجے");
    }

    if let Some(z) = zone {
        o.push(' ');
        o.push_str(z);
    }

    Some(o)
}

fn ordinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let n = f.take("integer")?;
    if n.is_empty() || !f.done() {
        return None;
    }
    Some(n.to_string())
}

fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let result = if f.peek_key() == Some("units") {
        if f.take("units")? != "address" {
            return None;
        }
        let card = f.dict("cardinal")?;
        let result = cardinal(card)?;
        while f.preserve_order() {}
        result
    } else {
        let number = match f.peek_key()? {
            "cardinal" => cardinal(f.dict("cardinal")?)?,
            "decimal" => decimal(f.dict("decimal")?)?,
            _ => return None,
        };
        let units = f.take("units")?;
        if units.is_empty() || units == "address" {
            return None;
        }
        format!("{number} {units}")
    };
    f.done().then_some(result)
}

fn money(d: &Dict) -> Option<String> {
    const CURRENCIES: &str = include_str!("../../../data/ur/money/currency.tsv");
    const SINGULAR: &str = include_str!("../../../data/ur/money/currency_singular.tsv");
    const MAJOR_MINOR: &str = include_str!("../../../data/ur/money/major_minor_currencies.tsv");
    let mut f = Fields::new(d);
    let mut out = sign(&mut f)?;
    if f.peek_key() == Some("integer_part") {
        let integer = f.take("integer_part")?;
        let major = f.take("currency_maj")?;
        if integer.is_empty() || major.is_empty() {
            return None;
        }
        out.push_str(integer);
        out.push(' ');
        out.push_str(major);
    } else {
        let major = f.take("currency_maj")?;
        let known = CURRENCIES
            .lines()
            .chain(SINGULAR.lines())
            .filter_map(|l| l.split_once('\t'))
            .any(|(_, value)| value.trim_end_matches('\r') == major)
            || MAJOR_MINOR.lines().filter_map(|l| l.split_once('\t')).any(|(value, _)| value == major);
        if !known {
            return None;
        }
        let integer = f.take("integer_part")?;
        let fraction = f.take("fractional_part")?;
        if integer.is_empty() || fraction.is_empty() {
            return None;
        }
        if let Some(minor) = f.opt("currency_min")? {
            if minor.is_empty() {
                return None;
            }
            if integer != "صفر" {
                out.push_str(integer);
                out.push(' ');
                out.push_str(major);
                out.push(' ');
            }
            out.push_str(fraction);
            out.push(' ');
            out.push_str(minor);
        } else {
            out.push_str(integer);
            out.push_str(" اعشاریہ ");
            out.push_str(fraction);
            out.push(' ');
            out.push_str(major);
        }
    }
    if let Some(unit) = f.opt("morphosyntactic_features")? {
        if unit.is_empty() {
            return None;
        }
        out.push(' ');
        out.push_str(unit);
    }
    f.done().then_some(out)
}

fn telephone_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let cc = f.opt("country_code")?;
    let num = f.take("number_part")?;
    let ext = f.opt("extension")?;

    if !f.done() || num.is_empty() || cc.is_some_and(str::is_empty) || ext.is_some_and(str::is_empty) {
        return None;
    }

    let mut o = String::new();
    if let Some(c) = cc {
        o.push_str(c);
        o.push(' ');
    }
    o.push_str(num.strip_suffix(' ').unwrap_or(num));
    if let Some(e) = ext {
        o.push(' ');
        o.push_str(e);
    }
    Some(o)
}

fn verbalize_electronic_content(s: &str) -> Option<String> {
    let mut out = Vec::new();
    let mut latin_buf = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            latin_buf.push(c);
        } else {
            if !latin_buf.is_empty() {
                out.push(std::mem::take(&mut latin_buf));
            }
            let spoken = electronic_digit(c).or_else(|| electronic_symbol_to_spoken(c))?;
            out.push(spoken.to_owned());
        }
    }
    if !latin_buf.is_empty() {
        out.push(latin_buf);
    }
    Some(out.join(" "))
}

fn electronic_digit(c: char) -> Option<&'static str> {
    if let Some(d) = telephone::digit_to_spoken(c) {
        return Some(d);
    }
    let n = (c as u32).checked_sub('₀' as u32)?;
    let digit = char::from_u32('0' as u32 + n)?;
    telephone::digit_to_spoken(digit)
}

fn electronic_symbol_to_spoken(c: char) -> Option<&'static str> {
    match c {
        '.' => Some("ڈاٹ"),
        '-' => Some("ہائفن"),
        '_' => Some("انڈر اسکور"),
        '@' => Some("ایٹ"),
        '/' => Some("سلیش"),
        '\\' => Some("بیک سلیش"),
        ':' => Some("کولن"),
        '#' => Some("ہیش ٹیگ"),
        '$' => Some("ڈالر"),
        '%' => Some("فیصد"),
        '&' => Some("اینڈ"),
        '+' => Some("پلس"),
        '=' => Some("ایکول"),
        '?' => Some("سوالیہ نشان"),
        '*' => Some("اسٹار"),
        '!' => Some("ایکسکلیمیشن مارک"),
        '`' => Some("بیک ٹک"),
        ' ' => Some("اسپیس"),
        '\'' => Some("سنگل کوٹ"),
        '"' => Some("ڈبل کوٹ"),
        '<' => Some("لیس دین"),
        '>' => Some("گریٹر دین"),
        '~' => Some("ٹلڈ"),
        '^' => Some("کیرٹ"),
        '|' => Some("پائپ"),
        ';' => Some("سیمی کولن"),
        ',' => Some("کوما"),
        '(' => Some("اوپن بریکٹ"),
        ')' => Some("کلوز بریکٹ"),
        '[' => Some("اوپن اسکوائر بریکٹ"),
        ']' => Some("کلوز اسکوائر بریکٹ"),
        '{' => Some("اوپن کرلی بریکٹ"),
        '}' => Some("کلوز کرلی بریکٹ"),
        '–' | '⁻' => Some("مائنس"),
        '⁺' => Some("پلس"),
        _ => None,
    }
}

fn electronic_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if let Some(chem) = f.opt("fragment_id")? {
        if !f.done() {
            return None;
        }
        let mut out = Vec::new();
        for c in chem.chars() {
            if c.is_ascii_alphabetic() {
                out.push(serial::letter_to_spoken(c)?.to_string());
            } else {
                out.push(electronic_digit(c).or_else(|| electronic_symbol_to_spoken(c))?.to_string());
            }
        }
        return Some(out.join(" "));
    }

    if let Some(path) = f.opt("path")? {
        if !f.done() {
            return None;
        }
        return verbalize_electronic_content(path);
    }

    let prot = f.opt("protocol")?;
    let user = f.opt("username")?;
    let domain = f.opt("domain")?;

    if !f.done() {
        return None;
    }

    let mut out = Vec::new();
    if let Some(p) = prot {
        match p {
            "https" => out.push("https کولن سلیش سلیش".to_string()),
            "http" => out.push("http کولن سلیش سلیش".to_string()),
            "www" => out.push("www ڈاٹ".to_string()),
            "httpswww" => out.push("https کولن سلیش سلیش www ڈاٹ".to_string()),
            "httpwww" => out.push("http کولن سلیش سلیش www ڈاٹ".to_string()),
            _ => return None,
        }
    }
    if let Some(u) = user {
        out.push(verbalize_electronic_content(u)?);
        out.push("ایٹ".to_string());
    }
    if let Some(dom) = domain {
        out.push(verbalize_electronic_content(dom)?);
    }

    if domain.is_none() || (prot.is_some() && user.is_some()) { None } else { Some(out.join(" ")) }
}

fn roman_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let out = if f.peek_key() == Some("key_cardinal") {
        let key = f.take("key_cardinal")?;
        if key.is_empty() {
            return None;
        }
        let integer = roman::spoken(f.take("integer")?)?;
        format!("{key} {integer}")
    } else {
        let integer = f.take("integer")?;
        if integer.is_empty() {
            return None;
        }
        if let Some(ordinal) = f.opt("default_ordinal")? {
            if ordinal.is_empty() {
                return None;
            }
            if let Some(key) = f.opt("key_cardinal")? {
                if key.is_empty() {
                    return None;
                }
                format!("{ordinal} {key}")
            } else {
                ordinal.to_owned()
            }
        } else {
            let key = f.take("key_cardinal")?;
            if key.is_empty() {
                return None;
            }
            format!("{} {key}", roman::spoken(integer)?)
        }
    };
    f.preserve_order();
    f.done().then(|| if out.contains('\u{a0}') { out.replace('\u{a0}', " ") } else { out })
}

pub fn verbalize(token: &Dict) -> Option<String> {
    let (class, value) = common::class(token)?;
    if class == "name" {
        let Value::Str(name) = value else { return None };
        return (!name.is_empty()).then(|| name.to_string());
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
        "telephone" => telephone_v(fields),
        "electronic" => electronic_v(fields),
        "roman" => roman_v(fields),
        _ => None,
    }
}

//! Telugu Verbalizer.

use super::{money, numeric, roman};
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn sign(f: &mut Fields<'_, '_>) -> Option<String> {
    Some(match f.opt("negative")? {
        Some("true") => "ఋణ ".to_string(),
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
    let int_part = f.take("integer_part")?;
    let frac = f.opt("fractional_part")?;
    let qty = f.opt("quantity")?;
    if !f.done() || (frac.is_none() && qty.is_none()) || qty.is_some_and(|q| !numeric::is_quantity(q)) {
        return None;
    }

    let mut out = format!("{s}{int_part}");
    if let Some(fr) = frac {
        out.push_str(" దశాంశం ");
        out.push_str(fr);
    }
    if let Some(q) = qty {
        out.push(' ');
        out.push_str(q);
    }
    Some(out)
}

fn locative_denominator(s: &str) -> String {
    if let Some(base) = s.strip_suffix('ు') {
        format!("{base}ింటిలో")
    } else if let Some(base) = s.strip_suffix('ి') {
        format!("{base}ింటిలో")
    } else {
        format!("{s}లో")
    }
}

fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let s = sign(&mut f)?;
    if let Some(morph) = f.opt("morphosyntactic_features")? {
        if !f.done() {
            return None;
        }
        return Some(format!("{s}{morph}"));
    }

    let int_part = f.opt("integer_part")?;
    let den = f.take("denominator")?;
    let num = f.take("numerator")?;
    if !f.done() {
        return None;
    }

    let den_loc = locative_denominator(den);
    let mut out = s;
    if let Some(i) = int_part {
        out.push_str(i);
        out.push_str(" మరియు ");
    }
    out.push_str(&den_loc);
    out.push(' ');
    out.push_str(num);
    Some(out)
}

fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    if !f.done() {
        return None;
    }

    let mut out = String::new();
    match (day, month, year) {
        (Some(dy), Some(m), Some(y)) => {
            out.push_str(dy);
            out.push(' ');
            out.push_str(m);
            out.push(' ');
            out.push_str(y);
        }
        (Some(dy), Some(m), None) => {
            if era.is_some() {
                return None;
            }
            out.push_str(dy);
            out.push(' ');
            out.push_str(m);
        }
        (None, Some(m), Some(y)) => {
            out.push_str(m);
            out.push(' ');
            out.push_str(y);
        }
        (None, None, Some(y)) => {
            out.push_str(y);
        }
        _ => return None,
    }

    if let Some(e) = era {
        out.push(' ');
        out.push_str(e);
    }
    Some(out)
}

// Exact ordered HM shape; all other time dictionaries use the general
// permutation/verbalizer path.
pub fn append_time_hm(out: &mut String, fields: &Dict) -> bool {
    let [(h_key, Value::Str(hours)), (m_key, Value::Str(minutes))] = fields.as_slice() else { return false };
    if h_key != "hours" || m_key != "minutes" || hours.contains('"') || minutes.contains('"') {
        return false;
    }
    let mut append = |s: &str| {
        for c in s.chars() {
            out.push(if c == '\u{a0}' { ' ' } else { c });
        }
    };
    if hours == "ఒకటి" {
        append("ఒంటి గంట");
    } else {
        append(hours);
        append(" గంటల");
    }
    append(" ");
    if minutes == "ఒకటి" {
        append("ఒక నిమిషం");
    } else {
        append(minutes);
        append(" నిమిషాలు");
    }
    true
}

fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let day_period = f.opt("day_period")?;
    let hours = f.take("hours")?;
    let minutes = f.opt("minutes")?;
    let seconds = f.opt("seconds")?;
    let zone = f.opt("zone")?;
    if !f.done() {
        return None;
    }

    let mut out = String::new();
    if let Some(dp) = day_period {
        out.push_str(dp);
        out.push(' ');
    }

    match (minutes, seconds) {
        (Some(m), Some(sec)) => {
            if hours == "ఒకటి" {
                out.push_str("ఒంటి గంట");
            } else {
                out.push_str(hours);
                out.push_str(" గంటల");
            }
            out.push(' ');
            if m == "ఒకటి" {
                out.push_str("ఒక నిమిషం");
            } else {
                out.push_str(m);
                out.push_str(" నిమిషాల");
            }
            out.push(' ');
            if sec == "ఒకటి" {
                out.push_str("ఒక సెకను");
            } else {
                out.push_str(sec);
                out.push_str(" సెకన్లు");
            }
        }
        (Some(m), None) => {
            if hours == "ఒకటి" {
                out.push_str("ఒంటి గంట");
            } else {
                out.push_str(hours);
                out.push_str(" గంటల");
            }
            out.push(' ');
            if m == "ఒకటి" {
                out.push_str("ఒక నిమిషం");
            } else {
                out.push_str(m);
                out.push_str(" నిమిషాలు");
            }
        }
        (None, None) => {
            if hours == "ఒకటి" {
                out.push_str("ఒంటి గంట");
            } else {
                out.push_str(hours);
                out.push_str(" గంటలు");
            }
        }
        (None, Some(_)) => return None,
    }

    if let Some(z) = zone {
        out.push(' ');
        out.push_str(z);
    }
    Some(out)
}

fn ordinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let n = f.take("integer")?;
    if !f.done() {
        return None;
    }
    Some(n.to_string())
}

fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let morph = f.opt("morphosyntactic_features")?;
    let num_val = match f.peek_key()? {
        "cardinal" => cardinal(f.dict("cardinal")?)?,
        "decimal" => decimal(f.dict("decimal")?)?,
        _ => return None,
    };
    let units = f.take("units")?;
    f.preserve_order();
    if !f.done() {
        return None;
    }

    let mut out = String::new();
    if let Some(m) = morph {
        out.push_str(m);
        out.push(' ');
    }
    out.push_str(&num_val);
    out.push(' ');
    out.push_str(units);
    Some(out)
}

fn append_typed(out: &mut String, value: &str) {
    for c in value.chars() {
        out.push(if c == '\u{a0}' { ' ' } else { c });
    }
}

pub fn append_plain_measure(out: &mut String, fields: &Dict) -> bool {
    let (number, units) = match fields.as_slice() {
        [(key, Value::Dict(number)), (unit_key, Value::Str(units))] if key == "cardinal" && unit_key == "units" => {
            (number, units)
        }
        [(key, Value::Dict(number)), (unit_key, Value::Str(units)), (order_key, Value::True)]
            if key == "cardinal" && unit_key == "units" && order_key == "preserve_order" =>
        {
            (number, units)
        }
        _ => return false,
    };
    let [(key, Value::Str(integer))] = number.as_slice() else { return false };
    if key != "integer" || integer.contains('"') || units.contains('"') {
        return false;
    }
    append_typed(out, integer);
    out.push(' ');
    append_typed(out, units);
    true
}

pub fn append_plain_money(out: &mut String, fields: &Dict) -> bool {
    let [(int_key, Value::Str(integer)), (curr_key, Value::Str(currency))] = fields.as_slice() else { return false };
    if int_key != "integer_part" || curr_key != "currency_maj" || integer.contains('"') || currency.contains('"') {
        return false;
    }
    append_typed(out, integer);
    out.push(' ');
    append_typed(out, currency);
    true
}

fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let morph = f.opt("morphosyntactic_features")?;
    let s = sign(&mut f)?;
    let major_first = f.peek_key() == Some("currency_maj");
    let curr_maj = if major_first { Some(f.take("currency_maj")?) } else { None };
    let int_part = f.take("integer_part")?;
    let curr_maj = if major_first { curr_maj? } else { f.take("currency_maj")? };
    let frac_part = f.opt("fractional_part")?;
    let curr_min = f.opt("currency_min")?;
    if !f.done()
        || (major_first && (frac_part.is_none() || !money::is_major_name(curr_maj)))
        || (!major_first && frac_part.is_some())
    {
        return None;
    }

    let mut prefix = String::new();
    if let Some(m) = morph {
        prefix.push_str(m);
        prefix.push(' ');
    }
    prefix.push_str(&s);

    if let Some(c_min) = curr_min {
        let fr = frac_part?;
        if int_part == "సున్నా" {
            Some(format!("{prefix}{fr} {c_min}"))
        } else {
            Some(format!("{prefix}{int_part} {curr_maj} {fr} {c_min}"))
        }
    } else if let Some(fr) = frac_part {
        Some(format!("{prefix}{int_part}  దశాంశం {fr} {curr_maj}"))
    } else {
        Some(format!("{prefix}{int_part} {curr_maj}"))
    }
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
    if let Some(c) = cc {
        out.push_str(c);
        out.push(' ');
    }
    out.push_str(num.strip_suffix(' ').filter(|v| !v.is_empty()).unwrap_or(num));
    if let Some(e) = ext {
        out.push(' ');
        out.push_str(e);
    }
    Some(out)
}

fn symbol_to_te(c: char) -> Option<&'static str> {
    match c {
        '.' => Some("డాట్"),
        '-' => Some("హైఫన్"),
        '_' => Some("అండర్‌స్కోర్"),
        '@' => Some("ఎట్"),
        '/' => Some("స్లాష్"),
        '\\' => Some("బ్యాక్‌స్లాష్"),
        ':' => Some("కోలన్"),
        '#' => Some("హ్యాష్"),
        '$' => Some("డాలర్"),
        '%' => Some("శాతం"),
        '&' => Some("అండ్"),
        '+' => Some("ప్లస్"),
        '=' => Some("ఈక్వల్స్"),
        '?' => Some("క్వశ్చన్ మార్క్"),
        '*' => Some("స్టార్"),
        '!' => Some("ఎక్స్‌క్లమేషన్ మార్క్"),
        '~' => Some("టిల్డ్"),
        '^' => Some("క్యారెట్"),
        '|' => Some("పైప్"),
        ';' => Some("సెమికోలన్"),
        ',' => Some("కామా"),
        '(' => Some("ఓపెన్ బ్రాకెట్"),
        ')' => Some("క్లోజ్ బ్రాకెట్"),
        ' ' => Some("స్పేస్"),
        '–' | '⁻' => Some("మైనస్"),
        '⁺' => Some("ప్లస్"),
        '`' => Some("బ్యాక్‌టిక్"),
        '\'' => Some("సింగిల్ కోట్"),
        '"' => Some("డబుల్ కోట్"),
        '<' => Some("లెస్ దాన్"),
        '>' => Some("గ్రేటర్ దాన్"),
        '[' => Some("ఓపెన్ స్క్వేర్ బ్రాకెట్"),
        ']' => Some("క్లోజ్ స్క్వేర్ బ్రాకెట్"),
        '{' => Some("ఓపెన్ కర్లీ బ్రాకెట్"),
        '}' => Some("క్లోజ్ కర్లీ బ్రాకెట్"),
        _ => None,
    }
}

fn digit_to_te(c: char) -> Option<&'static str> {
    match c {
        '0' | '౦' | '₀' => Some("సున్నా"),
        '1' | '౧' | '₁' => Some("ఒకటి"),
        '2' | '౨' | '₂' => Some("రెండు"),
        '3' | '౩' | '₃' => Some("మూడు"),
        '4' | '౪' | '₄' => Some("నాలుగు"),
        '5' | '౫' | '₅' => Some("ఐదు"),
        '6' | '౬' | '₆' => Some("ఆరు"),
        '7' | '౭' | '₇' => Some("ఏడు"),
        '8' | '౮' | '₈' => Some("ఎనిమిది"),
        '9' | '౯' | '₉' => Some("తొమ్మిది"),
        _ => None,
    }
}

fn letter_to_te(c: char) -> Option<&'static str> {
    match c.to_ascii_lowercase() {
        'a' => Some("ఎ"),
        'b' => Some("బి"),
        'c' => Some("సి"),
        'd' => Some("డి"),
        'e' => Some("ఇ"),
        'f' => Some("ఎఫ్"),
        'g' => Some("జి"),
        'h' => Some("హెచ్"),
        'i' => Some("ఐ"),
        'j' => Some("జె"),
        'k' => Some("కె"),
        'l' => Some("ఎల్"),
        'm' => Some("ఎం"),
        'n' => Some("ఎన్"),
        'o' => Some("ఓ"),
        'p' => Some("పి"),
        'q' => Some("క్యూ"),
        'r' => Some("ఆర్"),
        's' => Some("ఎస్"),
        't' => Some("టి"),
        'u' => Some("యు"),
        'v' => Some("వి"),
        'w' => Some("డబ్ల్యూ"),
        'x' => Some("ఎక్స్"),
        'y' => Some("వై"),
        'z' => Some("జెడ్"),
        _ => None,
    }
}

fn verbalize_electronic_content(s: &str) -> Option<String> {
    let mut out = String::new();
    let mut chars = s.chars().peekable();
    while let Some(&c) = chars.peek() {
        if c.is_ascii_alphabetic() {
            let mut alpha = String::new();
            while let Some(&ch) = chars.peek() {
                if ch.is_ascii_alphabetic() {
                    alpha.push(ch);
                    chars.next();
                } else {
                    break;
                }
            }
            if !out.is_empty() && !out.ends_with(' ') {
                out.push(' ');
            }
            out.push_str(&alpha);
        } else if let Some(d) = digit_to_te(c) {
            chars.next();
            if !out.is_empty() && !out.ends_with(' ') {
                out.push(' ');
            }
            out.push_str(d);
        } else if let Some(sym) = symbol_to_te(c) {
            chars.next();
            if !out.is_empty() && !out.ends_with(' ') {
                out.push(' ');
            }
            out.push_str(sym);
        } else {
            return None;
        }
    }
    Some(out)
}

fn verbalize_chemical(s: &str) -> Option<String> {
    let mut out = String::new();
    for c in s.chars() {
        let piece = if let Some(l) = letter_to_te(c) {
            l
        } else if let Some(d) = digit_to_te(c) {
            d
        } else if let Some(sym) = symbol_to_te(c) {
            sym
        } else {
            return None;
        };
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(piece);
    }
    if out.is_empty() { None } else { Some(out) }
}

fn electronic_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if let Some(frag) = f.opt("fragment_id")? {
        if !f.done() {
            return None;
        }
        return verbalize_chemical(frag);
    }

    let proto = f.opt("protocol")?;
    let user = f.opt("username")?;
    let domain = f.opt("domain")?;
    let path = f.opt("path")?;
    if !f.done()
        || !matches!(
            (proto, user, domain, path),
            (Some(_), None, Some(_), None)
                | (None, Some(_), Some(_), None)
                | (None, None, Some(_), None)
                | (None, None, None, Some(_))
        )
    {
        return None;
    }

    let mut out = String::new();
    if let Some(p) = proto {
        let p_spoken = match p {
            "https" => "https కోలన్ స్లాష్ స్లాష్",
            "http" => "http కోలన్ స్లాష్ స్లాష్",
            "www" => "www డాట్",
            "httpswww" => "https కోలన్ స్లాష్ స్లాష్ www డాట్",
            "httpwww" => "http కోలన్ స్లాష్ స్లాష్ www డాట్",
            _ => return None,
        };
        out.push_str(p_spoken);
        out.push(' ');
    }

    if let Some(u) = user {
        out.push_str(&verbalize_electronic_content(u)?);
        out.push_str(" ఎట్ ");
    }

    if let Some(dom) = domain {
        out.push_str(&verbalize_electronic_content(dom)?);
    }

    if let Some(pa) = path {
        out.push_str(&verbalize_electronic_content(pa)?);
    }

    Some(out.trim_end().to_owned())
}

fn roman_v(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let result = if f.peek_key() == Some("key_cardinal") {
        let key = f.take("key_cardinal")?;
        let integer = roman::spoken(f.take("integer")?)?;
        format!("{key} {integer}")
    } else {
        let integer = f.take("integer")?;
        if let Some(ordinal) = f.opt("default_ordinal")? {
            if let Some(key) = f.opt("key_cardinal")? { format!("{ordinal} {key}") } else { ordinal.to_owned() }
        } else {
            let spoken = roman::spoken(integer)?;
            let key = f.take("key_cardinal")?;
            format!("{spoken} {key}")
        }
    };
    f.preserve_order();
    f.done().then_some(result)
}

pub fn verbalize(token: &Dict) -> Option<String> {
    let (class, value) = common::class(token)?;
    if class == "name" {
        let Value::Str(name) = value else { return None };
        return (!name.is_empty() && !name.contains(' ')).then(|| name.to_string());
    }

    let Value::Dict(fields) = value else { return None };
    let out = match class {
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
    }?;
    Some(if out.contains('\u{a0}') { out.replace('\u{a0}', " ") } else { out })
}

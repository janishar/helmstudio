//! Ordered Malayalam verbalizers.
use super::{lexical, number};
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn sign(f: &mut Fields<'_, '_>) -> Option<&'static str> {
    match f.opt("negative")? {
        Some("true") => Some("മൈനസ് "),
        Some(_) => None,
        None => Some(""),
    }
}
fn nonempty(s: &str) -> Option<&str> {
    if s.is_empty() { None } else { Some(s) }
}
fn cardinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = sign(&mut f)?;
    let n = nonempty(f.take("integer")?)?;
    if !f.done() {
        return None;
    }
    Some(format!("{sign}{n}"))
}
fn decimal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = sign(&mut f)?;
    let n = nonempty(f.take("integer_part")?)?;
    let frac = f.opt("fractional_part")?;
    let quantity = f.opt("quantity")?;
    if !f.done() || (frac.is_none() && quantity.is_none()) {
        return None;
    }
    let mut out = format!("{sign}{n}");
    if let Some(frac) = frac {
        out.push_str(" ദശാംശം ");
        out.push_str(nonempty(frac)?)
    }
    if let Some(q) = quantity {
        out.push(' ');
        out.push_str(nonempty(q)?)
    }
    Some(out)
}
fn fraction(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = sign(&mut f)?;
    if let Some(m) = f.opt("morphosyntactic_features")? {
        return f.done().then(|| format!("{sign}{m}"));
    }
    let integer = f.opt("integer_part")?;
    let den = nonempty(f.take("denominator")?)?;
    let num = nonempty(f.take("numerator")?)?;
    if !f.done() {
        return None;
    }
    let den = if let Some(s) = den.strip_suffix('്') {
        format!("{s}ിൽ")
    } else if let Some(s) = den.strip_suffix('ം') {
        format!("{s}ത്തിൽ")
    } else {
        den.to_owned()
    };
    Some(format!("{sign}{}{den} {num}", integer.map(|i| format!("{i} ")).unwrap_or_default()))
}
fn date(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    if !f.done()
        || !matches!(
            (day.is_some(), month.is_some(), year.is_some(), era.is_some()),
            (true, true, false, false) | (true, true, true, _) | (false, true, true, _) | (false, false, true, _)
        )
    {
        return None;
    }
    Some([day, month, year, era].into_iter().flatten().map(nonempty).collect::<Option<Vec<_>>>()?.join(" "))
}
fn time(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let period = f.opt("day_period")?;
    let h = nonempty(f.take("hours")?)?;
    let min = f.opt("minutes")?;
    let sec = f.opt("seconds")?;
    let zone = f.opt("zone")?;
    if !f.done() || sec.is_some() && min.is_none() {
        return None;
    }
    let mut v = Vec::new();
    if let Some(p) = period {
        v.push(p.to_owned())
    }
    if min.is_none() {
        v.push(format!("{} മണി", if h == "ഒന്ന്" { "ഒരു" } else { h }));
    } else {
        v.push(h.to_owned());
        v.push(nonempty(min?)?.to_owned());
        if let Some(s) = sec {
            v.push(nonempty(s)?.to_owned())
        }
    }
    if let Some(z) = zone {
        v.push(nonempty(z)?.to_owned())
    }
    Some(v.join(" "))
}
fn measure(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let pre = f.opt("morphosyntactic_features")?;
    let n = if let Some(x) = f.dict("cardinal") { cardinal(x)? } else { decimal(f.dict("decimal")?)? };
    let unit = nonempty(f.take("units")?)?;
    f.preserve_order();
    if !f.done() {
        return None;
    }
    Some(format!("{}{n} {unit}", pre.map(|x| format!("{x} ")).unwrap_or_default()))
}
fn money(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let sign = sign(&mut f)?;
    let maj_first = f.peek_key() == Some("currency_maj");
    let (maj, int) = if maj_first {
        (f.take("currency_maj")?, f.take("integer_part")?)
    } else {
        let i = f.take("integer_part")?;
        (f.take("currency_maj")?, i)
    };
    nonempty(maj)?;
    nonempty(int)?;
    if maj_first
        && !matches!(
            maj,
            "രൂപ" | "ഡോളർ"
                | "യൂറോ"
                | "പൗണ്ട്"
                | "യെൻ"
                | "സ്വിസ് ഫ്രാങ്ക്"
                | "കനേഡിയൻ ഡോളർ"
                | "ഓസ്ട്രേലിയൻ ഡോളർ"
                | "ബ്രസീലിയൻ റിയാൽ"
                | "മെക്സിക്കൻ പെസോ"
                | "റഷ്യൻ റൂബിൾ"
                | "ദക്ഷിണ കൊറിയൻ വോൺ"
                | "ടർക്കിഷ് ലിറ"
                | "ഇസ്രായേലി ന്യൂ ഷെക്കെൽ"
                | "ഉക്രേനിയൻ ഹ്രിവ്‌നിയ"
                | "വിയറ്റ്നാമീസ് ഡോങ്"
                | "ബംഗ്ലാദേശി ടാക്ക"
                | "നൈജീരിയൻ നൈറ"
                | "ഫിലിപ്പീനി പെസോ"
                | "തായ് ബട്ട്"
                | "അർമേനിയൻ ഡ്രാം"
                | "സെന്റ്"
        )
    {
        return None;
    }
    let frac = f.opt("fractional_part")?;
    let min = f.opt("currency_min")?;
    let rate = f.opt("morphosyntactic_features")?;
    if !f.done() || frac.is_some() != min.is_some() {
        return None;
    }
    let mut s = if int == "പൂജ്യം" && frac.is_some() {
        format!("{} {}", nonempty(frac?)?, nonempty(min?)?)
    } else if let (Some(frac), Some(min)) = (frac, min) {
        format!("{int} {maj} {} {}", nonempty(frac)?, nonempty(min)?)
    } else {
        format!("{int} {maj}")
    };
    if let Some(rate) = rate {
        s.push(' ');
        s.push_str(nonempty(rate)?)
    }
    Some(format!("{sign}{s}"))
}
fn telephone(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let cc = f.opt("country_code")?;
    let n = nonempty(f.take("number_part")?)?;
    if !f.done() {
        return None;
    }
    Some(format!("{}{}", cc.map(|x| format!("{x} ")).unwrap_or_default(), n.strip_suffix(' ').unwrap_or(n)))
}
fn electronic(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let protocol = f.opt("protocol")?;
    let user = f.opt("username")?;
    let domain = nonempty(f.take("domain")?)?;
    if !f.done() {
        return None;
    }
    let mut raw = String::new();
    if let Some(p) = protocol {
        raw.push_str(p);
        raw.push_str(" കോളൻ സ്ലാഷ് സ്ലാഷ് ")
    }
    if let Some(u) = user {
        raw.push_str(u);
        raw.push_str(" അറ്റ് ")
    }
    raw.push_str(domain);
    let mut out = String::new();
    let mut prev_digit = false;
    for c in raw.chars() {
        let digit = number::digit(c);
        if digit.is_some() && prev_digit {
            out.push(' ')
        }
        if let Some(n) = digit {
            out.push_str(&number::digitwise(&c.to_string())?);
            let _ = n;
        } else if let Some(w) = match c {
            '.' => Some("ഡോട്ട്"),
            '/' => Some("സ്ലാഷ്"),
            '_' => Some("അണ്ടർസ്കോർ"),
            '-' => Some("ഹൈഫൻ"),
            ':' => Some("കോളൻ"),
            '?' => Some("ചോദ്യചിഹ്നം"),
            '=' => Some("സമം"),
            '&' => Some("ആൻഡ്"),
            '#' => Some("ഹാഷ്"),
            '%' => Some("ശതമാനം"),
            '+' => Some("പ്ലസ്"),
            _ => None,
        } {
            out.push(' ');
            out.push_str(w);
            out.push(' ')
        } else {
            out.push(c)
        }
        prev_digit = digit.is_some()
    }
    Some(out.replace("  ", " "))
}
fn roman(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    f.preserve_order();
    let k = f.opt("key_cardinal")?;
    if let Some(k) = k {
        let i = f.take("integer")?;
        let word = &lexical::ROMANS.exact(i).next()?.value;
        if !f.done() {
            return None;
        }
        return Some(format!("{k} {word}"));
    }
    if let Some(o) = f.opt("default_ordinal")? {
        let k = f.opt("key_cardinal")?;
        if !f.done() {
            return None;
        }
        return Some(format!("{o}{}", k.map(|x| format!(" {x}")).unwrap_or_default()));
    }
    let i = f.take("integer")?;
    let word = &lexical::ROMANS.exact(i).next()?.value;
    let k = f.take("key_cardinal")?;
    if !f.done() {
        return None;
    }
    Some(format!("{word} {k}"))
}

pub fn one(token: &Dict) -> Option<String> {
    if let Some(s) = common::verbalize_name(token) {
        return Some(s);
    }
    if let [(k, Value::Str(s))] = token.as_slice() {
        if k == "name" && !s.is_empty() && !s.contains('"') {
            return Some(s.replace('\u{a0}', " "));
        }
    }
    let (kind, Value::Dict(d)) = common::class(token)? else { return None };
    let s = match kind {
        "cardinal" => cardinal(d),
        "decimal" => decimal(d),
        "fraction" => fraction(d),
        "date" => date(d),
        "time" => time(d),
        "measure" => measure(d),
        "money" => money(d),
        "telephone" => telephone(d),
        "electronic" => electronic(d),
        "roman" => roman(d),
        "ordinal" => {
            let mut f = Fields::new(d);
            let s = nonempty(f.take("integer")?)?;
            f.done().then(|| s.to_owned())
        }
        _ => None,
    }?;
    Some(s.replace('\u{a0}', " "))
}

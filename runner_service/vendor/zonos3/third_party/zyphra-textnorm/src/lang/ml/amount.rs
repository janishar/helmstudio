//! Money and measurement taggers.
use super::{
    number,
    numeric::{self, Reading},
};
crate::tsv!(UNITS, "ml/measure/units.tsv");
crate::tsv!(CURRENCIES, "ml/money/currency.tsv");
crate::tsv!(SINGULAR, "ml/money/currency_singular.tsv");
crate::labels!(MINORS, "ml/money/major_minor_currencies.tsv");

fn range(s: &str, max: usize, attr: bool) -> Option<(String, i64)> {
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            fn part(s: &str, max: usize, attr: bool) -> Option<(String, i64)> {
                let (n, f) = s.split_once('.').map_or((s, None), |(n, f)| (n, Some(f)));
                if n.chars().count() > max || n.is_empty() || !n.chars().all(|c| number::digit(c).is_some()) {
                    return None;
                }
                let (mut w, c) = if attr && f.is_none() { number::attributive(n)? } else { number::final_cardinal(n)? };
                if let Some(f) = f {
                    if f.chars().count() > 3 {
                        return None;
                    }
                    w.push_str(" ദശാംശം ");
                    w.push_str(&number::digitwise(f)?);
                }
                Some((w, c))
            }
            let (a, ac) = part(a, max, false)?;
            let (b, bc) = part(b, max, attr)?;
            return Some((format!("{a} മുതൽ {b} വരെ"), ac + bc));
        }
    }
    None
}

fn measure_number(s: &str) -> Vec<(String, i64)> {
    let (body, sign) = numeric::sign(s);
    let mut out = Vec::new();
    if let Some((w, c)) = number::attributive(body) {
        out.push((format!("cardinal {{ {sign}integer: \"{w}\" }}"), c));
    }
    if let Some(fields) = numeric::decimal_fields(body) {
        out.push((format!("decimal {{ {sign}{fields} }}"), 0));
    }
    if sign.is_empty() {
        if let Some((w, c)) = range(body, 5, true) {
            out.push((format!("cardinal {{ integer: \"{w}\" }}"), c));
        }
    }
    out
}

pub fn measure(s: &str, out: &mut Vec<Reading>) {
    for row in UNITS.rows() {
        if let Some(prefix) = s.strip_suffix(row.key.as_str()) {
            let raw = prefix.strip_suffix(' ').unwrap_or(prefix);
            if row.key == "s"
                && raw.chars().count() == 4
                && number::ascii_digits(raw)
                    .is_some_and(|x| (x.starts_with('1') || x.starts_with('2')) && x.ends_with('0'))
            {
                continue;
            }
            for (num, c) in measure_number(raw) {
                out.push((
                    format!("measure {{ {num} units: \"{}\" preserve_order: true }}", row.value),
                    1_080_000 + c + row.cost,
                    7,
                ));
                if row.key == "km/h" && !num.contains(" മുതൽ ") {
                    out.push((format!("measure {{ morphosyntactic_features: \"മണിക്കൂറിൽ\" {num} units: \"കിലോമീറ്റർ\" preserve_order: true }}"),1_070_000+c,6));
                }
            }
        }
    }
}

fn rate(s: &str) -> (&str, Option<&'static str>) {
    if let Some((body, unit)) = s.rsplit_once('/') {
        let unit = unit.strip_prefix(' ').unwrap_or(unit);
        let v = match unit {
            "h" | "hr" => "മണിക്കൂറിൽ",
            "s" | "sec" => "സെക്കൻഡിൽ",
            "min" => "മിനിറ്റിൽ",
            "d" | "day" => "ദിവസത്തിൽ",
            "wk" => "ആഴ്ചയിൽ",
            "mo" => "മാസത്തിൽ",
            "yr" => "വർഷത്തിൽ",
            "kg" => "കിലോഗ്രാമിൽ",
            "g" => "ഗ്രാമിൽ",
            "mg" => "മില്ലിഗ്രാമിൽ",
            "t" => "ടണ്ണിൽ",
            "l" | "L" => "ലിറ്ററിൽ",
            "m" => "മീറ്ററിൽ",
            "km" => "കിലോമീറ്ററിൽ",
            _ => return (s, None),
        };
        return (body.strip_suffix(' ').unwrap_or(body), Some(v));
    }
    (s, None)
}
fn shorthand(s: &str) -> Option<(String, i64)> {
    let (base, mag) = s.split_at(s.char_indices().last()?.0);
    let magword = match mag {
        "k" | "K" => "ആയിരം",
        "M" => "മില്യൺ",
        "B" => "ബില്യൺ",
        "T" => "ട്രിില്യൺ",
        _ => return None,
    };
    let base = base.strip_suffix(' ').unwrap_or(base);
    if let Some((n, f)) = base.split_once('.') {
        if n.is_empty()
            || n.chars().count() > 6
            || !n.chars().all(|c| number::digit(c).is_some())
            || f.chars().count() > 4
        {
            return None;
        }
        let (number, cost) = number::final_cardinal(n)?;
        return Some((format!("{number} ദശാംശം {} {magword}", number::digitwise(f)?), cost));
    }
    if base.is_empty() || base.chars().count() > 6 || !base.chars().all(|c| number::digit(c).is_some()) {
        return None;
    }
    let (n, c) = number::attributive(base)?;
    if magword == "ആയിരം" && (base == "1" || base == "൧") {
        Some((magword.to_owned(), c))
    } else {
        Some((format!("{n} {magword}"), c))
    }
}
fn minor(sym: &str) -> Option<&'static str> {
    MINORS
        .iter()
        .find(|r| r.first().is_some_and(|x| x == sym))
        .and_then(|r| r.get(2))
        .filter(|s| !s.is_empty())
        .map(String::as_str)
}
fn format_currency(
    prefix: bool,
    sign: &str,
    maj: &str,
    integer: &str,
    fraction: Option<(&str, &str)>,
    per: Option<&str>,
) -> String {
    let mut fields = if prefix {
        format!("{sign}currency_maj: \"{maj}\" integer_part: \"{integer}\"")
    } else {
        format!("{sign}integer_part: \"{integer}\"")
    };
    if let Some((f, _)) = fraction {
        fields.push_str(&format!(" fractional_part: \"{f}\""));
    }
    if !prefix {
        fields.push_str(&format!(" currency_maj: \"{maj}\""));
    }
    if let Some((_, m)) = fraction {
        if !m.is_empty() {
            fields.push_str(&format!(" currency_min: \"{m}\""));
        }
    }
    if let Some(p) = per {
        fields.push_str(&format!(" morphosyntactic_features: \"{p}\""));
    }
    format!("money {{ {fields} }}")
}

pub fn money(s: &str, out: &mut Vec<Reading>) {
    let (s, per) = rate(s);
    let (s, sign) = numeric::sign(s);
    for (table, singular) in [(&*CURRENCIES, false), (&*SINGULAR, true)] {
        for row in table.rows() {
            for prefix in [true, false] {
                let raw = if prefix {
                    s.strip_prefix(row.key.as_str()).map(|x| x.strip_prefix(' ').unwrap_or(x))
                } else {
                    s.strip_suffix(row.key.as_str()).map(|x| x.strip_suffix(' ').unwrap_or(x))
                };
                let Some(raw) = raw else { continue };
                let one = |x: &str| x == "1" || x == "൧";
                let singular_cost = if singular { -1_000 } else { 0 };
                if let Some((w, c)) = shorthand(raw) {
                    if !singular {
                        out.push((format_currency(prefix, sign, &row.value, &w, None, per), 1_078_000 + c, 8));
                    }
                }
                if sign.is_empty() && !singular {
                    for sep in [" - ", " – ", "-", "–"] {
                        if let Some((a, b)) = raw.split_once(sep) {
                            let mut right = vec![b];
                            if prefix {
                                for alias in CURRENCIES.rows().iter().filter(|r| r.value == row.value) {
                                    if let Some(tail) = b.strip_prefix(alias.key.as_str()) {
                                        right.push(tail.strip_prefix(' ').unwrap_or(tail));
                                    }
                                }
                            }
                            let amount = |v: &str| {
                                shorthand(v).or_else(|| {
                                    if let Some((n, f)) = v.split_once('.') {
                                        let (n, c) = number::final_cardinal(n)?;
                                        Some((format!("{n} ദശാംശം {}", number::digitwise(f)?), c))
                                    } else {
                                        number::final_cardinal(v)
                                    }
                                })
                            };
                            for b in right {
                                if let (Some((a, ac)), Some((b, bc))) = (amount(a), amount(b)) {
                                    let w = format!("{a} മുതൽ {b} വരെ");
                                    out.push((
                                        format_currency(prefix, sign, &row.value, &w, None, per),
                                        1_078_000 + ac + bc,
                                        8,
                                    ));
                                }
                            }
                        }
                    }
                }
                let (int, frac) = raw.split_once('.').map_or((raw, None), |(a, b)| (a, Some(b)));
                if singular && !one(int) {
                    continue;
                }
                let Some((word, cost)) = number::attributive(int) else { continue };
                if frac.is_none()
                    || frac.is_some_and(|f| !f.is_empty() && f.chars().all(|c| number::digit(c) == Some(0)))
                {
                    out.push((
                        format_currency(prefix, sign, &row.value, &word, None, per),
                        1_080_000 + cost + singular_cost,
                        8,
                    ));
                }
                if let Some(f) = frac {
                    let Some(ascii) = number::ascii_digits(f) else { continue };
                    let stripped = ascii.trim_end_matches('0');
                    if let Some(m) = minor(&row.key) {
                        let canon = match stripped.len() {
                            1 => format!("{}0", stripped),
                            2 => stripped.to_owned(),
                            _ => String::new(),
                        };
                        if !canon.is_empty() && !canon.starts_with("00") {
                            let canon = canon.trim_start_matches('0');
                            if let Some((fw, fc)) = number::attributive(canon) {
                                out.push((
                                    format_currency(prefix, sign, &row.value, &word, Some((&fw, m)), per),
                                    1_080_000 + cost + fc + singular_cost,
                                    8,
                                ));
                            }
                        }
                    }
                    if !singular && ascii.len() >= 3 && ascii[2..].chars().any(|x| x != '0') {
                        if let (Some((integer, ic)), Some(fw)) = (number::final_cardinal(int), number::digitwise(f)) {
                            out.push((
                                format_currency(prefix, sign, &row.value, &integer, Some((&fw, "")), per),
                                1_079_500 + ic,
                                8,
                            ));
                        }
                    }
                }
            }
        }
    }
}

//! Punjabi class verbalizers, consuming fields in source order.
use super::graph_utils::{digitwise, lookup};
use crate::lang::common::{Fields, class, verbalize_name};
use crate::tag::{Dict, Value};
const ROMAN: &str = include_str!("../../../data/pa/roman/roman_to_spoken.tsv");
const SYMBOLS: &str = include_str!("../../../data/pa/electronic/symbols.tsv");
const SUB: &str = include_str!("../../../data/pa/electronic/subscript_digit.tsv");
const PROTOCOLS: &str = include_str!("../../../data/pa/electronic/protocols.tsv");
const LETTERS: &str = include_str!("../../../data/pa/address/letters.tsv");
const QUANTITIES: &str = include_str!("../../../data/pa/numbers/quantities.tsv");
const MINORS: &str = include_str!("../../../data/pa/money/major_minor_currencies.tsv");

fn sign(fields: &mut Fields<'_, '_>) -> Option<&'static str> {
    Some(match fields.opt("negative")? {
        None => "",
        Some("true") => "ਰਿਣ ",
        Some("approx") => "ਲਗਭਗ ",
        Some("pm") => "ਜੋੜ ਜਾਂ ਘਟਾਓ ",
        _ => return None,
    })
}
fn number(kind: &str, fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let sign = sign(&mut f)?;
    let int = f.take(if kind == "cardinal" { "integer" } else { "integer_part" })?;
    let mut out = format!("{sign}{int}");
    if kind == "decimal" {
        if int.is_empty() {
            return None;
        }
        let fractional = f.opt("fractional_part")?;
        let quantity = f.opt("quantity")?;
        if fractional.is_none() && quantity.is_none() {
            return None;
        }
        if let Some(frac) = fractional {
            if frac.is_empty() {
                return None;
            }
            out.push_str(" ਦਸ਼ਮਲਵ ");
            out.push_str(frac);
        }
        if let Some(q) = quantity {
            if !QUANTITIES.lines().any(|row| row.split_once('\t').map_or(false, |(_, value)| value == q)) {
                return None;
            }
            out.push(' ');
            out.push_str(q);
        }
    }
    f.done().then_some(out)
}
fn content(s: &str, chemical: bool) -> Option<String> {
    let mut words = Vec::new();
    let mut run = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() && !chemical {
            run.push(c);
            continue;
        }
        if !run.is_empty() {
            words.push(std::mem::take(&mut run));
        }
        let key = c.to_string();
        let word = if chemical && c.is_ascii_alphabetic() {
            lookup(LETTERS, &c.to_ascii_lowercase().to_string()).map(str::to_owned)
        } else {
            let symbol_key = match c {
                '[' => "\\[",
                ']' => "\\]",
                _ => key.as_str(),
            };
            digitwise(&key)
                .or_else(|| lookup(SUB, &key).map(str::to_owned))
                .or_else(|| lookup(SYMBOLS, symbol_key).map(str::to_owned))
        }?;
        words.push(word);
    }
    if !run.is_empty() {
        words.push(run);
    }
    (!words.is_empty()).then(|| words.join(" "))
}
pub(crate) fn verbalize(token: &Dict) -> Option<String> {
    if let Some(name) = verbalize_name(token) {
        return Some(name);
    }
    let (kind, value) = class(token)?;
    let Value::Dict(fields) = value else { return None };
    if kind == "cardinal" || kind == "decimal" {
        return number(kind, fields);
    }
    let mut f = Fields::new(fields);
    let out = match kind {
        "ordinal" => {
            let value = f.take("integer")?;
            if value.is_empty() {
                return None;
            }
            value.to_owned()
        }
        "fraction" => {
            let sign = match f.opt("negative")? {
                None => "",
                Some("true") => "ਰਿਣ ",
                _ => return None,
            };
            if let Some(m) = f.opt("morphosyntactic_features")? {
                if m.is_empty() {
                    return None;
                }
                format!("{sign}{m}")
            } else {
                let i = f.opt("integer_part")?;
                let n = f.take("numerator")?;
                let d = f.take("denominator")?;
                match i {
                    Some(i) => format!("{sign}{i} ਅਤੇ {n} ਬਟਾ {d}"),
                    None => format!("{sign}{n} ਬਟਾ {d}"),
                }
            }
        }
        "date" => {
            f.preserve_order();
            if f.peek_key() == Some("field_order") {
                if f.take("field_order")?.chars().count() != 1 {
                    return None;
                }
            }
            if f.peek_key() == Some("era") {
                f.take("era")?.to_owned()
            } else {
                let mut day = f.opt("day")?;
                let month = f.opt("month")?;
                if day.is_none() && month.is_some() {
                    day = f.opt("day")?;
                    if day.is_some()
                        && ![
                            "ਜਨਵਰੀ",
                            "ਫ਼ਰਵਰੀ",
                            "ਮਾਰਚ",
                            "ਅਪ੍ਰੈਲ",
                            "ਮਈ",
                            "ਜੂਨ",
                            "ਜੁਲਾਈ",
                            "ਅਗਸਤ",
                            "ਸਤੰਬਰ",
                            "ਅਕਤੂਬਰ",
                            "ਨਵੰਬਰ",
                            "ਦਸੰਬਰ",
                        ]
                        .contains(&month?)
                    {
                        return None;
                    }
                }
                let year = f.opt("year")?;
                let era = f.opt("era")?;
                if day.is_some() && month.is_none() || day.is_none() && month.is_none() && year.is_none() {
                    return None;
                }
                [day, month, year, era].into_iter().flatten().collect::<Vec<_>>().join(" ")
            }
        }
        "time" => {
            if let Some(range) = f.opt("morphosyntactic_features")? {
                let mut out = range.to_owned();
                if let Some(z) = f.opt("zone")? {
                    out.push(' ');
                    out.push_str(z);
                }
                out
            } else {
                let period = f.opt("suffix")?;
                let hour = f.take("hours")?;
                let minute = f.opt("minutes")?;
                let second = f.opt("seconds")?;
                let zone = f.opt("zone")?;
                let mut out = String::new();
                if let Some(p) = period {
                    out.push_str(p);
                    out.push(' ');
                }
                out.push_str(hour);
                if minute.is_none() && second.is_none() {
                    out.push_str(" ਵਜੇ");
                } else {
                    out.push_str(" ਵੱਜ ਕੇ");
                    if let Some(m) = minute {
                        out.push_str(&format!(" {m} ਮਿੰਟ"));
                    }
                    if let Some(s) = second {
                        out.push_str(&format!(" {s} ਸਕਿੰਟ"));
                    }
                }
                if let Some(z) = zone {
                    out.push(' ');
                    out.push_str(z);
                }
                out
            }
        }
        "measure" => {
            let (kind, d) =
                if let Some(d) = f.dict("cardinal") { ("cardinal", d) } else { ("decimal", f.dict("decimal")?) };
            let n = number(kind, d)?;
            let units = f.take("units")?;
            format!("{n} {units}")
        }
        "money" => {
            let sign = sign(&mut f)?;
            let major_first = f.peek_key() == Some("currency_maj");
            let (major, integer, fraction, minor) = if major_first {
                let major = f.take("currency_maj")?;
                let i = f.take("integer_part")?;
                let frac = f.opt("fractional_part")?;
                let minor = f.opt("currency_min")?;
                (major, i, frac, minor)
            } else {
                let i = f.take("integer_part")?;
                let frac = f.opt("fractional_part")?;
                let major = f.take("currency_maj")?;
                let minor = f.opt("currency_min")?;
                (major, i, frac, minor)
            };
            if major_first != fraction.is_some() {
                return None;
            }
            if fraction.is_some() && lookup(MINORS, major).is_none() {
                return None;
            }
            if major.is_empty() || integer.is_empty() || fraction == Some("") || minor == Some("") {
                return None;
            }
            let mut out = match (fraction, minor) {
                (None, None) => format!("{sign}{integer} {major}"),
                (Some(frac), Some(minor)) if integer == "ਸਿਫ਼ਰ" => format!("{sign}{frac} {minor}"),
                (Some(frac), Some(minor)) => format!("{sign}{integer} {major} {frac} {minor}"),
                (Some(frac), None) => format!("{sign}{integer} ਦਸ਼ਮਲਵ {frac} {major}"),
                _ => return None,
            };
            if let Some(per) = f.opt("morphosyntactic_features")? {
                out.push(' ');
                out.push_str(per);
            }
            out
        }
        "telephone" => {
            let country = f.opt("country_code")?;
            let n = f.take("number_part")?.trim_end_matches(' ');
            let ext = f.opt("extension")?;
            [country, Some(n), ext].into_iter().flatten().collect::<Vec<_>>().join(" ")
        }
        "roman" => {
            f.preserve_order();
            let mut out = String::new();
            if let Some(key) = f.opt("key_cardinal")? {
                out.push_str(key);
                out.push(' ');
            }
            let integer = f.take("integer")?;
            if let Some(ord) = f.opt("default_ordinal")? {
                out.push_str(ord);
            } else {
                out.push_str(lookup(ROMAN, integer)?);
            }
            if let Some(key) = f.opt("key_cardinal")? {
                out.push(' ');
                out.push_str(key);
            }
            f.preserve_order();
            out
        }
        "electronic" => {
            if let Some(u) = f.opt("username")? {
                let d = f.take("domain")?;
                format!("{} ਐਟ {}", content(u, false)?, content(d, false)?)
            } else if let Some(p) = f.opt("protocol")? {
                let d = f.take("domain")?;
                format!("{} {}", lookup(PROTOCOLS, p)?, content(d, false)?)
            } else if let Some(p) = f.opt("path")? {
                content(p, false)?
            } else if let Some(d) = f.opt("domain")? {
                content(d, false)?
            } else {
                content(f.take("fragment_id")?, true)?
            }
        }
        _ => return None,
    };
    f.done().then(|| if out.contains('\u{a0}') { out.replace('\u{a0}', " ") } else { out })
}

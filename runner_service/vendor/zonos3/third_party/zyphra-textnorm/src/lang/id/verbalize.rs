use super::number::digits;
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn sign(f: &mut Fields<'_, '_>) -> Option<String> {
    let x = f.opt("negative")?;
    Some(match x {
        Some("true") => "negatif ".into(),
        Some("sekitar") => "sekitar ".into(),
        Some("kurang lebih") => "kurang lebih ".into(),
        Some(v) => format!("{v} "),
        None => String::new(),
    })
}

fn add(s: &mut String, x: Option<&str>, before: &str) {
    if let Some(x) = x {
        s.push_str(before);
        s.push_str(x);
    }
}

fn numerical(kind: &str, d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut result = if kind == "ordinal" { String::new() } else { sign(&mut f)? };
    match kind {
        "cardinal" => result.push_str(f.take("integer")?),
        "ordinal" => {
            let v = f.take("integer")?;
            if v == "satu" {
                result.push_str("pertama");
            } else {
                result.push_str("ke");
                result.push_str(v);
            }
        }
        "decimal" => {
            result.push_str(f.take("integer_part")?);
            if let Some(v) = f.opt("fractional_part")? {
                add(&mut result, Some(v), " koma ");
            } else {
                add(&mut result, f.opt("point_part")?, " titik ");
            }
            add(&mut result, f.opt("quantity")?, " ");
            f.preserve_order();
        }
        "fraction" => {
            add(&mut result, f.opt("integer_part")?, "");
            if !result.is_empty() && !result.ends_with(' ') {
                result.push(' ');
            }
            let n = f.take("numerator")?;
            let d = f.take("denominator")?;
            let x = match (n, d) {
                ("satu", "dua") => "setengah".into(),
                ("satu", "tiga") => "sepertiga".into(),
                ("satu", "empat") => "seperempat".into(),
                ("satu", "sepuluh") => "sepersepuluh".into(),
                ("satu", "seratus") => "seperseratus".into(),
                ("satu", "seribu") => "seperseribu".into(),
                (_, "tiga") => format!("{n} pertiga"),
                (_, "empat") => format!("{n} perempat"),
                _ => format!("{n} per {d}"),
            };
            result.push_str(&x);
            f.preserve_order();
        }
        _ => return None,
    }
    if f.done() { Some(result) } else { None }
}

fn lookup_tsv(tsv: &'static str, key: &str) -> Option<&'static str> {
    for line in tsv.lines() {
        if let Some((k, v)) = line.split_once('\t') {
            if k == key {
                return Some(v.trim_end_matches('\r'));
            }
        }
    }
    None
}

fn read_electronic(s: &str, domains: bool, spell: bool) -> String {
    let symbols = include_str!("../../../data/id/electronic/symbols.tsv");
    let domain = include_str!("../../../data/id/electronic/domain.tsv");
    let mut pieces = Vec::new();
    let mut i = 0;
    let mut last_alpha = false;
    while i < s.len() {
        if domains {
            let tail = &s[i..];
            let mut matched_domain = None;
            for line in domain.lines() {
                if let Some((k, v)) = line.split_once('\t') {
                    if tail.starts_with(k) {
                        let rest = &tail[k.len()..];
                        if rest.is_empty() || rest.starts_with(['.', '/', ':']) {
                            matched_domain = Some((k.len(), v.trim_end_matches('\r')));
                            break;
                        }
                    }
                }
            }
            if let Some((len, expansion)) = matched_domain {
                pieces.push(expansion.to_owned());
                i += len;
                last_alpha = false;
                continue;
            }
        }
        let ch = s[i..].chars().next().unwrap();
        let ch_str = &s[i..i + ch.len_utf8()];
        i += ch.len_utf8();
        if let Some(exp) = lookup_tsv(symbols, ch_str) {
            pieces.push(exp.to_owned());
            last_alpha = false;
        } else if ch.is_ascii_digit() {
            if let Some(d) = digits(ch_str) {
                pieces.push(d);
            }
            last_alpha = false;
        } else if spell {
            pieces.push(ch.to_string());
            last_alpha = false;
        } else if ch.is_alphabetic() {
            if last_alpha {
                if let Some(last) = pieces.last_mut() {
                    last.push(ch);
                }
            } else {
                pieces.push(ch.to_string());
                last_alpha = true;
            }
        } else {
            pieces.push(ch.to_string());
            last_alpha = false;
        }
    }
    pieces.join(" ")
}

fn nonempty(d: &Dict) -> bool {
    d.iter().all(|(_, v)| match v {
        Value::Str(s) => !s.is_empty(),
        Value::Dict(sub) => nonempty(sub),
        _ => true,
    })
}

pub(crate) fn verbalize(token: &Dict) -> Option<String> {
    if !nonempty(token) {
        return None;
    }
    if let Some(v) = common::verbalize_name(token) {
        return Some(v);
    }
    let (kind, value) = common::class(token)?;
    let Value::Dict(d) = value else { return None };
    if ["cardinal", "decimal", "fraction", "ordinal"].contains(&kind) {
        return numerical(kind, d);
    }
    let mut f = Fields::new(d);
    let result = match kind {
        "measure" => {
            let k = match f.peek_key()? {
                "cardinal" => "cardinal",
                "decimal" => "decimal",
                "fraction" => "fraction",
                _ => return None,
            };
            let v = numerical(k, f.dict(k)?)?;
            let units = f.take("units")?;
            f.preserve_order();
            format!("{v} {units}")
        }
        "date" => {
            if let Some(day) = f.opt("day")? {
                let m = f.take("month")?;
                let y = f.opt("year")?;
                format!("{day} {m}{}", y.map(|v| format!(" {v}")).unwrap_or_default())
            } else if let Some(m) = f.opt("month")? {
                format!("{m} {}", f.take("year")?)
            } else {
                f.take("year")?.to_owned()
            }
        }
        "time" => {
            let pre = f.opt("prefix")?.unwrap_or("pukul");
            let h = f.take("hours")?;
            let mut x = format!("{pre} {h}");
            if let Some(m) = f.opt("minutes")? {
                x.push_str(" lewat ");
                x.push_str(m);
                x.push_str(" menit");
            }
            if let Some(s) = f.opt("seconds")? {
                x.push(' ');
                x.push_str(s);
                x.push_str(" detik");
            }
            add(&mut x, f.opt("zone")?, " ");
            f.preserve_order();
            x
        }
        "telephone" => {
            let mut x = String::new();
            add(&mut x, f.opt("country_code")?, "");
            if !x.is_empty() {
                x.push(' ');
            }
            x.push_str(f.take("number_part")?);
            f.preserve_order();
            x
        }
        "electronic" => {
            let p = f.opt("protocol")?;
            let u = f.opt("username")?;
            if p.is_some() && u.is_some() {
                return None;
            }
            let d = f.take("domain")?;
            let mut x = String::new();
            if let Some(p) = p {
                x.push_str(&read_electronic(p, false, true));
                x.push(' ');
            }
            if let Some(u) = u {
                x.push_str(&read_electronic(u, false, false));
                x.push_str(" et ");
            }
            x.push_str(&read_electronic(d, true, false));
            f.preserve_order();
            x
        }
        "money" => {
            let negative = f.opt("negative")?;
            let mut x = negative.map(|v| format!("{v} ")).unwrap_or_default();
            if let Some(n) = f.opt("integer_part")? {
                x.push_str(n);
                if let Some(maj) = f.opt("currency_maj")? {
                    x.push(' ');
                    x.push_str(maj);
                    if let Some(v) = f.opt("fractional_part")? {
                        x.push(' ');
                        x.push_str(v);
                        x.push(' ');
                        x.push_str(f.take("currency_min")?);
                    }
                } else {
                    add(&mut x, f.opt("fractional_part")?, " koma ");
                    add(&mut x, f.opt("quantity")?, " ");
                    x.push(' ');
                    x.push_str(f.take("currency_maj")?);
                }
            } else {
                x.push_str(f.take("fractional_part")?);
                x.push(' ');
                x.push_str(f.take("currency_min")?);
            }
            add(&mut x, f.opt("morphosyntactic_features")?, " ");
            x
        }
        _ => return None,
    };
    if !f.done() {
        None
    } else if result.contains('\u{a0}') {
        Some(result.replace('\u{a0}', " "))
    } else {
        Some(result)
    }
}

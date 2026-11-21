use super::{lookup, number::digits};
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};
fn sign(f: &mut Fields<'_, '_>) -> Option<String> {
    let x = f.opt("negative")?;
    Some(match x {
        None => String::new(),
        Some("true") => "negatif ".into(),
        Some("sekitar" | "lebih kurang" | "kurang lebih" | "kira-kira") => format!("{} ", x?),
        _ => return None,
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
            result.push_str(if v == "satu" { "pertama" } else { "ke" });
            if v != "satu" {
                result.push_str(v);
            }
        }
        "decimal" => {
            result.push_str(f.take("integer_part")?);
            if let Some(v) = f.opt("fractional_part")? {
                add(&mut result, Some(v), " perpuluhan ");
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
                ("satu", "lapan") => "seperlapan".into(),
                ("satu", "sepuluh") => "sepersepuluh".into(),
                ("satu", "seratus") => "seperseratus".into(),
                ("satu", "seribu") => "seperseribu".into(),
                (_, "tiga") => format!("{n} pertiga"),
                (_, "empat") => format!("{n} perempat"),
                (_, "lapan") => format!("{n} perlapan"),
                _ => format!("{n} per {d}"),
            };
            result.push_str(&x);
            f.preserve_order();
        }
        _ => return None,
    }
    if f.done() { Some(result) } else { None }
}
fn read_electronic(s: &str, domains: bool, spell: bool) -> String {
    let symbols = include_str!("../../../data/ms/electronic/symbols.tsv");
    let domain = include_str!("../../../data/ms/electronic/domain.tsv");
    let mut pieces = Vec::new();
    let mut i = 0;
    let mut last_alpha = false;
    while i < s.len() {
        let r = &s[i..];
        if domains {
            let mut matches: Vec<_> = domain
                .lines()
                .filter_map(|l| l.split_once('\t'))
                .filter(|(k, _)| {
                    r.starts_with(*k)
                        && r[k.len()..].chars().next().map(|c| matches!(c, '.' | '/' | ':')).unwrap_or(true)
                })
                .collect();
            matches.sort_by_key(|(k, _)| std::cmp::Reverse(k.len()));
            if let Some((k, v)) = matches.first() {
                pieces.push((*v).to_owned());
                i += k.len();
                last_alpha = false;
                continue;
            }
        }
        let c = r.chars().next().unwrap();
        let len = c.len_utf8();
        let word = if c.is_ascii_digit() {
            digits(&c.to_string()).unwrap()
        } else if let Some(v) = lookup(symbols, &s[i..i + len]) {
            v.to_owned()
        } else {
            c.to_string()
        };
        if !spell && c.is_ascii_alphabetic() && last_alpha {
            pieces.last_mut().unwrap().push(c);
        } else {
            pieces.push(word);
        }
        last_alpha = !spell && c.is_ascii_alphabetic();
        i += len;
    }
    pieces.join(" ")
}
fn nonempty(d: &Dict) -> bool {
    d.iter().all(|(_, v)| match v {
        Value::Str(s) => !s.is_empty(),
        Value::Dict(x) => nonempty(x),
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
                x.push_str(&format!(" {m} minit"));
            }
            if let Some(s) = f.opt("seconds")? {
                x.push_str(&format!(" {s} saat"));
            }
            add(&mut x, f.opt("zone")?, " ");
            f.preserve_order();
            x
        }
        "telephone" => {
            let mut x = String::new();
            add(&mut x, f.opt("country_code")?, "");
            if !x.is_empty() {
                x.push(' ')
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
                    x.push_str(&format!(" {maj}"));
                    if let Some(v) = f.opt("fractional_part")? {
                        x.push_str(&format!(" {v} {}", f.take("currency_min")?));
                    }
                } else {
                    add(&mut x, f.opt("fractional_part")?, " perpuluhan ");
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

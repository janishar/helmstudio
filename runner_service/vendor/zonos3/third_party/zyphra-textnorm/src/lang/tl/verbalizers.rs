//! Tagalog token verbalization.
use super::graph_utils::link_in_place;
use crate::lang::common::{self, Fields};
use crate::tag::{Dict, Value};

fn sign(f: &mut Fields<'_, '_>) -> Option<&'static str> {
    Some(match f.opt("negative")? {
        None => "",
        Some("true") => "minus ",
        Some("humigit-kumulang") => "humigit-kumulang ",
        Some("plus-minus") => "plus-minus ",
        _ => return None,
    })
}
fn linked(out: &mut String, value: &str) -> Option<()> {
    if value.is_empty() {
        return None;
    }
    out.push_str(value);
    link_in_place(out)
}
fn finish(f: &mut Fields<'_, '_>, s: String) -> Option<String> {
    f.preserve_order();
    if !f.done() {
        return None;
    }
    Some(if s.contains('\u{a0}') { s.replace('\u{a0}', " ") } else { s })
}
fn special_fraction(n: &str, d: &str) -> Option<&'static str> {
    Some(match (n, d) {
        ("isa", "dalawa") => "kalahati",
        ("isa", "tatlo") => "isang-katlo",
        ("dalawa", "tatlo") => "dalawang-katlo",
        ("isa", "apat") => "isang-kapat",
        ("tatlo", "apat") => "tatlong-kapat",
        ("isa", "lima") => "isang-kalima",
        ("dalawa", "lima") => "dalawang-kalima",
        ("tatlo", "lima") => "tatlong-kalima",
        ("apat", "lima") => "apat na kalima",
        ("isa", "anim") => "isang-kaanim",
        ("lima", "anim") => "limang-kaanim",
        ("isa", "pito") => "isang-kapito",
        ("isa", "walo") => "isang-kawalo",
        ("tatlo", "walo") => "tatlong-kawalo",
        ("lima", "walo") => "limang-kawalo",
        ("pito", "walo") => "pitong-kawalo",
        ("isa", "siyam") => "isang-kasiyam",
        ("isa", "sampu") => "isang-kasampu",
        _ => return None,
    })
}

fn electronic_read(value: &str, domain: bool, spell: bool) -> String {
    let symbols = include_str!("../../../data/tl/electronic/symbols.tsv");
    let domains = include_str!("../../../data/tl/electronic/domain.tsv");
    let mut out = String::new();
    let mut rest = value;
    while !rest.is_empty() {
        let mut matched = None;
        if domain {
            for row in domains.lines() {
                if let Some((key, val)) = row.split_once('\t') {
                    if rest.starts_with(key)
                        && rest[key.len()..].chars().next().map_or(true, |c| matches!(c, '.' | '/' | ':'))
                        && matched.map_or(true, |(k, _): (&str, &str)| key.len() > k.len())
                    {
                        matched = Some((key, val));
                    }
                }
            }
        }
        if let Some((key, val)) = matched {
            out.push(' ');
            out.push_str(val);
            out.push(' ');
            rest = &rest[key.len()..];
            continue;
        }
        let c = rest.chars().next().unwrap();
        rest = &rest[c.len_utf8()..];
        let one = c.to_string();
        let word = symbols.lines().filter_map(|l| l.split_once('\t')).find_map(|(k, v)| (k == one).then_some(v));
        if let Some(w) = word {
            out.push(' ');
            out.push_str(w);
            out.push(' ');
        } else if c.is_ascii_digit() {
            out.push(' ');
            out.push_str(&super::graph_utils::digit_sequence(&one).unwrap());
            out.push(' ');
        } else {
            out.push(c);
            if spell {
                out.push(' ');
            }
        }
    }
    out.split_whitespace().collect::<Vec<_>>().join(" ")
}
fn body(kind: &str, d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let mut s = String::new();
    match kind {
        "cardinal" => {
            s.push_str(sign(&mut f)?);
            s.push_str(f.take("integer")?);
        }
        "ordinal" => {
            let n = f.take("integer")?;
            s = match n {
                "isa" => "una".into(),
                "dalawa" => "ikalawa".into(),
                "tatlo" => "ikatlo".into(),
                _ => format!("ika{n}"),
            };
        }
        "decimal" => {
            s.push_str(sign(&mut f)?);
            let n = f.take("integer_part")?;
            let frac = f.opt("fractional_part")?;
            let qty = f.opt("quantity")?;
            if frac.is_none() && qty.is_some() {
                linked(&mut s, n)?;
            } else {
                s.push_str(n);
            }
            if let Some(v) = frac {
                s.push_str(" tuldok ");
                if qty.is_some() {
                    linked(&mut s, v)?;
                } else {
                    s.push_str(v);
                }
            }
            if let Some(v) = qty {
                s.push(' ');
                s.push_str(v);
            }
        }
        "fraction" => {
            s.push_str(sign(&mut f)?);
            if let Some(v) = f.opt("integer_part")? {
                s.push_str(v);
                s.push_str(" at ");
            }
            let n = f.take("numerator")?;
            let d = f.take("denominator")?;
            if let Some(v) = special_fraction(n, d) {
                s.push_str(v);
            } else {
                s.push_str(n);
                s.push_str(" sa ");
                s.push_str(d);
            }
        }
        "date" => {
            if let Some(day) = f.opt("day")? {
                s.push_str(day);
                s.push_str(" ng ");
                s.push_str(f.take("month")?);
                if let Some(y) = f.opt("year")? {
                    s.push(' ');
                    s.push_str(y);
                }
            } else if let Some(month) = f.opt("month")? {
                s.push_str(month);
                s.push(' ');
                s.push_str(f.take("year")?);
            } else {
                s.push_str(f.take("year")?);
            }
        }
        "time" => {
            if let Some(range) = f.opt("range")? {
                s.push_str(range);
            } else {
                s.push_str("ika-");
                s.push_str(f.take("hours")?);
                for (key, unit) in [("minutes", "minuto"), ("seconds", "segundo")] {
                    if let Some(v) = f.opt(key)? {
                        s.push_str(" at ");
                        linked(&mut s, v)?;
                        s.push(' ');
                        s.push_str(unit);
                    }
                }
                for key in ["suffix", "zone"] {
                    if let Some(v) = f.opt(key)? {
                        s.push(' ');
                        s.push_str(v);
                    }
                }
            }
        }
        "telephone" => {
            if let Some(v) = f.opt("country_code")? {
                s.push_str(v);
                s.push(' ');
            }
            s.push_str(f.take("number_part")?);
        }
        "electronic" => {
            if let Some(u) = f.opt("username")? {
                s.push_str(&electronic_read(u, false, false));
                s.push_str(" at ");
                s.push_str(&electronic_read(f.take("domain")?, true, false));
            } else {
                if let Some(p) = f.opt("protocol")? {
                    s.push_str(&electronic_read(p, false, true));
                    s.push(' ');
                }
                s.push_str(&electronic_read(f.take("domain")?, true, false));
            }
        }
        "measure" => {
            let key = f.peek_key()?;
            if !matches!(key, "cardinal" | "decimal" | "fraction") {
                return None;
            }
            let inner = f.dict(key)?;
            s = body(key, inner)?;
            link_in_place(&mut s)?;
            s.push(' ');
            s.push_str(f.take("units")?);
        }
        "money" => {
            if let Some(v) = f.opt("negative")? {
                s.push_str(v);
                s.push(' ');
            }
            if let Some(n) = f.opt("integer_part")? {
                let frac = f.opt("fractional_part")?;
                let qty = f.opt("quantity")?;
                if frac.is_some() && qty.is_some() {
                    s.push_str(n);
                    s.push_str(" tuldok ");
                    linked(&mut s, frac?)?;
                } else {
                    linked(&mut s, n)?;
                }
                if let Some(q) = qty {
                    s.push(' ');
                    linked(&mut s, q)?;
                }
                let maj = f.opt("currency_maj")?;
                let frac = if frac.is_none() { f.opt("fractional_part")? } else { frac };
                let min = f.opt("currency_min")?;
                let maj = maj.or(f.opt("currency_maj")?);
                if let Some(m) = maj {
                    s.push(' ');
                    s.push_str(m);
                }
                if let Some(v) = frac {
                    if qty.is_none() {
                        s.push_str(" at ");
                        linked(&mut s, v)?;
                        s.push(' ');
                        s.push_str(min?);
                    }
                }
            } else {
                let n = f.take("fractional_part")?;
                linked(&mut s, n)?;
                s.push(' ');
                s.push_str(f.take("currency_min")?);
            }
            if let Some(v) = f.opt("morphosyntactic_features")? {
                s.push(' ');
                s.push_str(v);
            }
        }
        _ => return None,
    }
    finish(&mut f, s)
}
pub fn verbalize(token: &Dict) -> Option<String> {
    if let Some(v) = common::verbalize_name(token) {
        return Some(v);
    }
    match common::class(token)? {
        (k, Value::Dict(d)) => body(k, d),
        _ => None,
    }
}

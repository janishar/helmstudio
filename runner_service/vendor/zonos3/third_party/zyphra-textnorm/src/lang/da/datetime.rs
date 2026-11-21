// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::graph_utils::write_small_number;
use super::numeric::{number, ordinal};
const MONTHS: [(&str, &[&str]); 12] = [
    ("januar", &["januar", "jan.", "jan"]),
    ("februar", &["februar", "feb.", "feb"]),
    ("marts", &["marts", "mar.", "mar"]),
    ("april", &["april", "apr.", "apr"]),
    ("maj", &["maj"]),
    ("juni", &["juni", "jun.", "jun"]),
    ("juli", &["juli", "jul.", "jul"]),
    ("august", &["august", "aug.", "aug"]),
    ("september", &["september", "sep.", "sep", "sept.", "sept"]),
    ("oktober", &["oktober", "okt.", "okt"]),
    ("november", &["november", "nov.", "nov"]),
    ("december", &["december", "dec.", "dec"]),
];
fn day(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n = s.parse::<u32>().ok()?;
    if !(1..=31).contains(&n) || s.len() > 2 {
        return None;
    }
    ordinal(&n.to_string())
}
fn month(s: &str) -> Option<usize> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n = s.parse::<usize>().ok()?;
    (1..=12).contains(&n).then_some(n)
}
fn year(s: &str) -> Option<String> {
    if s == "1900" {
        return Some("nitten hundrede".into());
    }
    if s == "2000" {
        return Some("to tusind".into());
    }
    if let Some(rest) = s.strip_prefix("19") {
        if rest.len() <= 3 && !rest.starts_with('0') {
            if let Some(n) = number(rest, false) {
                if rest != "0" {
                    return Some(format!("nitten hundrede og {n}"));
                }
            }
        }
    }
    if s.len() == 4 && s.starts_with("200") {
        return Some(format!("to tusind og {}", number(&s[3..], false)?));
    }
    if let Some(rest) = s.strip_prefix("20") {
        if rest.len() <= 3 && !rest.starts_with('0') {
            if let Some(n) = number(rest, false) {
                if rest != "0" {
                    return Some(format!("to tusind og {n}"));
                }
            }
        }
    }
    number(s, false)
}
fn years(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    // The optimized source year union prefers the ordinary cardinal path for 19xx.
    if s.len() == 4 && s.starts_with("19") && s != "1900" {
        if let Some(raw) = number(s, false) {
            out.push(raw);
        }
    }
    if let Some(y) = year(s) {
        if !out.contains(&y) {
            out.push(y);
        }
    }
    if s == "1000" {
        out.push("et tusind".into());
    }
    if s == "2000" {
        out.push("to tusind og nul".into());
    }
    if let Some(raw) = number(s, false) {
        if !out.contains(&raw) {
            out.push(raw);
        }
    }
    out
}
pub fn date(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (era, spoken) in [
        ("f.Kr.", "før Kristus"),
        ("f. Kr.", "før Kristus"),
        ("f.v.t.", "før Kristus"),
        ("BC", "før Kristus"),
        ("B.C.", "før Kristus"),
        ("BCE", "før Kristus"),
        ("e.Kr.", "efter Kristus"),
        ("e. Kr.", "efter Kristus"),
        ("v.t.", "efter Kristus"),
        ("AD", "efter Kristus"),
        ("A.D.", "efter Kristus"),
        ("CE", "efter Kristus"),
    ] {
        if let Some(a) = s.strip_suffix(era) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            for y in years(a) {
                out.push((format!("{y} {spoken}"), 0.0));
            }
            if let Some((before, last)) = a.rsplit_once(' ') {
                if year(last).is_some()
                    && MONTHS.iter().any(|(_, aliases)| {
                        aliases.iter().any(|m| before.ends_with(*m) || before.ends_with(format!("{m},").as_str()))
                    })
                {
                    for (v, c) in date(a) {
                        out.push((format!("{v} {spoken}"), c));
                    }
                }
            }
        }
    }
    let fields: Vec<&str> = s.split(|c| matches!(c, '.' | '/' | '-')).collect();
    if fields.len() == 3 {
        let (a, b, c) = (fields[0], fields[1], fields[2]);
        if let (Some(d), Some(m)) = (day(a), month(b)) {
            for y in years(c) {
                out.push((format!("{d} {} {y}", MONTHS[m - 1].0), 0.0));
            }
        }
        if b.len() == 2 && c.len() == 2 {
            if let (Some(d), Some(m)) = (day(c), month(b)) {
                for y in years(a) {
                    out.push((format!("{d} {} {y}", MONTHS[m - 1].0), 0.0));
                }
            }
        }
    }
    for (name, aliases) in MONTHS {
        for alias in aliases {
            if let Some((d, rest)) = s.split_once(format!(" {alias}").as_str()) {
                let mut d = d;
                for suf in [".ste", ".den", ".det", ".de", ".te", ".ende", ":a", "."] {
                    if let Some(v) = d.strip_suffix(suf) {
                        d = v;
                        break;
                    }
                }
                if let Some(d) = day(d) {
                    if rest.is_empty() {
                        out.push((format!("{d} {name}"), 0.0));
                    } else {
                        let rest = rest.strip_prefix(',').unwrap_or(rest);
                        if let Some(y) = rest.strip_prefix(' ') {
                            for y in years(y) {
                                out.push((format!("{d} {name} {y}"), 0.0));
                            }
                        }
                    }
                }
            }
        }
    }
    for (i, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].trim_end_matches(' ');
            let b = s[i + c.len_utf8()..].trim_start_matches(' ');
            for x in years(a) {
                for y in years(b) {
                    out.push((format!("{x} til {y}"), 0.0));
                }
            }
        }
    }
    const DECADES: [(&str, &str); 11] = [
        ("1920", "nittentyverne"),
        ("1930", "nittentrediverne"),
        ("1940", "nittenfyrrerne"),
        ("1950", "nittenhalvtredserne"),
        ("1960", "nittentresserne"),
        ("1970", "nittenhalvfjerdserne"),
        ("1980", "nittenfirserne"),
        ("1990", "nittenhalvfemserne"),
        ("2000", "totusinderne"),
        ("2010", "totusindtierne"),
        ("2020", "totusindtyverne"),
    ];
    if s.len() >= 4 && s.is_char_boundary(4) {
        let a = &s[..4];
        if a.len() == 4 && a.bytes().all(|b| b.is_ascii_digit()) && !a.starts_with('0') && a.ends_with('0') {
            let rest = &s[4..];
            let known = DECADES.iter().find(|x| x.0 == a).map(|x| x.1);
            if ["erne", "'erne", "’erne", "‘erne", "s", "'s", "’s", "‘s"].contains(&rest)
                || (!a.ends_with("00") && ["-tallet", "tallet"].contains(&rest))
            {
                if let Some(d) = known {
                    out.push((d.to_owned(), -0.2));
                }
                for y in years(a) {
                    out.push((format!("årtiet fra {y}"), -0.15));
                }
            }
            if rest == "-talsbygninger" {
                if let Some(d) = known {
                    out.push((format!("{}talsbygninger", &d[..d.len() - 2]), 0.0));
                }
            }
            if rest == "-tallet" && a.ends_with("00") {
                for y in years(a) {
                    out.push((format!("{y}tallet"), 0.0));
                }
            }
        }
    }
    out
}
fn hour(s: &str) -> Option<String> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n = s.parse::<u32>().ok()?;
    if n > 23 {
        return None;
    }
    number(&n.to_string(), false)
}
/// Recognize the bare hour-minute language and its competing ratio path.
pub fn simple_clock(s: &str) -> Option<(usize, bool)> {
    let (hour, minute) = s.split_once(':')?;
    if hour.is_empty()
        || hour.len() > 2
        || !hour.bytes().all(|b| b.is_ascii_digit())
        || minute.len() != 2
        || !minute.bytes().all(|b| b.is_ascii_digit())
        || hour.parse::<u8>().ok()? > 23
        || minute.parse::<u8>().ok()? > 59
    {
        return None;
    }
    Some((if minute == "00" { 2 } else { 1 }, (hour.len() == 1 || !hour.starts_with('0')) && !minute.starts_with('0')))
}
/// Recognize a prefixed, space-separated hour and minute without spelling it.
pub fn simple_prefixed_clock(s: &str) -> Option<usize> {
    let (prefix, time) = s.split_once(' ')?;
    if !["klokken", "Klokken", "kl.", "Kl.", "kl", "Kl"].contains(&prefix) {
        return None;
    }
    if !time.contains(':') {
        if !time.is_empty()
            && time.len() <= 2
            && time.bytes().all(|b| b.is_ascii_digit())
            && time.parse::<u8>().ok()? <= 23
        {
            return Some(1);
        }
        return None;
    }
    let (hour, minute) = time.split_once(':')?;
    if hour.is_empty()
        || hour.len() > 2
        || !hour.bytes().all(|b| b.is_ascii_digit())
        || minute.len() != 2
        || !minute.bytes().all(|b| b.is_ascii_digit())
        || hour.parse::<u8>().ok()? > 23
        || minute.parse::<u8>().ok()? > 59
    {
        return None;
    }
    Some(if minute == "00" { 2 } else { 1 })
}
fn clock(s: &str, spoken_zero: bool) -> Option<String> {
    let mut s = s;
    let mut p = "";
    for x in ["klokken", "Klokken", "kl.", "Kl.", "kl", "Kl"] {
        if let Some(a) = s.strip_prefix(x) {
            s = a.strip_prefix(' ').unwrap_or(a);
            p = "klokken ";
            break;
        }
    }
    if !p.is_empty() && !s.contains(':') {
        for x in ["klokken", "Klokken", "kl.", "Kl.", "kl", "Kl"] {
            if let Some(a) = s.strip_prefix(x) {
                s = a.strip_prefix(' ').unwrap_or(a);
                p = "klokken klokken ";
                break;
            }
        }
    }
    let mut tz = "";
    for (key, word) in [
        ("UTC", " u t c"),
        ("GMT", " g m t"),
        ("CET", " c e t"),
        ("CEST", " c e s t"),
        ("EST", " e s t"),
        ("EDT", " e d t"),
        ("PST", " p s t"),
        ("PDT", " p d t"),
    ] {
        if let Some(a) = s.strip_suffix(format!(" {key}").as_str()) {
            s = a;
            tz = word;
            break;
        }
    }
    let parts: Vec<_> = s.split(':').collect();
    if parts.len() == 1 {
        if p.is_empty() {
            return None;
        }
        return Some(format!("{p}{}{tz}", hour(parts[0])?));
    }
    if parts.len() > 3 {
        return None;
    }
    let h = hour(parts[0])?;
    let m = parts[1];
    if m.len() != 2 || !m.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mn = m.parse::<u32>().ok()?;
    if mn > 59 {
        return None;
    }
    let ms = if mn == 0 {
        if parts.len() == 3 || spoken_zero { " nul nul".into() } else { String::new() }
    } else if mn < 10 {
        format!(" nul {}", number(&mn.to_string(), false)?)
    } else {
        format!(" {}", number(&mn.to_string(), false)?)
    };
    let sec = if parts.len() == 3 {
        let x = parts[2];
        if x.is_empty() || !x.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let x = if x == "00" {
            "0"
        } else if x.len() == 2 && x.starts_with('0') {
            &x[1..]
        } else if !x.starts_with('0') && x.len() <= 3 {
            x
        } else {
            return None;
        };
        format!(" og {} sekunder", number(x, false)?)
    } else {
        String::new()
    };
    Some(format!("{p}{h}{ms}{sec}{tz}"))
}
pub fn simple_clock_reading(s: &str, index: usize) -> Option<String> {
    let (prefix, time) = match s.split_once(' ') {
        Some((_, time)) => ("klokken ", time),
        None => ("", s),
    };
    let (hour, minute) = match time.split_once(':') {
        Some((h, m)) => (h, Some(m)),
        None => (time, None),
    };
    let n = hour.parse::<u16>().ok()?;
    if n > 23 || index > 1 {
        return None;
    }
    let mut out = String::with_capacity(48);
    out.push_str(prefix);
    write_small_number(&mut out, n, false);
    if let Some(minute) = minute {
        let m = minute.parse::<u16>().ok()?;
        if m > 59 || (index == 1 && m != 0) {
            return None;
        }
        if m == 0 {
            if index == 0 {
                out.push_str(" nul nul");
            }
        } else {
            out.push(' ');
            if m < 10 {
                out.push_str("nul ");
            }
            write_small_number(&mut out, m, false);
        }
    } else if index != 0 {
        return None;
    }
    Some(out)
}
pub fn time(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for spoken in [true, false] {
        if let Some(x) = clock(s, spoken) {
            if !out.contains(&x) {
                out.push(x);
            }
        }
    }
    for (i, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].trim_end_matches(' ');
            let b = s[i + c.len_utf8()..].trim_start_matches(' ');
            for left in [true, false] {
                for right in [true, false] {
                    let x = clock(a, left).or_else(|| hour(a));
                    let y = clock(b, right).or_else(|| hour(b));
                    if let (Some(x), Some(y)) = (x, y) {
                        let result = format!("{x} til {y}");
                        if !out.contains(&result) {
                            out.push(result);
                        }
                    }
                }
            }
        }
    }
    out
}

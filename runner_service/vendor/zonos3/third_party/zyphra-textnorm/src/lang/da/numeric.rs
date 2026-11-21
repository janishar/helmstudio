// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::graph_utils::{append_integer, digit, grouped_integer};

pub fn number(s: &str, common: bool) -> Option<String> {
    grouped_integer(s, common)
}
pub fn prefix(s: &str) -> (&str, &str) {
    for (p, w) in [("-", "minus "), ("−", "minus "), ("~", "cirka "), ("±", "plus-minus ")] {
        if let Some(rest) = s.strip_prefix(p) {
            return (rest, w);
        }
    }
    (s, "")
}
/// Count the same-span readings of a bare comma decimal without spelling them.
pub fn simple_decimal_count(input: &str) -> Option<usize> {
    let (s, _) = prefix(input);
    let (integer, fraction) = s.split_once(',')?;
    if integer.is_empty()
        || integer.len() > 15
        || !integer.bytes().all(|b| b.is_ascii_digit())
        || (integer.len() > 1 && integer.starts_with('0'))
        || fraction.is_empty()
        || !fraction.bytes().all(|b| b.is_ascii_digit())
    {
        return None;
    }
    Some(if (2..=3).contains(&fraction.len()) && !fraction.starts_with('0') { 3 } else { 2 })
}
pub fn simple_decimal_reading(input: &str, index: usize) -> Option<String> {
    if index >= simple_decimal_count(input)? {
        return None;
    }
    let (s, modifier) = prefix(input);
    let (integer, fraction) = s.split_once(',')?;
    let mut out = String::with_capacity(input.len().saturating_mul(10).saturating_add(24));
    out.push_str(modifier);
    append_integer(&mut out, integer, true)?;
    out.push_str(" komma ");
    if index == 0 && fraction == "14" {
        out.push_str("en fire");
    } else if index == 2
        || (index == 0
            && fraction.len() <= 2
            && !fraction.starts_with('0')
            && fraction.parse::<u8>().ok().is_some_and(|n| n < 20))
    {
        append_integer(&mut out, fraction, false)?;
    } else {
        let mut first = true;
        for c in fraction.chars() {
            if !first {
                out.push(' ');
            }
            first = false;
            out.push_str(digit(c)?);
        }
    }
    Some(out)
}
pub fn digits(s: &str) -> Option<String> {
    let mut out = String::with_capacity(s.len().saturating_mul(6));
    for c in s.chars() {
        let word = digit(c)?;
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(word);
    }
    Some(out)
}
pub fn ordinal(s: &str) -> Option<String> {
    let n: u32 = s.parse().ok()?;
    if n == 0 || s.starts_with('0') || n > 999999 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    const SMALL: [&str; 20] = [
        "",
        "første",
        "anden",
        "tredje",
        "fjerde",
        "femte",
        "sjette",
        "syvende",
        "ottende",
        "niende",
        "tiende",
        "ellevte",
        "tolvte",
        "trettende",
        "fjortende",
        "femtende",
        "sekstende",
        "syttende",
        "attende",
        "nittende",
    ];
    const TENS: [&str; 10] = [
        "",
        "",
        "tyvende",
        "tredivte",
        "fyrrende",
        "halvtredsindstyvende",
        "tresindstyvende",
        "halvfjerdsindstyvende",
        "firsindstyvende",
        "halvfemsindstyvende",
    ];
    if n < 20 {
        return Some(SMALL[n as usize].into());
    }
    if n < 100 {
        return Some(if n % 10 == 0 {
            TENS[(n / 10) as usize].into()
        } else {
            format!("{}og{}", number(&(n % 10).to_string(), true)?, TENS[(n / 10) as usize])
        });
    }
    if n < 1000 {
        let h = if n / 100 == 1 {
            "hundrede".into()
        } else {
            format!("{}hundrede", number(&(n / 100).to_string(), false)?)
        };
        return Some(if n % 100 == 0 {
            format!("{h}de")
        } else {
            format!("{h} og {}", ordinal(&(n % 100).to_string())?)
        });
    }
    let th = n / 1000;
    if th > 999 {
        return None;
    }
    let h = if th == 1 { "tusind".into() } else { format!("{} tusind", number(&th.to_string(), false)?) };
    Some(if n % 1000 == 0 {
        format!("{h}e")
    } else {
        format!("{h}{}{}", if n % 1000 < 100 { " og " } else { " " }, ordinal(&(n % 1000).to_string())?)
    })
}
pub fn ordinal_tag(s: &str) -> Option<String> {
    if let Some(n) = s.strip_suffix('.') {
        return ordinal(n);
    }
    for (suffix, allowed) in [
        ("ste", "1"),
        ("den", "2"),
        ("det", "2"),
        ("je", "3"),
        ("de", "4 10 20 30 40 50"),
        ("te", "5 6"),
        ("ende", "7 8 9"),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix('.').unwrap_or(n);
            if allowed.split(' ').any(|x| x == n) {
                return if suffix == "det" { Some("andet".into()) } else { ordinal(n) };
            }
        }
    }
    None
}
pub fn ordinal_alternatives(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let Some(n) = s.strip_suffix('.') else { return out };
    if n.len() == 4 && (n.starts_with("19") || n.starts_with("20")) {
        if let Some(o) = ordinal(&n[2..]) {
            out.push((format!("{} og {o}", if n.starts_with("19") { "nitten hundrede" } else { "to tusind" }), 0.0));
        }
    }
    if let Ok(v) = n.parse::<u32>() {
        if (50..=90).contains(&v) && v % 10 == 0 {
            let stem = match v {
                50 => "halvtreds",
                60 => "tres",
                70 => "halvfjerds",
                80 => "firs",
                90 => "halvfems",
                _ => "",
            };
            out.push((format!("{stem}ende"), 0.05));
        }
        if (21..=99).contains(&v) && v % 10 == 1 {
            if let Some(w) = ordinal(n) {
                if let Some(rest) = w.strip_prefix("enog") {
                    out.push((format!("etog{rest}"), 0.05));
                }
            }
        }
        if v == 1000 {
            out.push(("et tusinde".into(), 0.0));
        }
    }
    out
}
pub fn decimal(s: &str) -> Option<(String, f64)> {
    let (s, p) = prefix(s);
    let i = s.find([',', '.'])?;
    let (a, rest) = s.split_at(i);
    let sep = rest.chars().next()?;
    let b = &rest[1..];
    if b.is_empty() || !b.bytes().all(|x| x.is_ascii_digit()) || (sep == '.' && b == "000") {
        return None;
    }
    let left = number(a, true)?;
    let right = if b == "14" {
        "en fire".into()
    } else if b.len() <= 2 && !b.starts_with('0') && b.parse::<u8>().ok().is_some_and(|n| n < 20) {
        number(b, false)?
    } else {
        digits(b)?
    };
    Some((format!("{p}{left} komma {right}"), if sep == '.' { 0.5 } else { 0.0 }))
}
pub fn decimal_alternatives(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (s, p) = prefix(s);
    let Some(i) = s.find([',', '.']) else { return out };
    let a = &s[..i];
    let b = &s[i + 1..];
    if b.is_empty() || !b.bytes().all(|c| c.is_ascii_digit()) || (s.as_bytes()[i] == b'.' && b == "000") {
        return out;
    }
    let Some(left) = number(a, true) else { return out };
    let cost = if s.as_bytes()[i] == b'.' { 0.5 } else { 0.0 };
    if let Some(d) = digits(b) {
        out.push((format!("{p}{left} komma {d}"), cost));
    }
    if b.len() <= 3 && !b.starts_with('0') {
        if let Some(n) = number(b, false) {
            let x = format!("{p}{left} komma {n}");
            if !out.iter().any(|(v, _)| v == &x) {
                out.push((x, cost));
            }
        }
    }
    out
}
pub fn decimal_full(s: &str) -> Option<(String, f64)> {
    for (idx, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..idx].trim_end_matches(' ');
            let b = s[idx + c.len_utf8()..].trim_start_matches(' ');
            if let (Some((x, cx)), Some((y, cy))) = (decimal(a), decimal(b)) {
                return Some((format!("{x} til {y}"), cx + cy));
            }
        }
    }
    const SCALES: [(&str, &str, &str); 15] = [
        ("millioner", "million", "millioner"),
        ("million", "million", "millioner"),
        ("mio.", "million", "millioner"),
        ("mio", "million", "millioner"),
        ("mn", "million", "millioner"),
        ("milliarder", "milliard", "milliarder"),
        ("milliard", "milliard", "milliarder"),
        ("mia.", "milliard", "milliarder"),
        ("mia", "milliard", "milliarder"),
        ("md", "milliard", "milliarder"),
        ("billioner", "billion", "billioner"),
        ("billion", "billion", "billioner"),
        ("bn", "billion", "billioner"),
        ("", "", ""),
        ("", "", ""),
    ];
    for (key, sing, plur) in SCALES {
        if !key.is_empty() {
            if let Some(a) = s.strip_suffix(key) {
                let a = a.strip_suffix(' ').unwrap_or(a);
                if let Some((n, c)) = decimal(a) {
                    return Some((format!("{n} {plur}"), c));
                }
                if let Some(n) = number(a, true) {
                    if a == "1" && !key.ends_with("er") {
                        return Some((format!("{n} {sing}"), 0.0));
                    }
                    if a != "1" {
                        return Some((format!("{n} {plur}"), 0.0));
                    }
                }
            }
        }
    }
    decimal(s)
}
pub fn cardinal(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (v, p) = prefix(s);
    if let Some(n) = number(v, false) {
        out.push((format!("{p}{n}"), 0.0));
    }
    if p.is_empty() && v.len() >= 2 && v.bytes().all(|b| b.is_ascii_digit()) {
        if let Some(n) = digits(v) {
            out.push((n, 1.5));
        }
        if v.starts_with('0') {
            let zeros = v.bytes().take_while(|b| *b == b'0').count();
            if let Some(n) = number(&v[zeros..], false) {
                out.push((format!("{}{n}", "nul ".repeat(zeros)), 1.5));
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        if let Some(o) = ordinal(b) {
            if (2..=10).contains(&b.parse::<u32>().unwrap_or(0)) {
                if let Some(n) =
                    number(a, false).or_else(|| ["x", "y", "z", "n", "a", "b"].contains(&a).then(|| a.to_owned()))
                {
                    out.push((format!("{n} i {o}"), 0.05));
                }
            }
        }
    }
    if let Some((expression, result)) = s.split_once('=') {
        let result = result.strip_prefix(' ').unwrap_or(result);
        let expression = expression.strip_suffix(' ').unwrap_or(expression);
        if let Some(result) = number(result, false) {
            for (i, c) in expression.char_indices().skip(1) {
                if matches!(c, '+' | '-' | '−' | '*' | '×' | 'x' | '÷' | '/') {
                    let left = expression[..i].strip_suffix(' ').unwrap_or(&expression[..i]);
                    let right =
                        expression[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&expression[i + c.len_utf8()..]);
                    if c == '/' && !(expression[..i].ends_with(' ') && expression[i + 1..].starts_with(' ')) {
                        continue;
                    }
                    if let (Some(a), Some(b)) = (number(left, false), number(right, false)) {
                        let op = match c {
                            '+' => "plus",
                            '-' | '−' => "minus",
                            '*' | '×' | 'x' => "gange",
                            _ => "divideret med",
                        };
                        out.push((format!("{a} {op} {b} er lig med {result}"), 0.05));
                    }
                }
            }
        }
    }
    for (idx, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—' | ':' | '+' | '−' | '*' | '×' | 'x' | '÷' | '=' | '/') {
            let left = s[..idx].trim_end_matches(' ');
            let right = s[idx + c.len_utf8()..].trim_start_matches(' ');
            if c == '/' && !(s[..idx].ends_with(' ') && s[idx + 1..].starts_with(' ')) {
                continue;
            }
            if let (Some(a), Some(b)) = (number(left, c == ':'), number(right, c == ':')) {
                let op = match c {
                    ':' => " til ",
                    '+' => " plus ",
                    '−' | '-' => " minus ",
                    '*' | '×' | 'x' => " gange ",
                    '÷' | '/' => " divideret med ",
                    '=' => " er lig med ",
                    _ => " til ",
                };
                if matches!(c, '-' | '–') && left.len() <= 2 && right.len() <= 2 {
                    out.push((format!("{a} {b}"), 0.0));
                }
                if matches!(c, '-' | '–' | '—') {
                    out.push((format!("{a} til {b}"), 0.1));
                }
                if c != '–' && c != '—' {
                    out.push((format!("{a}{op}{b}"), 0.05));
                }
            }
        }
    }
    if s.matches('.').count() >= 2 {
        let p: Option<Vec<_>> = s.split('.').map(|x| number(x, false)).collect();
        if let Some(p) = p {
            out.push((p.join(" punktum "), 0.0));
        }
    }
    for suf in ["G", "g", "K", "k", "D", "d"] {
        if let Some(a) = s.strip_suffix(suf) {
            if let Some(n) = number(a, false) {
                out.push((format!("{n} {suf}"), 0.0));
            }
        }
    }
    if s == "CO2" || s == "co2" {
        out.push((format!("{} to", &s[..2]), 0.0));
    }
    out
}

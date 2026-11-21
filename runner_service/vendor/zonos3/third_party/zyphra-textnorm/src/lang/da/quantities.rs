// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::graph_utils::{append_integer, small_common};
use super::numeric::{decimal, number, prefix};
use std::collections::HashMap;
use std::sync::LazyLock;
const UNITS: &str = include_str!("../../../data/da/units.tsv");
const CURRENCIES: &str = include_str!("../../../data/da/currencies.tsv");
type Unit = (&'static str, &'static str, &'static str, &'static str);
static UNITS_BY_LAST: LazyLock<HashMap<char, Vec<Unit>>> = LazyLock::new(|| {
    let mut result: HashMap<char, Vec<Unit>> = HashMap::new();
    for row in UNITS.lines() {
        let mut p = row.split('\t');
        if let (Some(sym), Some(sing), Some(plur), Some(gender)) = (p.next(), p.next(), p.next(), p.next()) {
            if let Some(c) = sym.chars().last() {
                result.entry(c).or_default().push((sym, sing, plur, gender));
            }
        }
    }
    result
});
static CURRENCY_ROWS: LazyLock<Vec<[&'static str; 5]>> = LazyLock::new(|| {
    CURRENCIES
        .lines()
        .filter_map(|row| {
            let mut p = row.split('\t');
            Some([p.next()?, p.next()?, p.next()?, p.next()?, p.next()?])
        })
        .collect()
});
static UNIT_BY_SYMBOL: LazyLock<HashMap<&'static str, Unit>> = LazyLock::new(|| {
    let mut result = HashMap::new();
    for row in UNITS.lines() {
        let mut p = row.split('\t');
        if let (Some(sym), Some(sing), Some(plur), Some(gender)) = (p.next(), p.next(), p.next(), p.next()) {
            result.entry(sym).or_insert((sym, sing, plur, gender));
        }
    }
    result
});
static CURRENCY_BY_SYMBOL: LazyLock<HashMap<&'static str, [&'static str; 5]>> = LazyLock::new(|| {
    let mut result = HashMap::new();
    for row in CURRENCY_ROWS.iter() {
        result.entry(row[0]).or_insert(*row);
    }
    result
});
static CURRENCY_PREFIXES: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for row in CURRENCIES.lines() {
        if let Some(symbol) = row.split('\t').next() {
            if let Some(c) = symbol.chars().next() {
                index.entry(c).or_default().push(symbol);
            }
        }
    }
    index
});
/// A currency-prefix expression may contain spaces after its initial symbol.
pub fn currency_prefix(first: &str) -> bool {
    first
        .chars()
        .next()
        .and_then(|c| CURRENCY_PREFIXES.get(&c))
        .is_some_and(|symbols| symbols.iter().any(|symbol| first.starts_with(*symbol)))
}
/// Recognize an unambiguous integer followed by one space and a unit or currency.
/// The spoken value is rebuilt only if the lattice selects this span.
pub fn simple_space_value(s: &str) -> Option<(f64, i32)> {
    let (digits, symbol) = s.split_once(' ')?;
    if digits.is_empty()
        || digits.len() > 15
        || !digits.bytes().all(|b| b.is_ascii_digit())
        || (digits.len() > 1 && digits.starts_with('0'))
        || symbol.contains(' ')
    {
        return None;
    }
    let units = UNIT_BY_SYMBOL.contains_key(symbol);
    let money = CURRENCY_BY_SYMBOL.contains_key(symbol);
    if units == money || (money && digits == "0") {
        return None;
    }
    let et = digits.starts_with('1') && (digits.len() == 3 || digits.len() == 4);
    Some((if et { 1.04 } else { 1.05 }, if units { 4 } else { 5 }))
}
pub fn simple_space_reading(s: &str, branch: i32) -> Option<String> {
    if simple_space_value(s)?.1 != branch {
        return None;
    }
    let (digits, symbol) = s.split_once(' ')?;
    let (one, name) = if branch == 4 {
        let &(_, sing, plural, gender) = UNIT_BY_SYMBOL.get(symbol)?;
        (if gender == "c" { "en" } else { "et" }, if digits == "1" { sing } else { plural })
    } else if branch == 5 {
        let p = CURRENCY_BY_SYMBOL.get(symbol)?;
        (if p[1] == "pund" || p[1] == "britisk pund" { "et" } else { "en" }, if digits == "1" { p[1] } else { p[2] })
    } else {
        return None;
    };
    let mut out = String::with_capacity(digits.len() * 10 + name.len() + 10);
    if digits == "1" {
        out.push_str(one);
    } else {
        if digits.starts_with('1') && (digits.len() == 3 || digits.len() == 4) {
            out.push_str("et ");
        }
        append_integer(&mut out, digits, true)?;
    }
    out.push(' ');
    out.push_str(name);
    Some(out)
}
fn et(s: &str) -> Option<String> {
    let mut digits = s.chars().filter(|c| c.is_ascii_digit());
    if digits.next()? != '1' || !matches!(digits.count(), 2 | 3) {
        return None;
    }
    Some(format!("et {}", number(s, false)?))
}
fn fraction_core(s: &str) -> Option<String> {
    let vulgar = [
        ('½', "en halv"),
        ('⅓', "en tredjedel"),
        ('⅔', "to tredjedele"),
        ('¼', "en fjerdedel"),
        ('¾', "tre fjerdedele"),
        ('⅕', "en femtedel"),
        ('⅖', "to femtedele"),
        ('⅗', "tre femtedele"),
        ('⅘', "fire femtedele"),
        ('⅙', "en sjettedel"),
        ('⅚', "fem sjettedele"),
        ('⅛', "en ottendedel"),
        ('⅜', "tre ottendedele"),
        ('⅝', "fem ottendedele"),
        ('⅞', "syv ottendedele"),
        ('⅐', "en syvendedel"),
        ('⅑', "en niendedel"),
        ('⅒', "en tiendedel"),
    ];
    if s.chars().count() == 1 {
        if let Some((_, v)) = vulgar.iter().find(|x| Some(x.0) == s.chars().next()) {
            return Some((*v).into());
        }
    }
    let (a, b) = s.split_once('/')?;
    if a.is_empty() || !a.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let (sing, plur) = match b {
        "2" => ("halv", "halve"),
        "3" => ("tredjedel", "tredjedele"),
        "4" => ("fjerdedel", "fjerdedele"),
        "5" => ("femtedel", "femtedele"),
        "6" => ("sjettedel", "sjettedele"),
        "7" => ("syvendedel", "syvendedele"),
        "8" => ("ottendedel", "ottendedele"),
        "9" => ("niendedel", "niendedele"),
        "10" => ("tiendedel", "tiendedele"),
        "12" => ("tolvtedel", "tolvtedele"),
        "16" => ("sekstendedel", "sekstendedele"),
        "20" => ("tyvendedel", "tyvendedele"),
        "100" => ("hundrededel", "hundrededele"),
        "1000" => ("tusindedel", "tusindedele"),
        _ => return None,
    };
    Some(format!("{} {}", number(a, true)?, if a == "1" { sing } else { plur }))
}
pub fn fraction(s: &str) -> Option<String> {
    let (s, p) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { (x, "minus ") } else { (s, "") };
    if let Some(f) = fraction_core(s) {
        return Some(format!("{p}{f}"));
    }
    for (i, _) in s.char_indices().skip(1) {
        let a = s[..i].trim_end_matches(' ');
        let b = s[i..].trim_start_matches(' ');
        if let (Some(n), Some(f)) = (number(a, false), fraction_core(b)) {
            return Some(format!("{p}{n} og {f}"));
        }
    }
    None
}
fn measure_core(s: &str) -> Option<(String, f64)> {
    for &(sym, sing, plur, gender) in UNITS_BY_LAST.get(&s.chars().last()?)? {
        if let Some(a) = s.strip_suffix(sym) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            if a == "1" {
                return Some((format!("{} {sing}", if gender == "c" { "en" } else { "et" }), 0.0));
            }
            if let Some((n, c)) = decimal(a) {
                return Some((format!("{n} {plur}"), c));
            }
            if a.bytes().all(|b| b.is_ascii_digit()) {
                if let Some(n) = et(a) {
                    return Some((format!("{n} {plur}"), -0.01));
                }
                if let Some(n) = number(a, true) {
                    return Some((format!("{n} {plur}"), 0.0));
                }
            }
        }
    }
    None
}
pub fn measure(s: &str) -> Option<(String, f64)> {
    let (s, p) = prefix(s);
    for (i, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].trim_end_matches(' ');
            let b = s[i + c.len_utf8()..].trim_start_matches(' ');
            if let Some((y, cy)) = measure_core(b) {
                if let Some((x, cx)) = measure_core(a) {
                    return Some((format!("{p}{x} til {y}"), cx + cy));
                }
                if let Some(x) = number(a, true) {
                    return Some((format!("{p}{x} til {y}"), cy));
                }
            }
        }
    }
    let (x, c) = measure_core(s)?;
    Some((format!("{p}{x}"), c))
}
fn money_amount(a: &str, sing: &str, plur: &str, msing: &str, mplur: &str) -> Option<(String, f64)> {
    let major = |v: &str| -> Option<(String, f64)> {
        if v == "1" {
            Some((format!("{} {sing}", if sing == "pund" || sing == "britisk pund" { "et" } else { "en" }), 0.0))
        } else if v == "0" || !v.bytes().all(|b| b.is_ascii_digit()) {
            None
        } else if let Some(n) = et(v) {
            Some((format!("{n} {plur}"), -0.01))
        } else {
            Some((format!("{} {plur}", number(v, true)?), 0.0))
        }
    };
    if !msing.is_empty() {
        for (i, c) in a.char_indices() {
            if c == ',' || c == '.' {
                let x = &a[..i];
                let y = &a[i + 1..];
                if y == "00" {
                    return major(x);
                }
                let m = if y == "1" || y == "01" {
                    format!("en {msing}")
                } else {
                    let y = y.strip_prefix('0').unwrap_or(y);
                    format!("{} {mplur}", small_common(y)?)
                };
                if x == "0" {
                    return Some((m, -0.05));
                }
                let (n, cost) = major(x)?;
                return Some((format!("{n} og {m}"), cost));
            }
        }
    }
    major(a)
}
fn money_core(s: &str) -> Option<(String, f64)> {
    for (sym, currency) in [("$", "dollar"), ("€", "euro")] {
        if let Some(a) = s.strip_prefix(sym) {
            let a = a.strip_prefix(' ').unwrap_or(a);
            for (suffix, scale) in
                [("k", "tusind"), ("M", "millioner"), ("m", "millioner"), ("B", "milliarder"), ("b", "milliarder")]
            {
                if let Some(v) = a.strip_suffix(suffix) {
                    if let Some(n) = number(v, true) {
                        return Some((format!("{n} {scale} {currency}"), 0.0));
                    }
                    if sym == "$" && (suffix == "M" || suffix == "m") {
                        if let Some((a, b)) = v.split_once([',', '.']) {
                            if b.len() == 1 && b.bytes().all(|c| c.is_ascii_digit()) {
                                if let (Some(n), Some(d)) = (number(a, true), number(b, false)) {
                                    return Some((format!("{n} komma {d} {scale} {currency}"), 0.0));
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    for (suffix, scale) in [
        ("mio.", "millioner"),
        ("mio", "millioner"),
        ("millioner", "millioner"),
        ("mia.", "milliarder"),
        ("mia", "milliarder"),
        ("md", "milliarder"),
        ("milliarder", "milliarder"),
    ] {
        for currency in ["kr.", "kr", "DKK"] {
            if let Some(a) = s.strip_suffix(currency) {
                let a = a.strip_suffix(' ').unwrap_or(a);
                if let Some(a) = a.strip_suffix(suffix) {
                    let a = a.strip_suffix(' ').unwrap_or(a);
                    if let Some(n) = number(a, true) {
                        return Some((format!("{n} {scale} kroner"), 0.0));
                    }
                }
            }
        }
    }
    for p in CURRENCY_ROWS.iter() {
        if let Some(a) = s.strip_prefix(p[0]) {
            let a = a.strip_prefix(' ').unwrap_or(a);
            for (suffix, scale) in [
                ("millioner", "millioner"),
                ("million", "millioner"),
                ("mio.", "millioner"),
                ("mio", "millioner"),
                ("mn", "millioner"),
                ("milliarder", "milliarder"),
                ("milliard", "milliarder"),
                ("mia.", "milliarder"),
                ("mia", "milliarder"),
                ("md", "milliarder"),
                ("billioner", "billioner"),
                ("billion", "billioner"),
                ("bn", "billioner"),
            ] {
                if let Some(v) = a.strip_suffix(suffix) {
                    let v = v.strip_suffix(' ').unwrap_or(v);
                    if v != "0" && v.bytes().all(|b| b.is_ascii_digit()) {
                        if let Some(n) = et(v) {
                            return Some((format!("{n} {scale} {}", p[2]), -0.01));
                        }
                        if let Some(n) = number(v, true) {
                            return Some((format!("{n} {scale} {}", p[2]), 0.0));
                        }
                    }
                }
            }
            if let Some(v) = money_amount(a, p[1], p[2], p[3], p[4]) {
                return Some(v);
            }
        }
        if let Some(a) = s.strip_suffix(p[0]) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            if let Some(v) = money_amount(a, p[1], p[2], p[3], p[4]) {
                return Some(v);
            }
        }
    }
    None
}
pub fn money(s: &str) -> Option<(String, f64)> {
    let (s, p) =
        if let Some(a) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { (a, "minus ") } else { (s, "") };
    for (suf, w) in [("/h", " i timen"), ("/time", " i timen"), ("/kg", " pr. kilogram")] {
        if let Some(a) = s.strip_suffix(suf) {
            let (v, c) = money_core(a)?;
            return Some((format!("{p}{v}{w}"), c));
        }
    }
    for (i, ch) in s.char_indices().skip(1) {
        if matches!(ch, '-' | '–' | '—') {
            let a = s[..i].trim_end_matches(' ');
            let b = s[i + ch.len_utf8()..].trim_start_matches(' ');
            if let Some((y, cy)) = money_core(b) {
                if let Some((x, cx)) = money_core(a) {
                    return Some((format!("{p}{x} til {y}"), cx + cy));
                }
                if let Some(x) = number(a, true) {
                    return Some((format!("{p}{x} til {y}"), cy));
                }
            }
        }
    }
    let (v, c) = money_core(s)?;
    Some((format!("{p}{v}"), c))
}

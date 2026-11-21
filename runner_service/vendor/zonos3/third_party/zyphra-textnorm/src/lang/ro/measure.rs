// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
use super::graph_utils::{Gender, number, parse_number};
use super::money::counted;
const UNITS: &[(&[&str], &str, &str, Gender)] = &[
    (&["kg", "KG", "kilograme", "kilogram"], "kilogram", "kilograme", Gender::Neuter),
    (&["g", "G", "grame", "gram"], "gram", "grame", Gender::Neuter),
    (&["mg", "MG", "miligrame", "miligram"], "miligram", "miligrame", Gender::Neuter),
    (&["µg", "μg", "ug", "micrograme"], "microgram", "micrograme", Gender::Neuter),
    (&["t", "tone", "tonă"], "tonă", "tone", Gender::Feminine),
    (&["km", "KM", "kilometri", "kilometru"], "kilometru", "kilometri", Gender::Masculine),
    (&["m", "metri", "metru"], "metru", "metri", Gender::Masculine),
    (&["cm", "CM", "centimetri", "centimetru"], "centimetru", "centimetri", Gender::Masculine),
    (&["mm", "MM", "milimetri", "milimetru"], "milimetru", "milimetri", Gender::Masculine),
    (&["µm", "μm", "um", "micrometri"], "micrometru", "micrometri", Gender::Masculine),
    (&["m²", "mp", "metri pătrați"], "metru pătrat", "metri pătrați", Gender::Masculine),
    (&["km²", "kmp", "kilometri pătrați"], "kilometru pătrat", "kilometri pătrați", Gender::Masculine),
    (&["m³", "mc", "metri cubi"], "metru cub", "metri cubi", Gender::Masculine),
    (&["l", "L", "litri", "litru"], "litru", "litri", Gender::Masculine),
    (&["ml", "mL", "ML", "mililitri", "mililitru"], "mililitru", "mililitri", Gender::Masculine),
    (&["°C", "℃", "grade Celsius", "grade celsius"], "grad celsius", "grade celsius", Gender::Neuter),
    (&["°F", "℉", "grade Fahrenheit", "grade fahrenheit"], "grad fahrenheit", "grade fahrenheit", Gender::Neuter),
    (&["°", "grade", "grad"], "grad", "grade", Gender::Neuter),
    (&["km/h", "kmh", "km/oră"], "kilometru pe oră", "kilometri pe oră", Gender::Masculine),
    (&["m/s", "ms"], "metru pe secundă", "metri pe secundă", Gender::Masculine),
    (&["GB", "gb", "gigabytes", "gigabyți"], "gigabyte", "gigabyți", Gender::Masculine),
    (&["MB", "mb", "megabytes", "megabyți"], "megabyte", "megabyți", Gender::Masculine),
    (&["KB", "kb", "kilobytes", "kilobyți"], "kilobyte", "kilobyți", Gender::Masculine),
    (&["TB", "tb", "terabytes", "terabyți"], "terabyte", "terabyți", Gender::Masculine),
    (&["B", "bytes", "byți"], "byte", "byți", Gender::Masculine),
    (&["bit", "biți"], "bit", "biți", Gender::Masculine),
    (&["Gbps", "gbps"], "gigabit pe secundă", "gigabiți pe secundă", Gender::Masculine),
    (&["Mbps", "mbps"], "megabit pe secundă", "megabiți pe secundă", Gender::Masculine),
    (&["kbps", "Kbps"], "kilobit pe secundă", "kilobiți pe secundă", Gender::Masculine),
    (&["GHz", "ghz"], "gigahertz", "gigahertz", Gender::Neuter),
    (&["MHz", "mhz"], "megahertz", "megahertz", Gender::Neuter),
    (&["kHz", "khz"], "kilohertz", "kilohertz", Gender::Neuter),
    (&["Hz", "hz"], "hertz", "hertz", Gender::Neuter),
    (&["kWh", "kwh"], "kilowatt-oră", "kilowați-oră", Gender::Masculine),
    (&["mAh", "mah"], "miliamper-oră", "miliamperi-oră", Gender::Masculine),
    (&["hPa", "hpa"], "hectopascal", "hectopascali", Gender::Masculine),
    (&["kcal", "calorii"], "kilocalorie", "kilocalorii", Gender::Feminine),
    (&["dB", "db"], "decibel", "decibeli", Gender::Masculine),
    (&["Ω", "Ω", "ohmi"], "ohm", "ohmi", Gender::Masculine),
    (&["W", "wați", "watt"], "watt", "wați", Gender::Masculine),
    (&["kW", "kilowați"], "kilowatt", "kilowați", Gender::Masculine),
    (&["V", "volți"], "volt", "volți", Gender::Masculine),
    (&["A", "amperi"], "amper", "amperi", Gender::Masculine),
    (&["h", "ore", "oră"], "oră", "ore", Gender::Feminine),
    (&["min", "minute", "minut"], "minut", "minute", Gender::Neuter),
    (&["s", "sec", "secunde", "secundă"], "secundă", "secunde", Gender::Feminine),
    (&["zile", "zi"], "zi", "zile", Gender::Feminine),
    (&["luni", "lună"], "lună", "luni", Gender::Feminine),
    (&["ani", "an"], "an", "ani", Gender::Masculine),
];
use std::collections::HashMap;
use std::sync::LazyLock;

static SUFFIXES: LazyLock<HashMap<char, Vec<(usize, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(usize, &'static str)>> = HashMap::new();
    for (row, &(symbols, _, _, _)) in UNITS.iter().enumerate() {
        for &symbol in symbols {
            if let Some(c) = symbol.chars().last() {
                index.entry(c).or_default().push((row, symbol));
            }
        }
    }
    index
});

/// Cheap suffix index used before attempting the counted-unit graphs.
pub fn may_match(input: &str) -> bool {
    match input.chars().last() {
        Some('%' | '‰') => true,
        Some(c) => SUFFIXES.get(&c).is_some_and(|suffixes| suffixes.iter().any(|(_, s)| input.ends_with(*s))),
        None => false,
    }
}

pub struct Simple<'a> {
    pub number: &'a str,
    pub row: usize,
    pub serial: bool,
    pub spaced: bool,
}

/// Allocation-free recognition of integer + unit. Ambiguous date, ordinal and
/// currency endings use the full union instead.
pub fn simple(input: &str) -> Option<Simple<'_>> {
    if input.ends_with('s') || input.ends_with('°') || super::money::may_match(input) {
        return None;
    }
    let suffixes = SUFFIXES.get(&input.chars().last()?)?;
    for &(row, symbol) in suffixes {
        let Some(prefix) = input.strip_suffix(symbol) else { continue };
        let spaced = prefix.ends_with(' ');
        let n = prefix.strip_suffix(' ').unwrap_or(prefix);
        if n.is_empty() || !n.bytes().all(|b| b.is_ascii_digit()) || parse_number(n).is_none() {
            continue;
        }
        let serial = !spaced && symbol.chars().all(|c| c.is_ascii_alphabetic() || "ăâîșțĂÂÎȘȚşţŞŢ".contains(c));
        return Some(Simple { number: n, row, serial, spaced });
    }
    None
}

pub fn render_simple(value: &Simple<'_>) -> Option<String> {
    let (_, singular, plural, gender) = UNITS[value.row];
    counted(value.number, singular, plural, gender)
}

fn decimal(s: &str, gender: Gender) -> Option<String> {
    for (i, c) in s.char_indices() {
        if c == '.' || c == ',' {
            let a = number(&s[..i], gender);
            let b = &s[i + 1..];
            if b.len() == 1 {
                if let (Some(a), Some(b)) = (a, number(b, Gender::Masculine)) {
                    return Some(format!("{} virgulă {}", a, b));
                }
            }
        }
    }
    None
}
pub fn measure(input: &str) -> Vec<String> {
    let mut out = Vec::new();
    for &(symbols, sing, plur, gender) in UNITS {
        for &sym in symbols {
            if let Some(raw) = input.strip_suffix(sym) {
                let raw = raw.strip_suffix(' ').unwrap_or(raw);
                let (negative, raw) = if let Some(v) = raw.strip_prefix('-').or_else(|| raw.strip_prefix('−')) {
                    (true, v)
                } else {
                    (false, raw)
                };
                let (approx, raw) = if let Some(v) = raw.strip_prefix('~') { (true, v) } else { (false, raw) };
                let prefix =
                    format!("{}{}", if negative { "minus " } else { "" }, if approx { "aproximativ " } else { "" });
                if let Some(v) = counted(raw, sing, plur, gender) {
                    out.push(format!("{}{}", prefix, v));
                }
                if let Some(v) = decimal(raw, Gender::Masculine) {
                    out.push(format!("{}{} {}", prefix, v, plur));
                }
                if !negative && !approx {
                    for (i, c) in raw.char_indices() {
                        if matches!(c, '-' | '–' | '—' | '−') {
                            let a = raw[..i].strip_suffix(' ').unwrap_or(&raw[..i]);
                            let b = raw[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&raw[i + c.len_utf8()..]);
                            if let (Some(a), Some(b)) = (number(a, Gender::Masculine), counted(b, sing, plur, gender)) {
                                out.push(format!("{} până la {}", a, b));
                            }
                        }
                    }
                }
                if ["1/2", "½"].contains(&raw) && ["kg", "kilogram", "h", "oră"].contains(&sym) {
                    out.push(format!("o jumătate de {}", if sym == "h" || sym == "oră" { "oră" } else { "kilogram" }));
                }
                if ["2 1/2", "2½"].contains(&raw) && ["kg", "kilogram", "kilograme"].contains(&sym) {
                    out.push("două kilograme și jumătate".to_owned());
                }
            }
        }
    }
    for (sym, suffix) in [('%', "la sută"), ('‰', "la mie")] {
        if let Some(raw) = input.strip_suffix(sym) {
            let raw = raw.strip_suffix(' ').unwrap_or(raw);
            let (prefix, raw) = if let Some(v) = raw.strip_prefix('±') {
                ("plus-minus ", v)
            } else if let Some(v) = raw.strip_prefix('-').or_else(|| raw.strip_prefix('−')) {
                ("minus ", v)
            } else {
                ("", raw)
            };
            if let Some(v) = number(raw, Gender::Masculine) {
                out.push(format!("{}{} {}", prefix, v, suffix));
            }
            for g in [Gender::Feminine, Gender::Masculine] {
                if let Some(v) = decimal(raw, g) {
                    out.push(format!("{}{} {}", prefix, v, suffix));
                }
            }
            if prefix.is_empty() {
                for (i, c) in raw.char_indices() {
                    if matches!(c, '-' | '–' | '—' | '−') {
                        let a = raw[..i].strip_suffix(' ').unwrap_or(&raw[..i]);
                        let b = raw[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&raw[i + c.len_utf8()..]);
                        if let (Some(a), Some(b)) = (number(a, Gender::Masculine), number(b, Gender::Masculine)) {
                            out.push(format!("{} până la {} {}", a, b, suffix));
                        }
                    }
                }
            }
        }
    }
    out
}

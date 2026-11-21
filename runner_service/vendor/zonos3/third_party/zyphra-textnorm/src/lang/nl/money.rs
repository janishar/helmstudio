//! Currency amounts, minor units, rates and ranges.
use super::numeric::{self, Reading};
use std::collections::HashSet;
use std::sync::LazyLock;

const CURRENCIES: &[(&str, &str, &str)] = &[
    ("€", "euro", "cent"),
    ("EUR", "euro", "cent"),
    ("$", "dollar", "cent"),
    ("USD", "Amerikaanse dollar", "cent"),
    ("CA$", "Canadese dollar", "cent"),
    ("CAD", "Canadese dollar", "cent"),
    ("A$", "Australische dollar", "cent"),
    ("AUD", "Australische dollar", "cent"),
    ("£", "pond", "pence"),
    ("GBP", "Britse pond", "pence"),
    ("¥", "yen", "sen"),
    ("JPY", "yen", "sen"),
    ("CHF", "Zwitserse frank", "centime"),
    ("₹", "Indiase roepie", "paisa"),
    ("Rs.", "Indiase roepie", "paisa"),
    ("Rs", "Indiase roepie", "paisa"),
    ("INR", "Indiase roepie", "paisa"),
    ("₽", "Russische roebel", "kopeke"),
    ("RUB", "Russische roebel", "kopeke"),
    ("₺", "Turkse lira", "kuruş"),
    ("TRY", "Turkse lira", "kuruş"),
    ("₪", "Israëlische sjekel", "agora"),
    ("ILS", "Israëlische sjekel", "agora"),
    ("₴", "Oekraïense hryvnia", "kopiika"),
    ("UAH", "Oekraïense hryvnia", "kopiika"),
    ("₫", "Vietnamese dong", "xu"),
    ("VND", "Vietnamese dong", "xu"),
    ("৳", "Bengaalse taka", "poisha"),
    ("BDT", "Bengaalse taka", "poisha"),
    ("₦", "Nigeriaanse naira", "kobo"),
    ("NGN", "Nigeriaanse naira", "kobo"),
    ("₱", "Filipijnse peso", "centavo"),
    ("PHP", "Filipijnse peso", "centavo"),
    ("฿", "Thaise baht", "satang"),
    ("THB", "Thaise baht", "satang"),
    ("֏", "Armeense dram", "luma"),
    ("AMD", "Armeense dram", "luma"),
    ("₩", "won", "jeon"),
    ("KRW", "won", "jeon"),
    ("R$", "Braziliaanse real", "centavo"),
    ("BRL", "Braziliaanse real", "centavo"),
    ("MX$", "Mexicaanse peso", "centavo"),
    ("MXN", "Mexicaanse peso", "centavo"),
];

static CURRENCY_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for &(symbol, _, _) in CURRENCIES {
        for (i, c) in symbol.char_indices() {
            prefixes.insert(&symbol[..i + c.len_utf8()]);
        }
    }
    prefixes
});

pub(super) fn starts_spaced(s: &str) -> bool {
    CURRENCIES.iter().any(|(symbol, _, _)| *symbol == s)
}

pub(super) fn starts_spaced_chars(s: &[char]) -> bool {
    CURRENCIES.iter().any(|(symbol, _, _)| symbol.chars().eq(s.iter().copied()))
}

pub(super) fn currency_prefix(s: &str) -> bool {
    CURRENCY_PREFIXES.contains(s)
}

fn amount(s: &str, major: &str, minor: &str, leading: bool, shortcut: bool) -> Vec<Reading> {
    let mut out = Vec::new();
    for (word, w) in numeric::integer(s) {
        out.push((format!("{word} {major}"), w));
    }
    for (i, _) in s.char_indices().filter(|(_, c)| matches!(c, ',' | '.')) {
        let a = &s[..i];
        let b = &s[i + 1..];
        for (word, w) in numeric::integer(a) {
            if b == "00" {
                out.push((format!("{word} {major}"), w));
            }
            if b.len() <= 2 && !b.starts_with('0') {
                if let Some(min) = super::cardinal::raw(b) {
                    out.push((format!("{word} {major} en {min} {minor}"), w));
                    if a == "0" {
                        out.push((format!("{min} {minor}"), w - 0.05));
                    }
                }
            }
        }
    }
    if shortcut {
        if let Some(n) = s.strip_suffix('k').or_else(|| s.strip_suffix('K')) {
            for (word, w) in numeric::integer(n) {
                out.push((format!("{word}duizend {major}"), w));
            }
        }
    }
    if leading && shortcut {
        if let Some(n) = s.strip_suffix('m').or_else(|| s.strip_suffix('M')) {
            for (i, _) in n.char_indices().filter(|(_, c)| matches!(c, ',' | '.')) {
                let a = &n[..i];
                let b = &n[i + 1..];
                if b.len() == 1 {
                    if let Some(d) = b.chars().next().and_then(super::cardinal::digit) {
                        for (word, w) in numeric::integer(a) {
                            out.push((format!("{word} komma {d} miljoen {major}"), w));
                        }
                    }
                }
            }
        }
    }
    out
}

fn single(s: &str) -> Vec<Reading> {
    let mut out = Vec::new();
    for &(sym, major, minor) in CURRENCIES {
        if let Some(rest) = s.strip_prefix(sym) {
            let shortcut = !rest.starts_with(' ');
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            out.extend(amount(rest, major, minor, true, shortcut));
        }
        if let Some(rest) = s.strip_suffix(sym) {
            let rest = rest.strip_suffix(' ').unwrap_or(rest);
            out.extend(amount(rest, major, minor, false, true));
        }
    }
    if let Some((money, unit)) = s.rsplit_once('/') {
        let unit = match unit {
            "h" | "uur" | "hr" => Some("uur"),
            "kg" => Some("kilogram"),
            "m" => Some("meter"),
            "km" => Some("kilometer"),
            "maand" => Some("maand"),
            "jaar" => Some("jaar"),
            "dag" => Some("dag"),
            _ => None,
        };
        if let Some(unit) = unit {
            for &(sym, major, _) in CURRENCIES {
                if let Some(v) = money
                    .strip_prefix(sym)
                    .map(|v| v.strip_prefix(' ').unwrap_or(v))
                    .or_else(|| money.strip_suffix(sym).map(|v| v.strip_suffix(' ').unwrap_or(v)))
                {
                    for (word, w) in numeric::integer(v) {
                        out.push((format!("{word} {major} per {unit}"), w));
                    }
                }
            }
        }
    }
    out
}

pub(super) fn parse(s: &str) -> Vec<Reading> {
    let mut out: Vec<_> = single(s).into_iter().map(|(v, w)| (v, w + 1.0)).collect();
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        out.extend(single(rest).into_iter().map(|(v, w)| (format!("min {v}"), w + 0.98)));
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let a = s[..i].trim_end_matches(crate::text::is_nemo_space);
        let b = s[i + c.len_utf8()..].trim_start_matches(crate::text::is_nemo_space);
        for (x, xw) in single(a) {
            for (y, yw) in single(b) {
                out.push((format!("{x} tot {y}"), xw + yw + 0.95));
            }
        }
        for (x, xw) in numeric::integer(a) {
            for (y, yw) in single(b) {
                out.push((format!("{x} tot {y}"), xw + yw + 0.96));
            }
        }
    }
    out
}

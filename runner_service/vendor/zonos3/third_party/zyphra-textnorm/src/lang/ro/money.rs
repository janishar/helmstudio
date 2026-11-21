// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
use super::graph_utils::{Gender, number, parse_number};

pub fn counted(input: &str, singular: &str, plural: &str, gender: Gender) -> Option<String> {
    let n = parse_number(input)?;
    let adjective = match gender {
        Gender::Feminine => Gender::FeminineAdjective,
        Gender::Neuter => Gender::NeuterAdjective,
        _ => Gender::MasculineAdjective,
    };
    let mut words = number(input, if n == 1 { adjective } else { gender })?;
    if n == 1 {
        words.push(' ');
        words.push_str(singular);
    } else {
        if n % 100 == 0 && n > 0 || n % 100 >= 20 {
            words.push_str(" de");
        }
        words.push(' ');
        words.push_str(plural);
    }
    Some(words)
}
const CURRENCIES: &[(&[&str], &str, &str, Gender, bool, bool)] = &[
    (&["lei", "LEI", "leu", "LEU", "RON", "ron", "L", "L."], "leu", "lei", Gender::Masculine, false, true),
    (&["€", "EUR", "eur", "euro", "Euro"], "euro", "euro", Gender::Masculine, true, true),
    (&["$", "USD", "usd", "US$"], "dolar", "dolari", Gender::Masculine, true, true),
    (&["£", "GBP", "gbp"], "liră", "lire", Gender::Feminine, true, true),
    (&["¥", "JPY", "jpy"], "yen", "yeni", Gender::Masculine, true, false),
    (&["CHF", "chf"], "franc elvețian", "franci elvețieni", Gender::Masculine, true, false),
    (&["CAD", "cad", "CA$"], "dolar canadian", "dolari canadieni", Gender::Masculine, true, false),
    (&["AUD", "aud", "A$"], "dolar australian", "dolari australieni", Gender::Masculine, true, false),
    (&["BRL", "brl", "R$"], "real brazilian", "reali brazilieni", Gender::Masculine, true, false),
    (&["MXN", "mxn", "MX$"], "peso mexican", "pesos mexicani", Gender::Masculine, true, false),
    (&["INR", "inr", "₹", "Rs.", "Rs"], "rupie", "rupii", Gender::Feminine, true, false),
    (&["RUB", "rub", "₽"], "rublă", "ruble", Gender::Feminine, true, false),
    (&["KRW", "krw", "₩"], "won", "woni", Gender::Masculine, true, false),
    (&["TRY", "try", "₺"], "liră turcească", "lire turcești", Gender::Feminine, true, false),
    (&["ILS", "ils", "₪"], "șekel", "șekeli", Gender::Masculine, true, false),
    (&["UAH", "uah", "₴"], "hrivnă", "hrivne", Gender::Feminine, true, false),
    (&["VND", "vnd", "₫"], "dong", "dongi", Gender::Masculine, true, false),
    (&["BDT", "bdt", "৳"], "taka", "taka", Gender::Feminine, true, false),
    (&["NGN", "ngn", "₦"], "naira", "naira", Gender::Feminine, true, false),
    (&["PHP", "php", "₱"], "peso", "pesos", Gender::Masculine, true, false),
    (&["THB", "thb", "฿"], "baht", "baht", Gender::Masculine, true, false),
    (&["AMD", "amd", "֏"], "dram", "drami", Gender::Masculine, true, false),
];
use std::collections::HashMap;
use std::sync::LazyLock;

static SUFFIXES: LazyLock<HashMap<char, Vec<(usize, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(usize, &'static str)>> = HashMap::new();
    for (row, &(symbols, _, _, _, _, _)) in CURRENCIES.iter().enumerate() {
        for &symbol in symbols {
            if let Some(c) = symbol.chars().last() {
                index.entry(c).or_default().push((row, symbol));
            }
        }
    }
    index
});

/// Reject spans without a possible currency, rate, or magnitude ending.
pub fn may_match(input: &str) -> bool {
    match input.chars().last() {
        Some(c) if c.is_ascii_digit() || c == '¢' => true,
        Some(c) => {
            ((input.starts_with('$') || input.starts_with('€')) && matches!(c, 'k' | 'K' | 'm' | 'M'))
                || input.ends_with("/h")
                || input.ends_with("/oră")
                || input.ends_with("/ oră")
                || input.ends_with("/kg")
                || input.ends_with("/ kg")
                || SUFFIXES.get(&c).is_some_and(|suffixes| suffixes.iter().any(|(_, s)| input.ends_with(*s)))
        }
        None => false,
    }
}

pub struct Simple<'a> {
    pub number: &'a str,
    row: usize,
    pub serial: bool,
    pub spaced: bool,
}

/// Unambiguous integer + currency suffix, without spelling the amount.
pub fn simple(input: &str) -> Option<Simple<'_>> {
    if super::measure::may_match(input) {
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
        let serial = !spaced && symbol.chars().all(|c| c.is_ascii_alphabetic());
        return Some(Simple { number: n, row, serial, spaced });
    }
    None
}

pub fn render_simple(value: &Simple<'_>) -> Option<String> {
    let (_, singular, plural, gender, _, _) = CURRENCIES[value.row];
    counted(value.number, singular, plural, gender)
}

fn cents(s: &str, lei: bool) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if s == "00" {
        return Some(String::new());
    }
    let n: u8 = s.parse().ok()?;
    let allowed = if lei {
        n <= 20 || [23, 25, 30, 40, 50, 60, 70, 75, 80, 90, 95, 99].contains(&n)
    } else {
        n <= 5 || [10, 15, 20, 23, 25, 30, 50, 95, 99].contains(&n)
    };
    if !allowed && n < 10 {
        return None;
    }
    counted(&n.to_string(), if lei { "ban" } else { "cent" }, if lei { "bani" } else { "cenți" }, Gender::Masculine)
}
fn base(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    let (neg, s) =
        if let Some(v) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { (true, v) } else { (false, s) };
    for &(symbols, sing, plur, gender, prefix, can_neg) in CURRENCIES {
        if neg && !can_neg {
            continue;
        }
        for &sym in symbols {
            let mut variants = Vec::new();
            if let Some(v) = s.strip_suffix(sym) {
                variants.push(v.strip_suffix(' ').unwrap_or(v));
            }
            if prefix {
                if let Some(v) = s.strip_prefix(sym) {
                    variants.push(v.strip_prefix(' ').unwrap_or(v));
                }
            }
            for raw in variants {
                let pre = if neg { "minus " } else { "" };
                if let Some(n) = counted(raw, sing, plur, gender) {
                    out.push((format!("{}{}", pre, n), 0));
                }
                if sing == "leu" || sing == "euro" || sing == "dolar" {
                    for (i, ch) in raw.char_indices() {
                        if ch == '.' || ch == ',' {
                            let major = &raw[..i];
                            let minor = &raw[i + 1..];
                            if let Some(c) = cents(minor, sing == "leu") {
                                if major == "0" {
                                    out.push((format!("{}{}", pre, c), -50_000));
                                }
                                if let Some(m) = counted(major, sing, plur, gender) {
                                    if minor == "00" {
                                        out.push((format!("{}{}", pre, m), 0));
                                    }
                                    out.push((format!("{}{} și {}", pre, m, c), 0));
                                }
                            }
                        }
                    }
                }
            }
        }
    }
    if !neg {
        if let Some(n) = s.strip_suffix('¢') {
            if let Some(n) = counted(n.strip_suffix(' ').unwrap_or(n), "cent", "cenți", Gender::Masculine) {
                out.push((n, 0));
            }
        }
    }
    out
}
pub fn money(s: &str) -> Vec<(String, i64)> {
    let mut out = base(s);
    for (suffix, phrase) in
        [("/h", "pe oră"), ("/ oră", "pe oră"), ("/oră", "pe oră"), ("/kg", "pe kilogram"), ("/ kg", "pe kilogram")]
    {
        if let Some(raw) = s.strip_suffix(suffix) {
            for (value, cost) in base(raw) {
                if CURRENCIES[..3]
                    .iter()
                    .any(|entry| entry.0.iter().any(|sym| raw.starts_with(*sym) || raw.ends_with(*sym)))
                {
                    out.push((format!("{} {}", value, phrase), cost));
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '−') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = s[i + c.len_utf8()..].strip_prefix(' ').unwrap_or(&s[i + c.len_utf8()..]);
            if let Some(a) = a.strip_prefix('$').and_then(|v| number(v, Gender::Masculine)) {
                if let Some(b) = b.strip_prefix('$').and_then(|v| counted(v, "dolar", "dolari", Gender::Masculine)) {
                    out.push((format!("{} până la {}", a, b), 0));
                }
            }
            if let Some(a) = number(a, Gender::Masculine) {
                for &(symbols, sing, plur) in &[
                    (&["€", "EUR", "eur", "euro", "Euro"][..], "euro", "euro"),
                    (&["lei", "LEI", "leu", "LEU", "RON", "ron", "L", "L."][..], "leu", "lei"),
                ] {
                    for &sym in symbols {
                        if let Some(v) = b.strip_suffix(sym) {
                            let v = v.strip_suffix(' ').unwrap_or(v);
                            if let Some(b) = counted(v, sing, plur, Gender::Masculine) {
                                out.push((format!("{} până la {}", a, b), 0));
                            }
                        }
                    }
                }
            }
        }
    }
    for (sym, currency) in [("$", "dolari"), ("€", "euro")] {
        if let Some(v) = s.strip_prefix(sym) {
            if let Some(v) = v.strip_suffix('k').or_else(|| v.strip_suffix('K')) {
                if let Some(n) = number(v, Gender::Masculine) {
                    out.push((format!("{} de mii de {}", n, currency), 0));
                }
            }
        }
    }
    if let Some(v) = s.strip_prefix('$').and_then(|v| v.strip_suffix('M').or_else(|| v.strip_suffix('m'))) {
        for (i, c) in v.char_indices() {
            if c == '.' || c == ',' {
                let a = &v[..i];
                let b = &v[i + 1..];
                if b.len() == 1 {
                    if let (Some(a), Some(b)) = (number(a, Gender::Feminine), number(b, Gender::Masculine)) {
                        out.push((format!("{} virgulă {} milioane de dolari", a, b), 0));
                    }
                }
            }
        }
    }
    out
}

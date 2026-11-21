//! Currency, major/minor amounts, shorthand, rates and ranges.
use super::{cardinal::base_num, decimal::decimal_num};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

static CURRENCY_ENDINGS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for marker in MAJOR.iter().copied().chain(WORLD.iter().map(|(marker, _)| *marker)) {
        if let Some(last) = marker.chars().next_back() {
            index.entry(last).or_default().push(marker);
        }
    }
    index
});

static CURRENCY_PARTS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for marker in
        MAJOR.iter().copied().chain(WORLD.iter().map(|(marker, _)| *marker)).chain(["US$", "CA$", "A$", "R$", "MX$"])
    {
        for (i, ch) in marker.char_indices() {
            prefixes.insert(&marker[..i + ch.len_utf8()]);
        }
    }
    prefixes
});

pub(crate) fn currency_prefix(s: &str) -> bool {
    CURRENCY_PARTS.contains(s)
}
const WORLD: &[(&str, &str)] = &[
    ("CHF", "פרנק שוויצרי"),
    ("CAD", "דולר קנדי"),
    ("AUD", "דולר אוסטרלי"),
    ("INR", "רופי הודי"),
    ("₹", "רופי"),
    ("Rs.", "רופי"),
    ("Rs", "רופי"),
    ("RUB", "רובל"),
    ("₽", "רובל"),
    ("KRW", "וון"),
    ("₩", "וון"),
    ("TRY", "לירה טורקית"),
    ("₺", "לירה טורקית"),
    ("UAH", "הריבניה"),
    ("₴", "הריבניה"),
    ("VND", "דונג"),
    ("₫", "דונג"),
    ("BDT", "טאקה"),
    ("৳", "טאקה"),
    ("NGN", "נאירה"),
    ("₦", "נאירה"),
    ("PHP", "פזו"),
    ("₱", "פזו"),
    ("THB", "באט"),
    ("฿", "באט"),
    ("AMD", "דראם"),
    ("֏", "דראם"),
    ("BRL", "ריאל ברזילאי"),
    ("MXN", "פזו מקסיקני"),
];

const MAJOR: &[&str] = &[
    "₪",
    "ILS",
    "ש\"ח",
    "ש״ח",
    "שקלים",
    "שקל",
    "$",
    "USD",
    "דולר",
    "€",
    "EUR",
    "אירו",
    "יורו",
    "£",
    "GBP",
    "פאונד",
    "לירה שטרלינג",
    "¥",
    "JPY",
    "ין",
];

/// Whether the current span has just completed a currency marker.
pub(crate) fn currency_ending(s: &str, last: char) -> bool {
    !last.is_ascii_digit()
        && last != ' '
        && CURRENCY_ENDINGS.get(&last).is_some_and(|words| words.iter().any(|word| s.ends_with(word)))
}
fn trim(s: &str) -> &str {
    let s = s.strip_prefix(' ').unwrap_or(s);
    s.strip_suffix(' ').unwrap_or(s)
}
fn major(n: &str, k: u8) -> Vec<(String, i64)> {
    let (one, two, singular, plural) = match k {
        0 => ("שקל אחד", "שני שקלים", "שקל", "שקלים"),
        1 => ("דולר אחד", "שני דולרים", "דולר", "דולרים"),
        2 => ("אירו אחד", "שני אירו", "אירו", "אירו"),
        3 => ("פאונד אחד", "שני פאונד", "פאונד", "פאונד"),
        _ => ("", "", "ין", "ין"),
    };
    if k == 4 {
        return base_num(n, false).map(|v| vec![(format!("{v} ין"), 0)]).unwrap_or_default();
    }
    let mut out = Vec::new();
    if n == "1" {
        out.push((one.into(), 0));
    }
    if n == "2" {
        out.push((two.into(), 0));
    }
    if let Ok(i) = n.parse::<u8>() {
        if (3..=10).contains(&i) && n == i.to_string() && k != 3 {
            if let Some(v) = base_num(n, true) {
                out.push((format!("{v} {plural}"), 0));
            }
        }
    }
    if k == 3 {
        if n != "1" && n != "2" {
            if let Some(v) = base_num(n, true) {
                out.push((format!("{v} פאונד"), 0));
            }
        }
        if let Some(v) = base_num(n, false) {
            out.push((format!("{v} פאונד"), 0));
        }
        return out;
    }
    if !matches!(n, "1" | "2" | "3" | "4" | "5" | "6" | "7" | "8" | "9" | "10") {
        for (masc, w) in [(true, -50_000), (false, 500_000)] {
            if let Some(v) = base_num(n, masc) {
                let primary = if k == 0 { plural } else { singular };
                out.push((format!("{v} {primary}"), w + 950_000));
                if k != 2 {
                    let alt = if k == 0 { singular } else { plural };
                    out.push((format!("{v} {alt}"), w + 1_000_000));
                }
            }
        }
    }
    out
}
fn minor(n: &str, k: u8) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    let v = match n.parse::<u8>() {
        Ok(v) if v > 0 && v <= 99 => v,
        _ => return out,
    };
    if n.len() > 2 || n.len() == 2 && v < 10 && !n.starts_with('0') {
        return out;
    }
    let shekel = k == 0;
    let (one, two, plural, singular) = if shekel {
        ("אגורה אחת", "שתי אגורות", "אגורות", "אגורות")
    } else {
        ("סנט אחד", "שני סנטים", "סנטים", "סנט")
    };
    if v == 1 {
        out.push((one.into(), 0));
    } else if v == 2 {
        out.push((two.into(), 0));
    } else if v <= 10 {
        if let Some(a) = base_num(&v.to_string(), !shekel) {
            out.push((format!("{a} {plural}"), 0));
        }
    } else {
        for (m, w) in [(!shekel, 950_000), (shekel, 1_000_000)] {
            if let Some(a) = base_num(n, m) {
                out.push((format!("{a} {singular}"), w));
            }
        }
    }
    out
}
fn amount(s: &str, k: u8) -> Vec<(String, i64)> {
    let mut out = major(s, k);
    if let Some((a, b)) = s.split_once('.') {
        if b == "00" {
            out.extend(major(a, k));
        }
        if k <= 2 {
            if a == "0" {
                out.extend(minor(b, k));
            }
            for (x, xw) in major(a, k) {
                for (y, yw) in minor(b, k) {
                    out.push((format!("{x} ו{y}"), xw + yw));
                }
            }
        }
    }
    out
}
fn base(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (k, lead, trail) in [
        (0, &["₪", "ILS"][..], &["₪", "ILS", "ש\"ח", "ש״ח", "שקלים", "שקל"][..]),
        (1, &["$", "USD", "US$"][..], &["$", "USD", "דולר", "דולרים"][..]),
        (2, &["€", "EUR"][..], &["€", "EUR", "אירו", "יורו"][..]),
        (3, &["£", "GBP"][..], &["£", "GBP", "פאונד", "לירה שטרלינג"][..]),
        (4, &["¥", "JPY"][..], &["¥", "JPY", "ין"][..]),
    ] {
        for sym in lead {
            if let Some(n) = s.strip_prefix(sym) {
                out.extend(amount(n.strip_prefix(' ').unwrap_or(n), k));
            }
        }
        for sym in trail {
            if let Some(n) = s.strip_suffix(sym) {
                out.extend(amount(n.strip_suffix(' ').unwrap_or(n), k));
            }
        }
    }
    for &(sym, name) in WORLD {
        if let Some(n) = s.strip_suffix(sym) {
            if let Some(n) = base_num(n.strip_suffix(' ').unwrap_or(n), false) {
                out.push((format!("{n} {name}"), -100_000));
            }
        }
        if ["₹", "₽", "₩", "₺", "₴", "₫", "৳", "₦", "₱", "฿", "֏", "Rs."].contains(&sym) {
            if let Some(n) = s.strip_prefix(sym) {
                if let Some(n) = base_num(n.strip_prefix(' ').unwrap_or(n), false) {
                    out.push((format!("{n} {name}"), 0));
                }
            }
        }
    }
    for (sym, name) in [("CA$", "דולר קנדי"), ("A$", "דולר אוסטרלי"), ("R$", "ריאל ברזילאי"), ("MX$", "פזו מקסיקני")]
    {
        if let Some(n) = s.strip_prefix(sym) {
            if let Some(n) = base_num(n.strip_prefix(' ').unwrap_or(n), false) {
                out.push((format!("{n} {name}"), 0));
            }
        }
    }
    out
}
fn shorthand(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (sym, unit) in [("$", "דולר"), ("€", "אירו"), ("₪", "שקלים")] {
        if let Some(n) = s.strip_prefix(sym) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            if let Some(v) = n.strip_suffix('k').or_else(|| n.strip_suffix('K')) {
                if let Some(v) = base_num(v, false) {
                    out.push((format!("{v} אלף {unit}"), 0));
                }
            }
            if sym != "₪" {
                if let Some(v) = n.strip_suffix('M').or_else(|| n.strip_suffix('m')) {
                    for (v, w) in decimal_num(v) {
                        out.push((format!("{v} מיליון {unit}"), w));
                    }
                }
            }
        }
    }
    out
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (v, w) in base(s) {
        out.push((v, w + 1_920_000));
    }
    for (v, w) in shorthand(s) {
        out.push((v, w + 1_870_000));
    }
    if let Some(n) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        for (v, w) in base(n).into_iter().chain(shorthand(n)) {
            out.push((format!("מינוס {v}"), w + 1_820_000));
        }
    }
    for (suffix, name) in [
        ("/h", "לשעה"),
        ("/hr", "לשעה"),
        ("/kg", "לקילוגרם"),
        ("/m", "למטר"),
        ("/km", "לקילומטר"),
        ("/day", "ליום"),
        ("/month", "לחודש"),
        ("/year", "לשנה"),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            for (v, w) in base(n).into_iter().chain(shorthand(n)) {
                out.push((format!("{v} {name}"), w + 1_840_000));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–' | '—' | '־') || i == 0 {
            continue;
        }
        let a = trim(&s[..i]);
        let b = trim(&s[i + c.len_utf8()..]);
        for (x, xw) in base(a) {
            for (y, yw) in base(b) {
                out.push((format!("{x} עד {y}"), xw + yw + 1_860_000));
            }
        }
        for (symbol, name) in [("$", "דולר"), ("€", "אירו"), ("₪", "שקלים")] {
            if let Some(x) = a.strip_prefix(symbol) {
                let y = b.strip_prefix(symbol).unwrap_or(b);
                if let (Some(x), Some(y)) = (base_num(x, true), base_num(y, true)) {
                    out.push((format!("{x} עד {y} {name}"), 1_860_000));
                }
            }
            if symbol == "₪" {
                if let Some(y) = b.strip_prefix(symbol) {
                    if let (Some(x), Some(y)) = (base_num(a, true), base_num(y, true)) {
                        out.push((format!("{x} עד {y} {name}"), 1_860_000));
                    }
                }
            }
        }
    }
    out
}

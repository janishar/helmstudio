use super::graph_utils::{Gender, cardinal};
use super::numeric::number;
use super::ordinal::ordinal;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

static WHITELIST: LazyLock<HashMap<&'static str, Vec<&'static str>>> = LazyLock::new(|| {
    let mut rows = HashMap::<&'static str, Vec<&'static str>>::new();
    for line in include_str!("../../../data/bg/whitelist.tsv").lines() {
        if !line.starts_with('#') {
            if let Some((key, value)) = line.split_once('\t') {
                rows.entry(key).or_default().push(value);
            }
        }
    }
    rows
});

const NUMBER_PREFIXES: &[&str] = &["No.", "No", "no.", "no", "№", "ном.", "Номер", "номер", "§", "©"];
const SYMBOLS: &[(&str, &str)] = &[
    ("&", "и"),
    ("©", "авторско право"),
    ("®", "регистрирана марка"),
    ("™", "търговска марка"),
    ("§", "параграф"),
    ("°", "градус"),
    ("%", "процент"),
    ("‰", "промил"),
];
const POWERS: &[(&str, &str)] = &[
    ("x^2", "хикс на квадрат"),
    ("y^3", "игрек на куб"),
    ("2^3", "две на трета степен"),
    ("3^2", "три на квадрат"),
    ("10^6", "десет на шеста степен"),
];
static WHITELIST_PREFIXES: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = HashMap::<char, Vec<&'static str>>::new();
    for &key in WHITELIST.keys().chain(SYMBOLS.iter().map(|(key, _)| key)).chain(POWERS.iter().map(|(key, _)| key)) {
        if let Some(first) = key.chars().next() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});

/// Prefix recognizer for fixed whitelist keys and productive number prefixes.
pub(crate) fn whitelist_prefix(s: &str) -> bool {
    let Some(first) = s.chars().next() else { return true };
    WHITELIST_PREFIXES.get(&first).is_some_and(|keys| keys.iter().any(|key| key.starts_with(s)))
        || (matches!(first, 'N' | 'n' | '№' | 'н' | 'Н' | '§' | '©')
            && NUMBER_PREFIXES.iter().any(|&prefix| prefix.starts_with(s) || s.starts_with(prefix)))
}

static MULTIWORD_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for key in WHITELIST.keys() {
        if let Some((first, _)) = key.split_once(' ') {
            prefixes.insert(first);
        }
    }
    prefixes.extend([
        "No.",
        "No",
        "no.",
        "no",
        "№",
        "ном.",
        "Номер",
        "номер",
        "§",
        "©",
        "Глава",
        "глава",
        "Част",
        "част",
        "Том",
        "том",
        "Симеон",
        "Борис",
        "Петър",
        "Иван",
        "Калоян",
        "Самуил",
        "Александър",
        "Николай",
        "Хенри",
        "Луи",
        "Елизабет",
    ]);
    prefixes.extend(ROMANS);
    prefixes
});

/// Whether a nonnumeric first chunk can start a source rule containing spaces.
pub(crate) fn multiword_prefix(s: &str) -> bool {
    MULTIWORD_PREFIXES.contains(s)
}

pub(crate) fn whitelist(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(values) = WHITELIST.get(s) {
        for value in values {
            out.push(((*value).to_owned(), 1.0));
        }
    }
    for &(a, b) in SYMBOLS {
        if s == a {
            out.push((b.to_owned(), 1.05));
        }
    }
    for &(a, b) in POWERS {
        if s == a {
            out.push((b.to_owned(), 0.97));
        }
    }
    for &a in &NUMBER_PREFIXES[..NUMBER_PREFIXES.len() - 1] {
        if let Some(n) = s.strip_prefix(a) {
            if let Some(v) = number(n.strip_prefix(' ').unwrap_or(n), Gender::Neuter) {
                out.push((
                    format!("{} {v}", if a == "§" { "параграф" } else { "номер" }),
                    if a == "§" { 0.95 } else { 0.96 },
                ));
            }
        }
    }
    if let Some(n) = s.strip_prefix('©') {
        if let Some(v) = ordinal(n.strip_prefix(' ').unwrap_or(n), Gender::Feminine) {
            out.push((format!("авторско право {v}"), 0.95));
        }
    }
    out
}
const ROMANS: [&str; 22] = [
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI", "XXII",
];
fn roman(s: &str, g: Gender) -> Option<String> {
    let n = ROMANS.iter().position(|x| *x == s)? + 1;
    ordinal(&n.to_string(), g)
}
pub(crate) fn romans(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (a, b) in [("XL", "X L"), ("MIX", "M I X"), ("CD", "C D")] {
        if s == a {
            out.push(b.to_owned());
        }
    }
    for prefix in [
        "Глава",
        "глава",
        "Част",
        "част",
        "Том",
        "том",
        "Симеон",
        "Борис",
        "Петър",
        "Иван Асен",
        "Калоян",
        "Самуил",
        "Александър",
        "Николай",
        "Хенри",
        "Луи",
        "Елизабет",
    ] {
        if let Some(n) = s.strip_prefix(prefix) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            let g = if matches!(prefix, "Глава" | "глава" | "Част" | "част") {
                Gender::Feminine
            } else {
                Gender::Masculine
            };
            if let Some(v) = roman(n, g) {
                out.push(format!("{prefix} {v}"));
            }
        }
    }
    for suffix in ["век", "в."] {
        if let Some(n) = s.strip_suffix(suffix) {
            if let Some(v) = roman(n.strip_suffix(' ').unwrap_or(n), Gender::Masculine) {
                out.push(format!("{v} {suffix}"));
            }
        }
    }
    out
}

pub(crate) fn telephone(s: &str) -> Option<String> {
    let (prefix, s) = if let Some(v) = s.strip_prefix('+') {
        if let Some(t) = v.strip_prefix("1 ").or_else(|| v.strip_prefix("1-")).or_else(|| v.strip_prefix("1.")) {
            if t.starts_with('(') { ("плюс едно ", t) } else { ("плюс ", v) }
        } else {
            ("плюс ", v)
        }
    } else {
        ("", s)
    };
    let (area, rest) = if s.starts_with('(') {
        if prefix == "плюс " {
            return None;
        }
        let end = s.find(')')?;
        let a = &s[1..end];
        let rest = &s[end + 1..];
        (Some(a), rest.strip_prefix(' ').unwrap_or(rest))
    } else {
        (None, s)
    };
    let parts = area.into_iter().chain(rest.split(|c| matches!(c, ' ' | '-' | '.')));
    let count = parts.clone().count();
    if area.is_some() {
        if count != 3 {
            return None;
        }
    } else if count != 4 && !(prefix != "" && (4..=6).contains(&count)) {
        return None;
    }
    if !parts.clone().all(|p| !p.is_empty() && p.len() <= 4 && p.bytes().all(|b| b.is_ascii_digit())) {
        return None;
    }
    let mut result = String::from(prefix);
    for (i, p) in parts.enumerate() {
        if i != 0 {
            result.push(' ');
        }
        let value =
            if p.len() > 1 && p.starts_with('0') { super::numeric::digits(p)? } else { cardinal(p, Gender::Neuter)? };
        result.push_str(&value);
    }
    Some(result)
}

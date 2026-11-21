// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::{
    cardinal::{Gender, graph},
    electronic::letter,
};
const ITEMS: &[(&str, &str)] = &[
    ("і т. д.", "і так далі"),
    ("і т.д.", "і так далі"),
    ("і т. п.", "і тому подібне"),
    ("і т.п.", "і тому подібне"),
    ("тощо", "тощо"),
    ("напр.", "наприклад"),
    ("див.", "дивіться"),
    ("вул.", "вулиця"),
    ("буд.", "будинок"),
    ("кв.", "квартира"),
    ("д-р", "доктор"),
    ("д-р.", "доктор"),
    ("проф.", "професор"),
    ("проф", "професор"),
    ("пан", "пан"),
    ("пані", "пані"),
    ("г-н", "пан"),
    ("гр-н", "громадянин"),
];
pub(super) fn plain_match(s: &str) -> bool {
    ITEMS.iter().any(|(key, _)| *key == s) || matches!(s, "XL" | "CD" | "MIX")
}
pub(super) fn plain_match_chars(s: &[char]) -> bool {
    ITEMS.iter().any(|(key, _)| key.chars().eq(s.iter().copied()))
        || ["XL", "CD", "MIX"].iter().any(|key| key.chars().eq(s.iter().copied()))
}
pub(super) fn whitelist(s: &str) -> Vec<(String, i32)> {
    let mut out = Vec::new();
    for (k, v) in ITEMS {
        if *k == s {
            out.push(((*v).to_owned(), 5));
        }
    }
    for (prefix, word, weight) in
        [("№", "номер", 2), ("§", "параграф", 2), ("©", "копірайт", 2), ("~", "приблизно", 3), ("±", "плюс-мінус", 3)]
    {
        if let Some(n) = s.strip_prefix(prefix) {
            let n = n.strip_prefix(' ').unwrap_or(n);
            if n.bytes().all(|b| b.is_ascii_digit()) {
                if let Some(n) = graph(n, Gender::Masculine, false) {
                    out.push((format!("{word} {n}"), weight));
                }
            }
        }
    }
    if let Some(n) = s.strip_suffix('¢') {
        let n = n.strip_suffix(' ').unwrap_or(n);
        if n.bytes().all(|b| b.is_ascii_digit()) {
            if let Some(n) = graph(n, Gender::Masculine, false) {
                out.push((format!("{n} центів"), 3));
            }
        }
    }
    if s == "&" {
        out.push(("і".into(), 10));
    }
    if let Some((a, b)) = s.split_once('&') {
        let a = a.strip_suffix(' ').unwrap_or(a);
        let b = b.strip_prefix(' ').unwrap_or(b);
        if a.chars().count() == 1 && b.chars().count() == 1 {
            if let (Some(a), Some(b)) = (letter(a.chars().next().unwrap()), letter(b.chars().next().unwrap())) {
                out.push((format!("{a} і {b}"), 1));
            }
        }
    }
    if s == "г-н" {
        out.push(("господин".into(), 4));
    }
    if ["д-р", "д-р.", "пан"].contains(&s) {
        out.push(((if s == "пан" { "пан" } else { "доктор" }).into(), 4));
    }
    out
}

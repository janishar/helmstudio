// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::number_names::{Case, Gender, cardinal, ordinal};
use super::numeric::digits;
use std::collections::HashSet;
use std::sync::LazyLock;
static WHITELIST_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    let keys = include_str!("../../../data/pl/whitelist/abbreviations.tsv")
        .lines()
        .filter_map(|line| line.split_once('\t').map(|(key, _)| key))
        .chain(["&", "©", "§", "+", "=", "×", "÷"]);
    for key in keys {
        for end in (1..=key.len()).filter(|&i| key.is_char_boundary(i)) {
            prefixes.insert(&key[..end]);
        }
    }
    prefixes
});
pub fn whitelist_prefix(s: &str) -> bool {
    WHITELIST_PREFIXES.contains(s)
}
const M: Gender = Gender::Masculine;
const F: Gender = Gender::Feminine;
const N: Case = Case::Nominative;
const G: Case = Case::Genitive;
pub fn whitelist(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for l in include_str!("../../../data/pl/whitelist/abbreviations.tsv").lines() {
        if let Some((a, b)) = l.split_once('\t') {
            if s == a {
                out.push((b.to_owned(), 0.0));
            }
        }
    }
    for (a, b) in [
        ("&", "i"),
        ("©", "copyright"),
        ("§", "paragraf"),
        ("+", "plus"),
        ("=", "równa się"),
        ("×", "razy"),
        ("÷", "podzielić przez"),
    ] {
        if s == a {
            out.push((b.into(), 0.0));
        }
    }
    out
}
const ROMANS: &[&str] = &[
    "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
    "XVIII", "XIX", "XX", "XXI", "XXII",
];
const ROMAN_CONTEXTS: &[(&[&str], Gender, bool, usize)] = &[
    (
        &[
            "Rozdział",
            "rozdział",
            "Rozdz.",
            "rozdz.",
            "Tom",
            "tom",
            "Akt",
            "akt",
            "Wiek",
            "wiek",
            "w.",
            "Artykuł",
            "artykuł",
            "art.",
            "Paragraf",
            "paragraf",
            "Punkt",
            "punkt",
            "pkt",
            "Stopień",
            "stopień",
        ],
        M,
        false,
        22,
    ),
    (&["Część", "część", "Scena", "scena", "Klasa", "klasa", "Księga", "księga"], F, false, 22),
    (
        &[
            "Jan Paweł",
            "Jan",
            "Benedykt",
            "Franciszek",
            "Pius",
            "Leon",
            "Grzegorz",
            "Zygmunt",
            "Zygmunt August",
            "Kazimierz",
            "Władysław",
            "Bolesław",
            "Stefan",
            "Stanisław",
            "Stanisław August",
            "Henryk",
            "Ludwik",
            "Karol",
            "Edward",
            "Wilhelm",
            "Aleksander",
            "Mikołaj",
            "Piotr",
            "Napoleon",
            "Filip",
            "Jerzy",
            "Ryszard",
            "Papież",
            "Król",
            "Cesarz",
        ],
        M,
        true,
        16,
    ),
    (&["Elżbieta", "Katarzyna", "Maria", "Królowa"], F, true, 3),
];
static ROMAN_PREFIXES: LazyLock<[Vec<&'static str>; 128]> = LazyLock::new(|| {
    let mut index: [Vec<&'static str>; 128] = std::array::from_fn(|_| Vec::new());
    for &(words, _, _, _) in ROMAN_CONTEXTS {
        for &word in words {
            index[word.as_bytes()[0] as usize].push(word);
        }
    }
    for &word in ROMANS {
        index[word.as_bytes()[0] as usize].push(word);
    }
    index
});

/// Every Roman reading begins with a listed context or a listed numeral.
/// After that lexical item and a space, defer to the complete recognizer.
pub fn roman_prefix(s: &str) -> bool {
    let Some(&first) = s.as_bytes().first() else {
        return false;
    };
    let Some(words) = ROMAN_PREFIXES.get(first as usize) else {
        return false;
    };
    words.iter().any(|word| word.starts_with(s) || s.strip_prefix(*word).is_some_and(|rest| rest.starts_with(' ')))
}

pub fn roman(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (i, r) in ROMANS.iter().enumerate() {
        for &(words, gender, title, max) in ROMAN_CONTEXTS {
            if i >= max {
                continue;
            }
            for word in words {
                if s.strip_prefix(*word).and_then(|tail| tail.strip_prefix(' ')) == Some(*r) {
                    if let Some(mut o) = ordinal(&(i + 1).to_string(), gender, N) {
                        if title {
                            if let Some(c) = o.chars().next() {
                                o.replace_range(..c.len_utf8(), &c.to_uppercase().to_string());
                            }
                        }
                        out.push((format!("{word} {o}"), 0.0));
                    }
                }
            }
        }
        for (suffix, gender, case) in
            [(" wiek", M, N), (" wieku", M, G), (" wojna światowa", F, N), (" wojny światowej", F, N)]
        {
            if s.strip_prefix(*r) == Some(suffix) {
                if let Some(mut o) = ordinal(&(i + 1).to_string(), gender, case) {
                    if suffix == " wojny światowej" && o.ends_with('a') {
                        o.pop();
                        o.push_str("ej");
                    }
                    out.push((format!("{o}{suffix}"), 0.0));
                }
            }
        }
    }
    out
}
pub fn telephone(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (body, country) = if let Some(rest) = s.strip_prefix('+') {
        let Some(pos) = rest.find([' ', '-']) else {
            return out;
        };
        let c = &rest[..pos];
        if c.is_empty() || c.len() > 3 || !c.bytes().all(|b| b.is_ascii_digit()) {
            return out;
        }
        (&rest[pos + 1..], Some(c))
    } else {
        (s, None)
    };
    let plain: String = body.chars().filter(|c| c.is_ascii_digit()).collect();
    if plain.len() < 7 {
        return out;
    }
    let sep_ok = body.chars().all(|c| c.is_ascii_digit() || " -.()".contains(c));
    if !sep_ok {
        return out;
    }
    let date_like = |x: &str| -> bool {
        for sep in ['-', '.', '/'] {
            let p: Vec<_> = x.split(sep).collect();
            if p.len() == 3
                && p.iter().all(|v| v.bytes().all(|b| b.is_ascii_digit()))
                && ((p[0].len() == 2 && p[1].len() == 2 && p[2].len() == 4)
                    || (sep != '/' && p[0].len() == 4 && p[1].len() == 2 && p[2].len() == 2))
            {
                return true;
            }
        }
        false
    };
    if date_like(s) {
        return out;
    }
    let rest = if let Some(a) = body.strip_prefix('(') {
        let Some((area, rest)) = a.split_once(')') else {
            return out;
        };
        if area.is_empty() || !area.bytes().all(|b| b.is_ascii_digit()) {
            return out;
        }
        rest.strip_prefix(' ').unwrap_or(rest)
    } else {
        body
    };
    if rest.is_empty() || !rest.split([' ', '-', '.']).all(|g| !g.is_empty() && g.bytes().all(|b| b.is_ascii_digit())) {
        return out;
    }
    let mut w = String::new();
    if let Some(c) = country {
        if let Some(d) = digits(c) {
            w = format!("plus {d} ");
        }
    }
    if let Some(d) = digits(&plain) {
        w.push_str(&d);
        out.push((w, 0.0));
    }
    out
}
fn spell_char(c: char) -> Option<String> {
    if c.is_ascii_alphabetic() {
        Some(c.to_string())
    } else if c.is_ascii_digit() {
        cardinal(&c.to_string(), M, N)
    } else {
        Some(
            match c {
                '.' => "kropka",
                '@' => "małpa",
                '/' => "ukośnik",
                ':' => "dwukropek",
                '-' => "myślnik",
                '_' => "podkreślenie",
                _ => return None,
            }
            .into(),
        )
    }
}
fn spelled(s: &str) -> Option<String> {
    s.chars().map(spell_char).collect::<Option<Vec<_>>>().map(|v| v.join(" "))
}
const TLDS: &[(&str, &str)] = &[
    ("com", "c o m"),
    ("org", "o r g"),
    ("net", "n e t"),
    ("edu", "e d u"),
    ("gov", "g o v"),
    ("pl", "p l"),
    ("eu", "e u"),
    ("dev", "dev"),
    ("ai", "a i"),
    ("io", "i o"),
    ("app", "app"),
    ("info", "info"),
    ("de", "d e"),
    ("uk", "u k"),
    ("fr", "f r"),
];
pub fn electronic(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let mut octets = s.split('.');
    if let (Some(a), Some(b), Some(c), Some(d), None) =
        (octets.next(), octets.next(), octets.next(), octets.next(), octets.next())
    {
        let parts = [a, b, c, d];
        if parts.iter().all(|x| !x.is_empty() && x.len() <= 3) {
            if let Some(v) = parts.iter().map(|x| cardinal(x, M, N)).collect::<Option<Vec<_>>>() {
                out.push((v.join(" kropka "), 0.0));
            }
        }
    }
    if let Some((u, d)) = s.split_once('@') {
        for &(t, w) in TLDS {
            if let Some(dom) = d.strip_suffix(t).and_then(|v| v.strip_suffix('.')) {
                if !u.is_empty()
                    && !dom.is_empty()
                    && u.chars().all(|c| c.is_ascii_alphanumeric() || "._-".contains(c))
                    && dom.chars().all(|c| c.is_ascii_alphanumeric() || c == '-')
                {
                    if let (Some(u), Some(d)) = (spelled(u), spelled(dom)) {
                        out.push((format!("{u} małpa {d} kropka {w}"), 0.0));
                    }
                }
            }
        }
    }
    for &(t, w) in TLDS {
        if let Some((pos, _)) = s.match_indices(t).find(|(i, _)| *i > 0 && s.as_bytes()[*i - 1] == b'.') {
            let domain = &s[..pos - 1];
            let rest = &s[pos + t.len()..];
            if !domain.is_empty() && domain.chars().all(|c| c.is_ascii_alphanumeric() || c == '-') {
                if let Some(extra) = url_rest(rest) {
                    out.push((format!("{domain} kropka {w}{extra}"), 0.0));
                }
            }
        }
    }
    for (protocol, word) in [
        ("http://", "h t t p dwukropek ukośnik ukośnik "),
        ("https://", "h t t p s dwukropek ukośnik ukośnik "),
        ("www.", "w w w kropka "),
    ] {
        if let Some(rest) = s.strip_prefix(protocol) {
            let (rest, www) = if let Some(r) = rest.strip_prefix("www.") { (r, "w w w kropka ") } else { (rest, "") };
            let end = rest.find([':', '/']).unwrap_or(rest.len());
            let host = &rest[..end];
            if !host.is_empty() && host.split('.').all(|x| !x.is_empty() && x.chars().all(|c| c.is_ascii_alphabetic()))
            {
                if let Some(extra) = url_rest(&rest[end..]) {
                    out.push((format!("{word}{www}{}{extra}", host.replace('.', " kropka ")), 0.0));
                }
            }
        }
    }
    out
}
fn url_rest(s: &str) -> Option<String> {
    if s.is_empty() {
        return Some(String::new());
    }
    let mut rest = s;
    let mut out = String::new();
    if let Some(r) = rest.strip_prefix(':') {
        let n = r.find('/').unwrap_or(r.len());
        let p = &r[..n];
        if p.is_empty() || p.len() > 5 || !p.bytes().all(|c| c.is_ascii_digit()) {
            return None;
        }
        out.push_str(" dwukropek ");
        out.push_str(&digits(p)?);
        rest = &r[n..];
    }
    if !rest.is_empty() {
        if !rest.starts_with('/') || rest.len() == 1 {
            return None;
        }
        out.push(' ');
        out.push_str(&spelled(rest)?);
    }
    Some(out)
}

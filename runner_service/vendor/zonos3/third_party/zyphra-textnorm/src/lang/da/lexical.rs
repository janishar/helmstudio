// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::graph_utils::digit;
use super::numeric::{digits, number, ordinal};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;
const WHITELIST: &str = include_str!("../../../data/da/whitelist.tsv");
const ROMAN_CARD: &str = include_str!("../../../data/da/roman_cardinal.tsv");
const ROMAN_ORD: &str = include_str!("../../../data/da/roman_ordinal.tsv");
const NAMES: &str = include_str!("../../../data/da/monarch_names.tsv");
const KEYWORDS: &str = include_str!("../../../data/da/keywords.tsv");
fn table_index(table: &'static str) -> HashMap<&'static str, &'static str> {
    let mut index = HashMap::new();
    for (key, value) in table.lines().filter_map(|l| l.split_once('\t')) {
        index.entry(key).or_insert(value);
    }
    index
}
static WHITELIST_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| table_index(WHITELIST));
static ROMAN_CARD_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| table_index(ROMAN_CARD));
static ROMAN_ORD_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| table_index(ROMAN_ORD));
static MULTIWORD_INITIALS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut result = HashSet::new();
    for row in WHITELIST.lines() {
        if let Some((key, _)) = row.split_once('\t') {
            if let Some((first, _)) = key.split_once(' ') {
                result.insert(first);
            }
        }
    }
    for name in NAMES.lines() {
        result.insert(name.split(' ').next().unwrap_or(name));
    }
    for row in KEYWORDS.lines() {
        if let Some((key, _)) = row.split_once('\t') {
            result.insert(key);
        }
    }
    result
});
static INTERNAL_WORDS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut words = HashSet::new();
    for table in [
        WHITELIST,
        KEYWORDS,
        NAMES,
        include_str!("../../../data/da/units.tsv"),
        include_str!("../../../data/da/currencies.tsv"),
    ] {
        for row in table.lines() {
            let key = row.split('\t').next().unwrap_or("");
            words.extend(key.split(' '));
        }
    }
    words.extend([
        "januar",
        "jan.",
        "jan",
        "februar",
        "feb.",
        "feb",
        "marts",
        "mar.",
        "mar",
        "april",
        "apr.",
        "apr",
        "maj",
        "juni",
        "jun.",
        "jun",
        "juli",
        "jul.",
        "jul",
        "august",
        "aug.",
        "aug",
        "september",
        "sep.",
        "sep",
        "sept.",
        "sept",
        "oktober",
        "okt.",
        "okt",
        "november",
        "nov.",
        "nov",
        "december",
        "dec.",
        "dec",
        "million",
        "millioner",
        "mio.",
        "mio",
        "mn",
        "milliard",
        "milliarder",
        "mia.",
        "mia",
        "md",
        "billion",
        "billioner",
        "bn",
        "f.Kr.",
        "f.",
        "Kr.",
        "f.v.t.",
        "BC",
        "B.C.",
        "BCE",
        "e.Kr.",
        "e.",
        "v.t.",
        "AD",
        "A.D.",
        "CE",
        "UTC",
        "GMT",
        "CET",
        "CEST",
        "EST",
        "EDT",
        "PST",
        "PDT",
        "klokken",
        "Klokken",
        "kl.",
        "Kl.",
        "kl",
        "Kl",
        "century",
        "Century",
        "århundrede",
        "Århundrede",
        "årh.",
        "årh",
        "det",
        "Det",
        "iOS",
        "ios",
        "ver.",
        "version",
    ]);
    words
});
/// Complete alphabetic pieces allowed inside a multiword classified span.
pub fn internal_word(word: &str) -> bool {
    if !word.chars().any(char::is_alphabetic) || word.chars().any(|c| c.is_ascii_digit()) {
        return true;
    }
    let w = word.strip_suffix(',').unwrap_or(word);
    INTERNAL_WORDS.contains(w)
        || (!w.is_empty() && w.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')))
        || w.strip_suffix('.')
            .is_some_and(|r| !r.is_empty() && r.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')))
}
static INTERNAL_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for &word in INTERNAL_WORDS.iter() {
        for (i, _) in word.char_indices().skip(1) {
            prefixes.insert(&word[..i]);
        }
        prefixes.insert(word);
    }
    prefixes
});
pub fn internal_prefix(piece: &str) -> bool {
    if !piece.chars().any(char::is_alphabetic) || piece.chars().any(|c| c.is_ascii_digit()) {
        return true;
    }
    let p = piece.strip_suffix(',').unwrap_or(piece);
    INTERNAL_PREFIXES.contains(p)
        || (!p.is_empty() && p.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')))
        || p.strip_suffix('.')
            .is_some_and(|r| !r.is_empty() && r.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')))
}
fn lookup(t: &'static str, key: &str) -> Option<&'static str> {
    let index = if std::ptr::eq(t, WHITELIST) {
        &*WHITELIST_INDEX
    } else if std::ptr::eq(t, ROMAN_CARD) {
        &*ROMAN_CARD_INDEX
    } else {
        &*ROMAN_ORD_INDEX
    };
    index.get(key).copied()
}
pub fn plain_whitelist(s: &str) -> bool {
    WHITELIST_INDEX.contains_key(s)
}
pub fn whitelist_value(s: &str) -> Option<&'static str> {
    WHITELIST_INDEX.get(s).copied()
}
/// Whether a letter-starting span can continue across its first whitespace.
pub fn multiword_start(first: &str) -> bool {
    if MULTIWORD_INITIALS.contains(first) {
        return true;
    }
    if [
        "Nr.", "nr.", "Nr", "nr", "No.", "no.", "No", "no", "s.", "S.", "kap.", "Kap.", "fig.", "Fig.", "tab.", "Tab.",
        "kl.", "Kl.", "kl", "Kl", "klokken", "Klokken",
    ]
    .iter()
    .any(|p| first.starts_with(*p))
    {
        return true;
    }
    matches!(
        first,
        "Nr."
            | "nr."
            | "Nr"
            | "nr"
            | "No."
            | "no."
            | "No"
            | "no"
            | "s."
            | "S."
            | "kap."
            | "Kap."
            | "fig."
            | "Fig."
            | "tab."
            | "Tab."
            | "iOS"
            | "ios"
            | "ver."
            | "version"
            | "century"
            | "Century"
            | "århundrede"
            | "Århundrede"
            | "årh."
            | "årh"
            | "det"
            | "Det"
            | "kl."
            | "Kl."
            | "kl"
            | "Kl"
            | "klokken"
            | "Klokken"
    ) || (!first.is_empty() && first.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')))
        || first
            .strip_suffix('.')
            .is_some_and(|r| !r.is_empty() && r.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'M')))
}
fn letter(c: char) -> bool {
    c.is_ascii_alphabetic() || "æøåÆØÅ".contains(c)
}
fn letters(s: &str) -> Option<String> {
    if s.is_empty() || !s.chars().all(letter) {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 2);
    for c in s.chars() {
        if !out.is_empty() {
            out.push(' ');
        }
        out.extend(c.to_lowercase());
    }
    Some(out)
}
fn identifier_digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.chars().all(|c| c.is_ascii_digit() || ('₀'..='₉').contains(&c)) {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 6);
    for c in s.chars() {
        let d = if c.is_ascii_digit() {
            c
        } else if ('₀'..='₉').contains(&c) {
            char::from_u32('0' as u32 + c as u32 - '₀' as u32)?
        } else {
            return None;
        };
        let word = digit(d)?;
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(word);
    }
    if out.is_empty() { None } else { Some(out) }
}
pub fn whitelist(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(x) = lookup(WHITELIST, s) {
        out.push(x.into());
    }
    if !s.chars().any(|c| c.is_ascii_digit() || ('₀'..='₉').contains(&c)) {
        return out;
    }
    for (k, w) in [
        ("Nr.", "nummer"),
        ("nr.", "nummer"),
        ("Nr", "nummer"),
        ("nr", "nummer"),
        ("No.", "nummer"),
        ("no.", "nummer"),
        ("No", "nummer"),
        ("no", "nummer"),
        ("s.", "side"),
        ("S.", "side"),
        ("kap.", "kapitel"),
        ("Kap.", "kapitel"),
        ("fig.", "figur"),
        ("Fig.", "figur"),
        ("tab.", "tabel"),
        ("Tab.", "tabel"),
        ("§§", "paragraffer"),
        ("§", "paragraf"),
    ] {
        if let Some(a) = s.strip_prefix(k) {
            let a = a.strip_prefix(' ').unwrap_or(a);
            if let Some(n) = number(a, false) {
                out.push(format!("{w} {n}"));
            }
            for (i, c) in a.char_indices().skip(1) {
                if matches!(c, '-' | '–' | '—') {
                    let x = a[..i].trim_end_matches(' ');
                    let y = a[i + c.len_utf8()..].trim_start_matches(' ');
                    if let (Some(x), Some(y)) = (number(x, false), number(y, false)) {
                        out.push(format!("{w} {x} til {y}"));
                    }
                }
            }
        }
    }
    if let Some(a) = s.strip_suffix('¢') {
        if let Some(n) = number(a.strip_suffix(' ').unwrap_or(a), true) {
            out.push(format!("{n} cent"));
        }
    }
    if let Some((first, rest)) = s.split_once('-') {
        if let Some((middle, last)) = rest.split_once('-') {
            if !last.contains('-') {
                if let (Some(a), Some(b), Some(c)) = (letters(first), identifier_digits(middle), letters(last)) {
                    out.push(format!("{a} {b} {c}"));
                }
            }
        }
    }
    if s.chars().any(letter) {
        for (i, _) in s.char_indices().skip(1) {
            let a = &s[..i];
            let b = &s[i..];
            if b.chars().next().is_some_and(|c| c.is_ascii_digit() || ('₀'..='₉').contains(&c)) {
                if let Some(l) = letters(a) {
                    for (j, _) in b.char_indices().skip(1).chain(std::iter::once((b.len(), ' '))) {
                        if let Some(d) = identifier_digits(&b[..j]) {
                            if j == b.len() {
                                out.push(format!("{l} {d}"));
                            } else if let Some(t) = letters(&b[j..]) {
                                out.push(format!("{l} {d} {t}"));
                            }
                        }
                    }
                }
            }
            if ["G", "g", "K", "k"].contains(&b) {
                if let Some(d) = identifier_digits(a) {
                    out.push(format!("{d} {}", b.to_lowercase()));
                }
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        let base = number(a, false).or_else(|| {
            letter(a.chars().next().unwrap_or(' ')).then(|| a.to_lowercase()).filter(|_| a.chars().count() == 1)
        });
        if (2..=10).contains(&b.parse::<u32>().unwrap_or(0)) {
            if let (Some(a), Some(b)) = (base, ordinal(b)) {
                out.push(format!("{a} i {b}"));
            }
        }
    }
    for (p, w) in [
        ("iOS ", "i o s "),
        ("ios ", "i o s "),
        ("v", "v "),
        ("V", "V "),
        ("ver. ", "version "),
        ("version ", "version "),
    ] {
        if let Some(a) = s.strip_prefix(p) {
            if a.matches('.').count() >= 2 {
                let x: Option<Vec<_>> = a.split('.').map(|n| number(n, false)).collect();
                if let Some(x) = x {
                    out.push(format!("{w}{}", x.join(" punktum ")));
                }
            }
        }
    }
    out
}
pub fn roman(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(a) = s.strip_suffix('.') {
        if let Some(w) = lookup(ROMAN_ORD, a) {
            out.push(w.into());
        }
    }
    for name in NAMES.lines() {
        if let Some(r) = s.strip_prefix(name).and_then(|a| a.strip_prefix(' ')) {
            if let Some(w) = lookup(ROMAN_ORD, r) {
                out.push(format!("{name} den {w}"));
            }
        }
    }
    for row in KEYWORDS.lines() {
        if let Some((k, w)) = row.split_once('\t') {
            if let Some(r) = s.strip_prefix(k).and_then(|a| a.strip_prefix(' ')) {
                if let Some(n) = lookup(ROMAN_CARD, r) {
                    out.push(format!("{w} {n}"));
                }
            }
        }
    }
    for k in ["century ", "century", "Century ", "Century", "århundrede ", "Århundrede ", "årh. ", "årh.", "årh "]
    {
        if let Some(r) = s.strip_prefix(k) {
            if let Some(w) = lookup(ROMAN_ORD, r).or_else(|| lookup(ROMAN_CARD, r)) {
                out.push(format!("århundrede {w}"));
            }
        }
    }
    if let Some(a) = s.strip_suffix(" århundrede") {
        let (a, p) =
            if let Some(x) = a.strip_prefix("det ").or_else(|| a.strip_prefix("Det ")) { (x, "det ") } else { (a, "") };
        let a = a.strip_suffix('.').unwrap_or(a);
        if let Some(w) = lookup(ROMAN_ORD, a) {
            out.push(format!("{p}{} århundrede", if a == "II" { "andet" } else { w }));
        }
    }
    out
}
fn pair(s: &str) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if s.starts_with('0') { Some(format!("nul {}", number(&s[1..], false)?)) } else { number(s, false) }
}
pub fn telephone(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(t) = s.strip_prefix('+') {
        if t.chars().all(|c| c.is_ascii_digit() || " -.()".contains(c)) {
            let d: String = t.chars().filter(|c| c.is_ascii_digit()).collect();
            if (7..=15).contains(&d.len()) {
                if let Some(v) = digits(&d) {
                    out.push(format!("plus {v}"));
                }
            }
        }
    }
    if let Some(t) = s.strip_prefix('+') {
        let groups: Vec<_> = t.split(' ').collect();
        if groups.len() == 4
            && (groups[0].len() == 1 || groups[0].len() == 2)
            && groups.iter().all(|x| x.bytes().all(|b| b.is_ascii_digit()))
        {
            let country = if groups[0].len() == 1 { number(groups[0], false) } else { pair(groups[0]) };
            let group = |x: &str| -> Option<String> {
                match x.len() {
                    2 => pair(x),
                    3 => digits(x),
                    4 => Some(format!("{} {}", pair(&x[..2])?, pair(&x[2..])?)),
                    _ => None,
                }
            };
            if let (Some(c), Some(a), Some(b), Some(d)) =
                (country, group(groups[1]), group(groups[2]), group(groups[3]))
            {
                out.push(format!("plus {c} {a} {b} {d}"));
            }
        }
    }
    let (p, x) = if let Some(x) = s.strip_prefix("+45") {
        ("plus femogfyrre ", x.strip_prefix(' ').unwrap_or(x))
    } else if let Some(x) = s.strip_prefix("0045") {
        ("nul nul femogfyrre ", x.strip_prefix(' ').unwrap_or(x))
    } else {
        ("", s)
    };
    if x.chars().all(|c| c.is_ascii_digit() || c == ' ' || c == '-') {
        let d: String = x.chars().filter(|c| c.is_ascii_digit()).collect();
        let chunks: Vec<_> = x.split(|c| c == ' ' || c == '-').collect();
        if d.len() == 8
            && (!p.is_empty() || chunks.len() > 1)
            && chunks.iter().all(|c| !c.is_empty() && c.len() % 2 == 0)
        {
            let v: Option<Vec<_>> = (0..4).map(|i| pair(&d[i * 2..i * 2 + 2])).collect();
            if let Some(v) = v {
                out.insert(0, format!("{p}{}", v.join(" ")));
            }
        }
    }
    if let Some(x) = s.strip_prefix('(') {
        if let Some((code, rest)) = x.split_once(')') {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            if code.len() == 3 && code.bytes().all(|b| b.is_ascii_digit()) {
                if let Some((a, b)) = rest.split_once('-') {
                    let group = |x: &str| -> Option<String> {
                        if !x.bytes().all(|b| b.is_ascii_digit()) {
                            return None;
                        }
                        match x.len() {
                            2 => pair(x),
                            3 => digits(x),
                            4 => Some(format!("{} {}", pair(&x[..2])?, pair(&x[2..])?)),
                            _ => None,
                        }
                    };
                    if let (Some(c), Some(a), Some(b)) = (digits(code), group(a), group(b)) {
                        out.push(format!("{c} {a} {b}"));
                    }
                }
            }
        }
    }
    out
}
fn spell(s: &str) -> Option<String> {
    let mut out = Vec::new();
    let mut rest = s;
    while !rest.is_empty() {
        let mut matched = false;
        for (k, v) in [
            ("google", "google"),
            ("gmail", "g mail"),
            ("nvidia", "nvidia"),
            ("localhost", "localhost"),
            ("example", "example"),
            ("firma", "firma"),
            ("dr", "d r"),
        ] {
            if let Some(x) = rest.strip_prefix(k) {
                out.push(v.to_owned());
                rest = x;
                matched = true;
                break;
            }
        }
        if matched {
            continue;
        }
        let c = rest.chars().next()?;
        if letter(c) {
            out.push(c.to_lowercase().to_string());
        } else if c.is_ascii_digit() {
            out.push(number(&c.to_string(), false)?);
        } else {
            return None;
        }
        rest = &rest[c.len_utf8()..];
    }
    if out.is_empty() { None } else { Some(out.join(" ")) }
}
fn tld(s: &str) -> Option<&'static str> {
    match s {
        "com" => Some("com"),
        "dk" => Some("d k"),
        "org" => Some("o r g"),
        "net" => Some("n e t"),
        "edu" => Some("e d u"),
        "gov" => Some("g o v"),
        "io" => Some("i o"),
        "ai" => Some("a i"),
        "dev" => Some("dev"),
        "se" => Some("s e"),
        "no" => Some("n o"),
        "de" => Some("d e"),
        "sm" => Some("s m"),
        "es" => Some("e s"),
        _ => None,
    }
}
fn domain(s: &str) -> Option<String> {
    let labels: Vec<_> = s.split('.').collect();
    if labels.len() < 2 {
        return None;
    }
    let mut words = vec![spell(labels[0])?];
    for label in &labels[1..] {
        words.push("punktum".into());
        words.push(match tld(label) {
            Some(x) => x.into(),
            None => spell(label)?,
        });
    }
    Some(words.join(" "))
}
fn separated(s: &str, seps: &str) -> Option<String> {
    let mut words = Vec::new();
    let mut start = 0;
    for (i, c) in s.char_indices() {
        if seps.contains(c) {
            words.push(if seps.contains('/') {
                number(&s[start..i], false).or_else(|| spell(&s[start..i]))?
            } else {
                spell(&s[start..i])?
            });
            words.push(
                match c {
                    '.' => "punktum",
                    '-' => "bindestreg",
                    '_' => "understreg",
                    '/' => "skråstreg",
                    '?' => "spørgsmålstegn",
                    '=' => "er lig med",
                    '&' => "og",
                    _ => return None,
                }
                .into(),
            );
            start = i + c.len_utf8();
        }
    }
    words.push(if seps.contains('/') {
        number(&s[start..], false).or_else(|| spell(&s[start..]))?
    } else {
        spell(&s[start..])?
    });
    Some(words.join(" "))
}
pub fn electronic(s: &str) -> Option<(String, f64)> {
    let ips: Vec<_> = s.split('.').collect();
    if ips.len() == 4
        && ips.iter().all(|x| {
            x.bytes().all(|b| b.is_ascii_digit())
                && x.parse::<u16>().map_or(false, |n| n < 256)
                && !x.is_empty()
                && (x.len() == 1 || !x.starts_with('0'))
        })
    {
        let x: Option<Vec<_>> = ips
            .iter()
            .map(|a| {
                let n = number(a, false)?;
                Some(if a.starts_with('1') && a.len() == 3 { format!("et {n}") } else { n })
            })
            .collect();
        return Some((x?.join(" punktum "), -0.05));
    }
    if let Some((user, host)) = s.split_once('@') {
        if host.contains('@') {
            return None;
        }
        return Some((format!("{} snabel-a {}", separated(user, ".-_")?, domain(host)?), 0.0));
    }
    let mut rest = s;
    let mut words = Vec::new();
    let mut protocol = false;
    for (p, w) in [
        ("https://", "h t t p s kolon dobbelt skråstreg"),
        ("http://", "h t t p kolon dobbelt skråstreg"),
        ("ftp://", "f t p kolon dobbelt skråstreg"),
    ] {
        if let Some(x) = rest.strip_prefix(p) {
            words.push(w.to_owned());
            rest = x;
            protocol = true;
            break;
        }
    }
    if let Some(x) = rest.strip_prefix("www.") {
        words.push("w w w punktum".into());
        rest = x;
    }
    let (authority, path) = rest.split_once('/').map_or((rest, None), |(a, b)| (a, Some(b)));
    let (host, port) = authority.split_once(':').map_or((authority, None), |(a, b)| (a, Some(b)));
    if host == "localhost" && protocol {
        words.push("localhost".into());
    } else {
        words.push(domain(host)?);
    }
    if let Some(p) = port {
        words.push("kolon".into());
        words.push(number(p, false)?);
    }
    if let Some(p) = path {
        words.push("skråstreg".into());
        words.push(separated(p, "/.-_?=&")?);
    }
    Some((words.join(" "), 0.0))
}

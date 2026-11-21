// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
use super::cardinal::{Gender, digit, number};
pub(super) fn letter(c: char) -> Option<&'static str> {
    const LETTERS: [&str; 26] = [
        "ей",
        "бі",
        "сі",
        "ді",
        "і",
        "еф",
        "джі",
        "ейч",
        "ай",
        "джей",
        "кей",
        "ель",
        "ем",
        "ен",
        "оу",
        "пі",
        "кю",
        "ар",
        "ес",
        "ті",
        "ю",
        "ві",
        "дабл-ю",
        "ікс",
        "вай",
        "зед",
    ];
    if c.is_ascii_alphabetic() { Some(LETTERS[(c.to_ascii_lowercase() as u8 - b'a') as usize]) } else { None }
}
fn atom(c: char, symbols: &str) -> Option<&'static str> {
    if let Some(w) = letter(c) {
        return Some(w);
    }
    if c.is_ascii_digit() {
        return Some(digit(c as usize - '0' as usize));
    }
    if !symbols.contains(c) {
        return None;
    }
    Some(match c {
        ':' => "двокрапка",
        '/' => "слеш",
        '.' => "крапка",
        '-' => "дефіс",
        '_' => "підкреслення",
        '~' => "тильда",
        '?' => "знак питання",
        '=' => "дорівнює",
        '&' => "і",
        '%' => "відсоток",
        '#' => "решітка",
        '+' => "плюс",
        _ => return None,
    })
}
fn sequence(s: &str, symbols: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    Some(s.chars().map(|c| atom(c, symbols)).collect::<Option<Vec<_>>>()?.join(" "))
}
fn host(s: &str) -> Option<(String, i32)> {
    let (labels, tld) = s.rsplit_once('.')?;
    if labels.is_empty() || tld.len() != 2 && !["com", "org", "net", "edu", "gov", "dev", "app"].contains(&tld) {
        return None;
    }
    if !tld.bytes().all(|b| b.is_ascii_alphabetic()) {
        return None;
    }
    let mut names = labels.split('.').map(|s| sequence(s, "-")).collect::<Option<Vec<_>>>()?;
    let t = match tld {
        "com" => "ком".into(),
        "org" => "орг".into(),
        "net" => "нет".into(),
        "edu" => "еду".into(),
        "gov" => "гов".into(),
        "ua" => "ю ей".into(),
        "dev" => "дев".into(),
        "ai" => "ай".into(),
        "io" => "і о".into(),
        "app" => "апп".into(),
        _ => sequence(tld, "")?,
    };
    names.push(t);
    Some((
        names.join(" крапка "),
        if ["com", "org", "net", "edu", "gov", "ua", "dev", "ai", "io", "app"].contains(&tld) { 0 } else { 1 },
    ))
}
pub(super) fn electronic(s: &str) -> Vec<(String, i32)> {
    let mut out = Vec::new();
    if let Some((local, domain)) = s.split_once('@') {
        if let (Some(a), Some((b, c))) = (sequence(local, ".-_+"), host(domain)) {
            out.push((format!("{a} собака {b}"), c));
        }
    }
    for (prefix, spoken) in [
        ("https://", "ейч ті ті пі ес двокрапка слеш слеш"),
        ("http://", "ейч ті ті пі двокрапка слеш слеш"),
        ("www.", "дабл ю дабл ю дабл ю крапка"),
    ] {
        if let Some(rest) = s.strip_prefix(prefix) {
            if let Some(w) = sequence(rest, ":/.-_~?=&%#+") {
                out.push((format!("{spoken} {w}"), 0));
            }
        }
    }
    if let Some(w) = host(s) {
        out.push(w);
    }
    let parts: Vec<_> = s.split('.').collect();
    if parts.len() == 4 && parts.iter().all(|p| p.parse::<u8>().is_ok() && (p.len() == 1 || !p.starts_with('0'))) {
        if let Some(v) = parts.iter().map(|p| number(p, Gender::Masculine, false)).collect::<Option<Vec<_>>>() {
            out.push((v.join(" крапка "), 0));
        }
    }
    if let Some((name, version)) = s.split_once(' ') {
        if !name.is_empty() && name.bytes().all(|b| b.is_ascii_alphabetic()) {
            let p: Vec<_> = version.split('.').collect();
            if p.len() >= 2 && p.iter().all(|x| !x.is_empty() && x.bytes().all(|b| b.is_ascii_digit())) {
                if let Some(v) = p.iter().map(|x| sequence(x, "")).collect::<Option<Vec<_>>>() {
                    out.push((format!("{name} {}", v.join(" крапка ")), 0));
                }
            }
        }
    }
    let (prefix, version) =
        if let Some(v) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) { ("версія ", v) } else { ("", s) };
    let p: Vec<_> = version.split('.').collect();
    if p.len() >= (if prefix.is_empty() { 3 } else { 2 })
        && p.iter().all(|x| !x.is_empty() && x.bytes().all(|b| b.is_ascii_digit()))
    {
        let date = prefix.is_empty()
            && p.len() == 3
            && p[0].len() <= 2
            && p[1].len() <= 2
            && (p[2].len() == 2 || p[2].len() == 4);
        let grouped =
            prefix.is_empty() && p[0].len() <= 3 && !p[0].starts_with('0') && p.iter().skip(1).all(|x| x.len() == 3);
        if !date && !grouped {
            if let Some(v) = p.iter().map(|x| sequence(x, "")).collect::<Option<Vec<_>>>() {
                out.push((format!("{prefix}{}", v.join(" крапка ")), 0));
            }
        }
    }
    out
}

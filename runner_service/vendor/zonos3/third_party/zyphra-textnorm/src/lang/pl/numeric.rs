// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
use super::number_names::{Case, Gender, cardinal, ordinal};
const M: Gender = Gender::Masculine;
const F: Gender = Gender::Feminine;
const N: Case = Case::Nominative;
const G: Case = Case::Genitive;
/// Validate the cardinal grouping graph and obtain its numeric value without
/// producing its spoken form.
pub fn grouped_value(s: &str, case: Case) -> Option<u64> {
    if s.is_empty() || (s.contains('.') && (s.contains(' ') || s.contains('\u{a0}'))) {
        return None;
    }
    let mut groups = s.split([' ', '\u{a0}', '.']);
    let first = groups.next()?;
    if first.is_empty() || !first.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let mut value = 0u64;
    let mut len = first.len();
    for b in first.bytes() {
        value = value.checked_mul(10)?.checked_add((b - b'0') as u64)?;
    }
    let mut count = 0;
    for group in groups {
        if count == 0 && (first.len() > 3 || first.starts_with('0')) {
            return None;
        }
        if group.len() != 3 || !group.bytes().all(|c| c.is_ascii_digit()) {
            return None;
        }
        count += 1;
        if count > 3 {
            return None;
        }
        len += group.len();
        for b in group.bytes() {
            value = value.checked_mul(10)?.checked_add((b - b'0') as u64)?;
        }
    }
    if len > 12
        || (count == 0 && first.len() > 1 && first.starts_with('0'))
        || (case == Case::Genitive && value >= 2000)
    {
        return None;
    }
    Some(value)
}

/// Recognize the single-path range, score and addition branches without
/// spelling either operand. Other graphs are handled by `card`.
pub fn simple_binary(s: &str) -> Option<f64> {
    for (sep, weight, case) in [('-', 0.0, G), ('–', 0.0, G), ('—', 0.0, G), (':', 0.05, G), ('+', 0.0, N)] {
        if let Some((a, b)) = s.split_once(sep) {
            if grouped_value(a, N).is_some() && grouped_value(b, case).is_some() {
                return Some(weight);
            }
        }
    }
    None
}

pub fn grouped(s: &str, gender: Gender, case: Case, dots: bool) -> Option<String> {
    if s.contains('.') && (s.contains(' ') || s.contains('\u{a0}')) {
        return None;
    }
    if s.bytes().all(|c| c.is_ascii_digit()) {
        return cardinal(s, gender, case);
    }
    let mut groups = s.split(|c| c == ' ' || c == '\u{a0}' || (dots && c == '.'));
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut digits = first.to_owned();
    let mut count = 0;
    for g in groups {
        if g.len() != 3 || !g.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        digits.push_str(g);
        count += 1;
    }
    if count == 0 || count > 3 {
        return None;
    }
    cardinal(&digits, gender, case)
}
pub fn signed(s: &str) -> (&str, &'static str) {
    if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (s, "minus ")
    } else if let Some(s) = s.strip_prefix('~') {
        (s.strip_prefix(' ').unwrap_or(s), "około ")
    } else if let Some(s) = s.strip_prefix('±') {
        (s.strip_prefix(' ').unwrap_or(s), "plus minus ")
    } else {
        (s, "")
    }
}
pub fn digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    s.chars().map(|c| cardinal(&c.to_string(), M, N)).collect::<Option<Vec<_>>>().map(|v| v.join(" "))
}
pub fn simple_cardinal_words(s: &str) -> Option<String> {
    let (body, sign) = signed(s);
    let word = cardinal(body, M, N).or_else(|| digits(body))?;
    if sign.is_empty() { Some(word) } else { Some(format!("{sign}{word}")) }
}
/// An alphabetic character in the cardinal graph belongs either to a
/// single-letter power base or to the literal Polish power phrase.
pub fn cardinal_alpha_prefix(s: &str) -> bool {
    let (body, _) = signed(s);
    if body.as_bytes().first().is_some_and(u8::is_ascii_alphabetic) {
        let tail = &body[1..];
        return tail.is_empty() || tail.strip_prefix('^').is_some_and(|p| p.bytes().all(|c| c.is_ascii_digit()));
    }
    let Some((i, _)) = body.char_indices().find(|(_, c)| c.is_alphabetic()) else {
        return true;
    };
    let tail = &body[i..];
    const POWER: &str = "do potęgi ";
    POWER.starts_with(tail) || tail.strip_prefix(POWER).is_some_and(|p| p.bytes().all(|c| c.is_ascii_digit()))
}
pub fn fraction(s: &str) -> Vec<(String, f64)> {
    const FRS: &[(&str, &str)] = &[
        ("½", "jedna druga"),
        ("⅓", "jedna trzecia"),
        ("⅔", "dwie trzecie"),
        ("¼", "jedna czwarta"),
        ("¾", "trzy czwarte"),
        ("⅕", "jedna piąta"),
        ("⅖", "dwie piąte"),
        ("⅗", "trzy piąte"),
        ("⅘", "cztery piąte"),
        ("⅙", "jedna szósta"),
        ("⅚", "pięć szóstych"),
        ("⅛", "jedna ósma"),
        ("⅜", "trzy ósme"),
        ("⅝", "pięć ósmych"),
        ("⅞", "siedem ósmych"),
        ("⅐", "jedna siódma"),
        ("⅑", "jedna dziewiąta"),
        ("⅒", "jedna dziesiąta"),
        ("1/2", "jedna druga"),
        ("1/3", "jedna trzecia"),
        ("2/3", "dwie trzecie"),
        ("1/4", "jedna czwarta"),
        ("3/4", "trzy czwarte"),
        ("1/5", "jedna piąta"),
        ("2/5", "dwie piąte"),
        ("3/5", "trzy piąte"),
        ("4/5", "cztery piąte"),
        ("1/6", "jedna szósta"),
        ("5/6", "pięć szóstych"),
        ("1/7", "jedna siódma"),
        ("1/8", "jedna ósma"),
        ("3/8", "trzy ósme"),
        ("5/8", "pięć ósmych"),
        ("7/8", "siedem ósmych"),
        ("1/9", "jedna dziewiąta"),
        ("1/10", "jedna dziesiąta"),
    ];
    let (s, sign) = signed(s);
    let mut out = Vec::new();
    for &(key, value) in FRS {
        if s == key {
            out.push((format!("{sign}{value}"), 0.0));
        }
        if let Some(prefix) = s.strip_suffix(key) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if let Some(n) = grouped(prefix, M, N, true) {
                if key == "½" || key == "1/2" {
                    out.push((format!("{sign}{n} i pół"), 0.0));
                }
                out.push((format!("{sign}{n} i {value}"), 0.0));
            }
        }
    }
    out
}
pub fn ord(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (suffix, gender) in [
        (".", M),
        ("-szy", M),
        ("-gi", M),
        ("-ci", M),
        ("-ty", M),
        ("-my", M),
        ("-sza", F),
        ("-ga", F),
        ("-cia", F),
        ("-ta", F),
        ("-ma", F),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            if let Some(w) = ordinal(n, gender, N) {
                out.push((w, 0.0));
            }
        }
    }
    out
}
pub fn card(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (number, sign) = signed(s);
    if let Some(w) = grouped(number, M, N, true) {
        out.push((format!("{sign}{w}"), 0.0));
    }
    if sign.is_empty()
        && s.bytes().all(|b| b.is_ascii_digit())
        && ((s.starts_with('0') && s.len() > 1) || s.len() >= 13)
    {
        if let Some(w) = digits(s) {
            out.push((w, 0.0));
        }
    }
    if sign.is_empty() {
        for (sep, relation, weight) in
            [('-', " do ", 0.0), ('–', " do ", 0.0), ('—', " do ", 0.0), (':', " do ", 0.05), ('+', " plus ", 0.0)]
        {
            if let Some((a, b)) = s.split_once(sep) {
                let case = if sep == '+' { N } else { G };
                let second = if sep == ':' && b == "1" { Some("jednego".into()) } else { grouped(b, M, case, true) };
                if let (Some(a), Some(b)) = (grouped(a, M, N, true), second) {
                    out.push((format!("{a}{relation}{b}"), weight));
                }
            }
        }
        for sep in [" = ", "="] {
            if let Some((a, b)) = s.split_once(sep) {
                if let (Some(a), Some(b)) = (grouped(a, M, N, true), grouped(b, M, N, true)) {
                    out.push((format!("{a} równa się {b}"), 0.0));
                }
            }
        }
        if let Some((a, b)) = s.split_once(" do potęgi ") {
            if let (Some(a), Some(b)) = (grouped(a, M, N, true), ordinal(b, F, G)) {
                out.push((format!("{a} do potęgi {b}"), 0.0));
            }
        }
    }
    if let Some((base, power)) = number.split_once('^') {
        let base = if base.len() == 1 && base.as_bytes()[0].is_ascii_alphabetic() {
            Some(base.to_owned())
        } else {
            grouped(base, M, N, true)
        };
        if let Some(base) = base {
            if power == "2" {
                out.push((format!("{sign}{base} do kwadratu"), 0.0));
            }
            if power == "3" {
                out.push((format!("{sign}{base} do sześcianu"), 0.0));
            }
            if let Some(p) = ordinal(power, F, G) {
                out.push((format!("{sign}{base} do potęgi {p}"), 0.0));
            }
        }
    }
    out
}
pub fn decimal_core(s: &str) -> Option<(String, f64)> {
    let (a, b, penalty, dots) = if let Some((a, b)) = s.split_once(',') {
        (a, b, 0.0, true)
    } else if let Some((a, b)) = s.rsplit_once('.') {
        (a, b, 0.2, false)
    } else {
        return None;
    };
    if b == "0" {
        return None;
    }
    let a = grouped(a, M, N, dots)?;
    let b = if b.len() >= 4 || (b.len() > 1 && b.starts_with('0')) { digits(b)? } else { cardinal(b, M, N)? };
    Some((format!("{a} przecinek {b}"), penalty))
}
pub fn decimal(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (body, sign) = signed(s);
    if let Some((w, c)) = decimal_core(body) {
        out.push((format!("{sign}{w}"), c));
    }
    for (suffix, noun) in [("%", "procent"), ("‰", "promili")] {
        if let Some(n) = body.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(w) = grouped(n, M, N, true) {
                out.push((format!("{sign}{w} {noun}"), 0.0));
            }
            if let Some((w, c)) = decimal_core(n) {
                out.push((format!("{sign}{w} {noun}"), c));
            }
            if sign.is_empty() && suffix == "%" {
                for sep in ['-', '–', '—'] {
                    if let Some((a, b)) = n.split_once(sep) {
                        let repeated = a.ends_with('%');
                        let a = a.strip_suffix('%').unwrap_or(a);
                        if let (Some(a), Some(b)) = (grouped(a, M, N, true), grouped(b, M, G, true)) {
                            out.push((format!("{a}{} do {b} procent", if repeated { " procent" } else { "" }), 0.0));
                        }
                        if !repeated {
                            if let (Some((a, ca)), Some((b, cb))) = (decimal_core(a), decimal_core(b)) {
                                out.push((format!("{a} do {b} procent"), ca + cb));
                            }
                        }
                    }
                }
            }
        }
    }
    out
}

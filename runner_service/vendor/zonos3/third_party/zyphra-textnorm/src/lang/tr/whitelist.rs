//! Symbols, abbreviations, numeric expressions and serials.
use super::{
    decimal,
    graph_utils::{is_positive, positive},
};
use std::{collections::HashMap, sync::LazyLock};
static ABBREVIATIONS: LazyLock<HashMap<&'static str, usize>> = LazyLock::new(|| {
    let mut table = HashMap::new();
    for row in include_str!("../../../data/tr/abbrevs.tsv").lines() {
        if let Some((key, _)) = row.split_once('\t') {
            *table.entry(key).or_insert(0) += 1;
        }
    }
    table
});
static ABBREV_INITIALS: LazyLock<[bool; 256]> = LazyLock::new(|| {
    let mut starts = [false; 256];
    for key in ABBREVIATIONS.keys() {
        if let Some(&first) = key.as_bytes().first() {
            starts[first as usize] = true;
        }
    }
    starts
});
pub fn abbreviation_count(s: &str) -> usize {
    if !s.as_bytes().first().is_some_and(|&c| ABBREV_INITIALS[c as usize]) {
        return 0;
    }
    ABBREVIATIONS.get(s).copied().unwrap_or(0)
}
/// An alphabetic first chunk can only be an abbreviation, a number prefix,
/// or the name before a three-part version. Other whitelist branches require
/// their punctuation or digit inside that same chunk.
pub fn possible_start(text: &[char], start: usize) -> bool {
    if !text[start].is_ascii_alphabetic() {
        return true;
    }
    let end = text[start..].iter().position(|c| crate::text::is_nemo_space(*c)).map_or(text.len(), |n| start + n);
    let word = &text[start..end];
    if !word.iter().all(char::is_ascii_alphabetic) {
        return true;
    }
    if ["No", "no"].iter().any(|s| s.chars().eq(word.iter().copied()))
        || ABBREVIATIONS.keys().any(|s| s.chars().eq(word.iter().copied()))
    {
        return true;
    }
    if text.get(end) != Some(&' ') {
        return false;
    }
    text[end + 1..].iter().take_while(|c| !crate::text::is_nemo_space(**c)).filter(|&&c| c == '.').take(2).count() == 2
}
/// Alphabetic runs in a digit-led expression are quantity names, arithmetic
/// operators, or the single-letter numeric suffixes. A quantity may touch
/// the following multiplication/equality operator without whitespace.
pub fn numeric_word(word: &str, complete: bool) -> bool {
    const QUANTITIES: [&str; 9] =
        ["milyon", "milyonluk", "milyar", "milyarlık", "trilyon", "katrilyon", "bin", "bilyon", "biljoner"];
    const WORDS: [&str; 8] = ["x", "eşittir", "G", "g", "K", "k", "D", "d"];
    let matches_word = |item: &str| if complete { item == word } else { item.starts_with(word) };
    if WORDS.iter().any(|item| matches_word(item)) {
        return true;
    }
    for quantity in QUANTITIES {
        if matches_word(quantity) {
            return true;
        }
        if let Some(rest) = word.strip_prefix(quantity) {
            if ["x", "eşittir"].iter().any(|item| if complete { *item == rest } else { item.starts_with(rest) }) {
                return true;
            }
        }
    }
    false
}
fn num_count(s: &str) -> usize {
    decimal::count(s) + usize::from(is_positive(s))
}
fn alpha(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_alphabetic())
}
fn valid_version(s: &str) -> bool {
    let mut p = s.split('.');
    let (Some(a), Some(b), Some(c)) = (p.next(), p.next(), p.next()) else { return false };
    p.next().is_none()
        && a.len() <= 3
        && b.len() <= 3
        && c.len() <= 2
        && is_positive(a)
        && is_positive(b)
        && is_positive(c)
}
fn arith_count(s: &str, count: usize) -> usize {
    if count == 0 {
        return num_count(s);
    }
    let mut total = 0;
    for (i, c) in s.char_indices() {
        let len = match c {
            '+' | '=' | 'x' | '×' | '*' => c.len_utf8(),
            'e' if s[i..].starts_with("eşittir") => "eşittir".len(),
            _ => continue,
        };
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let b = &s[i + len..];
        let b = b.strip_prefix(' ').unwrap_or(b);
        total += num_count(a) * arith_count(b, count - 1);
    }
    total
}
/// Count whitelist paths without formatting lexical or numeric outputs.
pub fn count(s: &str) -> usize {
    let mut total = abbreviation_count(s);
    total += usize::from(matches!(s, "&" | "©" | "§" | "CO2" | "H2O" | "O2" | "N2"));
    for prefix in ["No. ", "No ", "No: ", "No:", "no. ", "no ", "no: ", "#"] {
        if s.strip_prefix(prefix).is_some_and(is_positive) {
            total += 1;
        }
    }
    for mark in ['%', '‰'] {
        if let Some(rest) = s.strip_prefix(mark).or_else(|| s.strip_suffix(mark)) {
            total += num_count(rest);
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            if let Some(a) = a.strip_prefix('%') {
                total += num_count(a) * num_count(b.strip_prefix('%').unwrap_or(b));
            }
            if let Some(b) = b.strip_suffix('%') {
                total += num_count(a) * num_count(b);
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        let bases = usize::from(a.len() <= 3 && alpha(a)) + usize::from(is_positive(a));
        let exps = usize::from(b == "2")
            + usize::from(b == "3")
            + usize::from(is_positive(b))
            + usize::from(b.len() <= 2 && alpha(b));
        total += bases * exps;
    }
    if s.bytes().any(|b| matches!(b, b'+' | b'=' | b'x' | b'*')) || s.contains('×') || s.contains("eşittir") {
        for n in 1..=3 {
            total += arith_count(s, n);
        }
    }
    if s.bytes().filter(|&b| b == b'.').take(2).count() == 2 {
        total += usize::from(valid_version(s));
        if let Some((a, b)) = s.split_once(' ') {
            total += usize::from(alpha(a) && valid_version(b));
        }
    }
    for c in ['G', 'g', 'K', 'k', 'D', 'd'] {
        total += usize::from(s.strip_suffix(c).is_some_and(is_positive));
    }
    let mut p = s.split('-');
    if let (Some(a), Some(b), Some(c), None) = (p.next(), p.next(), p.next(), p.next()) {
        total += usize::from(alpha(a) && is_positive(b) && alpha(c));
    }
    total
}
fn num(s: &str) -> Vec<String> {
    let mut v = decimal::readings(s);
    if let Some(n) = positive(s) {
        v.push(n);
    }
    v
}
fn spaced(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|c| c.is_ascii_alphabetic()) {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 2 - 1);
    for (i, c) in s.bytes().enumerate() {
        if i != 0 {
            out.push(' ');
        }
        out.push(c.to_ascii_lowercase() as char);
    }
    Some(out)
}
fn version(s: &str) -> Option<String> {
    let mut p = s.split('.');
    let (Some(a), Some(b), Some(c)) = (p.next(), p.next(), p.next()) else { return None };
    if p.next().is_some() || a.len() > 3 || b.len() > 3 || c.len() > 2 {
        return None;
    }
    Some(format!("{} nokta {} nokta {}", positive(a)?, positive(b)?, positive(c)?))
}
fn arith(s: &str, count: usize) -> Vec<String> {
    if count == 0 {
        return num(s);
    }
    let mut out = Vec::new();
    for (i, c) in s.char_indices() {
        let (len, op) = match c {
            '+' => (1, "artı"),
            '=' => (1, "eşittir"),
            'x' | '×' | '*' => (c.len_utf8(), "çarpı"),
            'e' if s[i..].starts_with("eşittir") => ("eşittir".len(), "eşittir"),
            _ => continue,
        };
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let b = &s[i + len..];
        let b = b.strip_prefix(' ').unwrap_or(b);
        for x in num(a) {
            for y in arith(b, count - 1) {
                out.push(format!("{x} {op} {y}"));
            }
        }
    }
    out
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for line in include_str!("../../../data/tr/abbrevs.tsv").lines() {
        if let Some((a, b)) = line.split_once('\t') {
            if s == a {
                out.push(b.into());
            }
        }
    }
    for (a, b) in [
        ("&", "ve"),
        ("©", "telif hakkı"),
        ("§", "madde"),
        ("CO2", "c o iki"),
        ("H2O", "h iki o"),
        ("O2", "o iki"),
        ("N2", "n iki"),
    ] {
        if s == a {
            out.push(b.into());
        }
    }
    for prefix in ["No. ", "No ", "No: ", "No:", "no. ", "no ", "no: ", "#"] {
        if let Some(n) = s.strip_prefix(prefix).and_then(positive) {
            out.push(format!("numara {n}"));
        }
    }
    for (mark, name) in [('%', "yüzde"), ('‰', "binde")] {
        if let Some(rest) = s.strip_prefix(mark).or_else(|| s.strip_suffix(mark)) {
            for n in num(rest) {
                out.push(format!("{name} {n}"));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let b = &s[i + c.len_utf8()..];
            let b = b.strip_prefix(' ').unwrap_or(b);
            if let Some(a) = a.strip_prefix('%') {
                let b = b.strip_prefix('%').unwrap_or(b);
                for x in num(a) {
                    for y in num(b) {
                        out.push(format!("yüzde {x} ila yüzde {y}"));
                    }
                }
            }
            if let Some(b) = b.strip_suffix('%') {
                for x in num(a) {
                    for y in num(b) {
                        out.push(format!("yüzde {x} ila {y}"));
                    }
                }
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        let mut bases = Vec::new();
        if (1..=3).contains(&a.len()) && a.bytes().all(|c| c.is_ascii_alphabetic()) {
            bases.push(a.to_owned());
        }
        if let Some(n) = positive(a) {
            bases.push(n);
        }
        let mut exps = Vec::new();
        if b == "2" {
            exps.push("kare".into());
        }
        if b == "3" {
            exps.push("küp".into());
        }
        if let Some(n) = positive(b) {
            exps.push(format!("üzeri {n}"));
        }
        if (1..=2).contains(&b.len()) && b.bytes().all(|c| c.is_ascii_alphabetic()) {
            exps.push(format!("üzeri {b}"));
        }
        for a in bases {
            for b in &exps {
                out.push(format!("{a} {b}"));
            }
        }
    }
    if s.chars().any(|c| matches!(c, '+' | '=' | 'x' | '×' | '*' | 'e')) {
        for count in 1..=3 {
            out.extend(arith(s, count));
        }
    }
    if let Some(v) = version(s) {
        out.push(v);
    }
    if let Some((a, b)) = s.split_once(' ') {
        if let (Some(a), Some(b)) = (spaced(a), version(b)) {
            out.push(format!("{a} {b}"));
        }
    }
    for c in ['G', 'g', 'K', 'k', 'D', 'd'] {
        if let Some(n) = s.strip_suffix(c).and_then(positive) {
            out.push(format!("{n} {}", c.to_ascii_lowercase()));
        }
    }
    let mut p = s.split('-');
    if let (Some(a), Some(b), Some(c), None) = (p.next(), p.next(), p.next(), p.next()) {
        if let (Some(a), Some(b), Some(c)) = (spaced(a), positive(b), spaced(c)) {
            out.push(format!("{a} {b} {c}"));
        }
    }
    out
}

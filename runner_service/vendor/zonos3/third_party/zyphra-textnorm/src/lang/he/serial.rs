//! Version numbers, alphanumerics, and variable powers.
use super::cardinal::base_num;
const LETTERS: [&str; 26] = [
    "איי",
    "בי",
    "סי",
    "די",
    "אי",
    "אף",
    "ג'י",
    "אייץ'",
    "איי",
    "ג'יי",
    "קיי",
    "אל",
    "אם",
    "אן",
    "או",
    "פי",
    "קיו",
    "אר",
    "אס",
    "טי",
    "יו",
    "וי",
    "דבליו",
    "איקס",
    "וואי",
    "זד",
];
pub(crate) fn letters(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_alphabetic()) {
        return None;
    }
    Some(s.bytes().map(|b| LETTERS[(b.to_ascii_lowercase() - b'a') as usize]).collect::<Vec<_>>().join(" "))
}
fn version(s: &str) -> Option<String> {
    let p: Vec<_> = s.split('.').collect();
    if p.len() < 3 {
        return None;
    }
    let n: Option<Vec<_>> = p.iter().map(|p| base_num(p, false)).collect();
    Some(n?.join(" נקודה "))
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if let Some(v) = version(s) {
        out.push((v, 2_790_000));
    }
    for (lead, name) in [
        ("iOS", "iOS"),
        ("ios", "iOS"),
        ("v.", "v"),
        ("v", "v"),
        ("version", "version"),
        ("Android", "Android"),
        ("Windows", "Windows"),
        ("Python", "Python"),
    ] {
        if let Some(t) = s.strip_prefix(lead) {
            let t = t.strip_prefix(' ').unwrap_or(t);
            if let Some(v) = version(t) {
                out.push((format!("{name} {v}"), 2_740_000));
            }
        }
    }
    if let Some((var, power)) = s.split_once('^') {
        if matches!(var, "x" | "X" | "y" | "Y" | "z" | "Z" | "a" | "A" | "b" | "B" | "n" | "N") {
            let p = match power {
                "2" => Some("בריבוע"),
                "3" => Some("בשלישית"),
                "4" => Some("ברביעית"),
                "5" => Some("בחמישית"),
                "n" => Some("בחזקת אן"),
                "k" => Some("בחזקת קיי"),
                "x" => Some("בחזקת איקס"),
                _ => None,
            };
            if let (Some(v), Some(p)) = (letters(var), p) {
                out.push((format!("{v} {p}"), 1_850_000));
            }
        }
    }
    for (i, _) in s.char_indices().skip(1) {
        let a = &s[..i];
        let b = &s[i..];
        let a = a.strip_suffix(' ').unwrap_or(a);
        let b = b.strip_prefix(' ').unwrap_or(b);
        if let (Some(n), Some(l)) = (base_num(a, false), letters(b)) {
            out.push((format!("{n} {l}"), 3_460_000));
        }
        if let (Some(l), Some(n)) = (letters(a), base_num(b, false)) {
            out.push((format!("{l} {n}"), 3_460_000));
        }
    }
    let p: Vec<_> = s.split(|c| matches!(c, '-' | '–' | '—' | '־')).collect();
    if p.len() == 3 {
        if let (Some(a), Some(b), Some(c)) = (letters(p[0]), base_num(p[1], false), letters(p[2])) {
            out.push((format!("{a} {b} {c}"), 1_860_000));
        }
    }
    out
}

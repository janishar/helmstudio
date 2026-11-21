//! Non-functional source branches whose alternatives have the same input span.
use super::graph_utils::{Gender, digit};
use super::numbers::{cardinal, decimal};

pub fn decimal_paths(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some((v, w)) = decimal(s) {
        let unsigned = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(s);
        if let Some(i) = unsigned.rfind(|c| c == ',' || c == '.') {
            let frac = &unsigned[i + 1..];
            if frac.len() >= 2 && frac.bytes().all(|b| b.is_ascii_digit()) {
                let digits = if frac.len() == 2 && frac.as_bytes()[0] >= b'2' {
                    cardinal(frac).unwrap_or_default()
                } else {
                    frac.bytes().map(|b| digit((b - b'0') as usize, Gender::Default)).collect::<Vec<_>>().join(" ")
                };
                // Remove the fractional part at the word boundary after "celá/celé/celých".
                for marker in [" celá ", " celé ", " celých "] {
                    if let Some((whole, _)) = v.split_once(marker) {
                        let candidate = format!("{whole}{marker}{digits}");
                        if candidate != v {
                            out.push((candidate, w))
                        }
                        break;
                    }
                }
            }
        }
    }
    out
}
pub fn leading_zero_path(s: &str) -> Option<String> {
    let (sign, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("mínus ", x) } else { ("", s) };
    if !s.starts_with('0') || s.len() < 3 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let zeros = s.bytes().take_while(|b| *b == b'0').count();
    if s.len() - zeros < 2 {
        return None;
    }
    let v = s.bytes().map(|b| digit((b - b'0') as usize, Gender::Default)).collect::<Vec<_>>().join(" ");
    if cardinal(s).as_deref() == Some(v.as_str()) {
        return None;
    }
    Some(format!("{sign}{v}"))
}

/// The scale graph gives the bare singular a 0.1 penalty, independently for
/// every occurrence in a compound expression.
pub fn scale_paths(value: &str) -> Vec<(String, f64)> {
    let mut paths = vec![(value.to_owned(), 0.0)];
    for (full, short) in [("jeden milion", "milion"), ("jedna miliarda", "miliarda")] {
        let mut i = 0;
        while i < paths.len() {
            let (text, w) = paths[i].clone();
            for (pos, _) in text.match_indices(full) {
                let mut next = String::with_capacity(text.len());
                next.push_str(&text[..pos]);
                next.push_str(short);
                next.push_str(&text[pos + full.len()..]);
                if !paths.iter().any(|(v, c)| *v == next && *c == w + 0.1) {
                    paths.push((next, w + 0.1))
                }
            }
            i += 1;
        }
    }
    paths
}

pub fn midnight_paths(value: &str) -> Vec<String> {
    let mut out = vec![value.to_owned()];
    let mut i = 0;
    while i < out.len() {
        let text = out[i].clone();
        for (pos, _) in text.match_indices("půlnoc") {
            let mut next = String::new();
            next.push_str(&text[..pos]);
            next.push_str("nula nula nula");
            next.push_str(&text[pos + "půlnoc".len()..]);
            if !out.contains(&next) {
                out.push(next)
            }
        }
        i += 1;
    }
    out
}

/// Expand digit-by-digit readings of zero-prefixed cardinals nested inside
/// larger semiotic expressions. The replacement is restricted to complete
/// words so a neighbouring number cannot be changed accidentally.
pub fn nested_leading_zero_paths(input: &str, tag: &str) -> Vec<String> {
    let mut paths = vec![tag.to_owned()];
    for part in input.split(|c: char| !c.is_ascii_digit()).filter(|x| x.len() >= 3 && x.starts_with('0')) {
        let (Some(normal), Some(alternative)) = (cardinal(part), leading_zero_path(part)) else { continue };
        let separator = if tag.starts_with("date {") { " " } else { "\u{a0}" };
        let normal = normal.replace(' ', separator);
        let alternative = alternative.replace(' ', separator);
        let mut index = 0;
        while index < paths.len() {
            let text = paths[index].clone();
            for (at, _) in text.match_indices(&normal) {
                let before = text[..at].chars().next_back();
                let after = text[at + normal.len()..].chars().next();
                if before.is_some_and(|c| c.is_alphabetic()) || after.is_some_and(|c| c.is_alphabetic()) {
                    continue;
                }
                let mut next = String::new();
                next.push_str(&text[..at]);
                next.push_str(&alternative);
                next.push_str(&text[at + normal.len()..]);
                if !paths.contains(&next) {
                    paths.push(next)
                }
            }
            index += 1;
        }
    }
    paths
}

pub fn nested_decimal_paths(input: &str, tag: &str) -> Vec<String> {
    let mut paths = vec![tag.to_owned()];
    for part in input.split(|c: char| !(c.is_ascii_digit() || c == '.' || c == ',')) {
        let Some((normal, _)) = decimal(part) else { continue };
        let normal = normal.replace(' ', "\u{a0}");
        for (alternative, _) in decimal_paths(part) {
            let alternative = alternative.replace(' ', "\u{a0}");
            let mut index = 0;
            while index < paths.len() {
                let text = paths[index].clone();
                for (at, _) in text.match_indices(&normal) {
                    let before = text[..at].chars().next_back();
                    let after = text[at + normal.len()..].chars().next();
                    if before.is_some_and(|c| c.is_alphabetic()) || after.is_some_and(|c| c.is_alphabetic()) {
                        continue;
                    }
                    let mut next = String::new();
                    next.push_str(&text[..at]);
                    next.push_str(&alternative);
                    next.push_str(&text[at + normal.len()..]);
                    if !paths.contains(&next) {
                        paths.push(next)
                    }
                }
                index += 1;
            }
        }
    }
    paths
}

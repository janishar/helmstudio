//! Decimal and magnitude readings from the Hebrew source grammar.
use super::cardinal::base_num;

const DIGITS: [&str; 10] = ["אפס", "אחת", "שתיים", "שלוש", "ארבע", "חמש", "שש", "שבע", "שמונה", "תשע"];

/// Return each source reading and its internal weight in micro-units.
pub(crate) fn decimal_num(input: &str) -> Vec<(String, i64)> {
    let (negative, rest) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(rest) => ("מינוס ", rest),
        None => ("", input),
    };
    let Some((integer, fraction)) = rest.split_once('.') else { return Vec::new() };
    let Some(integer) = base_num(integer, false) else { return Vec::new() };
    if fraction.is_empty() || !fraction.bytes().all(|b| b.is_ascii_digit()) {
        return Vec::new();
    }
    let digits = fraction.bytes().map(|b| DIGITS[(b - b'0') as usize]).collect::<Vec<_>>().join(" ");
    let mut out = vec![(format!("{negative}{integer} נקודה {digits}"), 950_000)];
    if fraction.len() == 2 {
        let alt = if fraction.starts_with('0') { Some(digits) } else { base_num(fraction, false) };
        if let Some(alt) = alt {
            out.push((format!("{negative}{integer} נקודה {alt}"), 1_050_000));
        }
    }
    out
}

/// The complete decimal union, with the top-level classifier weight included.
pub(crate) fn readings(input: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    for (name, weight) in decimal_num(input) {
        out.push((name, weight + 2_000_000));
    }
    if let Some(last) = input.chars().last() {
        let magnitude = match last {
            'k' => Some("אלף"),
            'M' => Some("מיליון"),
            'B' => Some("מיליארד"),
            _ => None,
        };
        if let Some(magnitude) = magnitude {
            let stem = &input[..input.len() - last.len_utf8()];
            let stem = stem.strip_suffix(' ').unwrap_or(stem);
            for (name, weight) in decimal_num(stem) {
                out.push((format!("{name} {magnitude}"), weight + 900_000));
            }
            let (sign, unsigned) = match stem.strip_prefix('-').or_else(|| stem.strip_prefix('−')) {
                Some(s) => ("מינוס ", s),
                None => ("", stem),
            };
            if let Some(name) = base_num(unsigned, false) {
                out.push((format!("{sign}{name} {magnitude}"), 900_000));
            }
        }
    }
    for (i, c) in input.char_indices() {
        if !matches!(c, '-' | '–' | '—' | '־') || i == 0 {
            continue;
        }
        for (left, lc) in decimal_num(&input[..i]) {
            for (right, rc) in decimal_num(&input[i + c.len_utf8()..]) {
                out.push((format!("{left} עד {right}"), lc + rc + 1_950_000));
            }
        }
    }
    out
}

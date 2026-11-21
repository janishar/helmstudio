use super::*;

pub(in crate::lang::el) fn fraction_base(s: &str) -> Option<(String, f64)> {
    if let Some((_, v)) = tables::VULGAR.iter().find(|(k, _)| *k == s) {
        return Some((spoken!("{v}"), 0.0));
    }
    let (a, b) = s.split_once('/')?;
    let numerator = number::cardinal(a)?;
    if let Ok(n) = b.parse::<usize>() {
        if n >= 2 && !b.starts_with('0') && b.bytes().all(|c| c.is_ascii_digit()) {
            if let Some(den) = number::ordinal(n, if a == "1" { "ο" } else { "α" }) {
                return Some((spoken!("{numerator} {den}"), 0.0));
            }
        }
    }
    Some((spoken!("{numerator} προς {}", number::cardinal(b)?), 0.5))
}
pub(in crate::lang::el) fn fraction(s: &str) -> Option<(String, f64)> {
    for p in ["-", "−"] {
        if let Some(x) = s.strip_prefix(p) {
            let (v, w) = positive_fraction(x)?;
            return Some((spoken!("μείον {v}"), w));
        }
    }
    positive_fraction(s)
}
pub(in crate::lang::el) fn positive_fraction(s: &str) -> Option<(String, f64)> {
    if let Some(x) = fraction_base(s) {
        return Some(x);
    }
    for (i, _) in s.char_indices().skip(1) {
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        if let (Some(n), Some((f, w))) = (number::cardinal(a), fraction_base(&s[i..])) {
            return Some((spoken!("{n} και {f}"), w));
        }
    }
    None
}
pub(in crate::lang::el) fn fraction_fallback(s: &str) -> Option<String> {
    let (sign, s) =
        if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("μείον ", s) } else { ("", s) };
    let raw = |v: &str| {
        let (a, b) = v.split_once('/')?;
        Some(spoken!("{} προς {}", number::cardinal(a)?, number::cardinal(b)?))
    };
    if let Some(v) = raw(s) {
        return Some(spoken!("{sign}{v}"));
    }
    for (i, _) in s.char_indices().skip(1) {
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        if let (Some(n), Some(v)) = (number::cardinal(a), raw(&s[i..])) {
            return Some(spoken!("{sign}{n} και {v}"));
        }
    }
    None
}

/// A particular source `mixed_fractions` split. The direct fraction keeps the
/// lower rank when both paths read the same input.
pub(in crate::lang::el) fn mixed_fraction(s: &str, split: usize, fallback: bool) -> Option<(String, f64)> {
    let (sign, body) = if let Some(body) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        ("μείον ", body)
    } else {
        ("", s)
    };
    if split == 0 || split >= body.len() || !body.is_char_boundary(split) {
        return None;
    }
    let whole = body[..split].strip_suffix(' ').unwrap_or(&body[..split]);
    let whole = number::cardinal(whole)?;
    let fraction = &body[split..];
    let (fraction, weight) = if fallback {
        let (num, den) = fraction.split_once('/')?;
        (spoken!("{} προς {}", number::cardinal(num)?, number::cardinal(den)?), 0.5)
    } else {
        fraction_base(fraction)?
    };
    Some((spoken!("{sign}{whole} και {fraction}"), weight))
}

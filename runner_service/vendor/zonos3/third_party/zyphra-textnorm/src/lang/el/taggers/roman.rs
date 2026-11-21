use super::*;

pub(in crate::lang::el) fn roman(s: &str) -> Option<String> {
    for &(name, out) in tables::MONARCH_NAMES {
        if let Some(n) = s.strip_prefix(name).and_then(|x| x.strip_prefix(' ')) {
            if let Some(v) = lookup(tables::ROMAN_TO_ORD_M, n) {
                return Some(spoken!("{out} {v}"));
            }
            if let Some(n) = n.strip_suffix(['΄', '\'', '’']) {
                let greek = [
                    "Α", "Β", "Γ", "Δ", "Ε", "ΣΤ", "Ζ", "Η", "Θ", "Ι", "ΙΑ", "ΙΒ", "ΙΓ", "ΙΔ", "ΙΕ", "ΙΣΤ", "ΙΖ", "ΙΗ",
                    "ΙΘ", "Κ", "ΚΑ",
                ];
                if let Some(i) = greek.iter().position(|v| *v == n) {
                    return Some(spoken!("{out} {}", tables::ROMAN_TO_ORD_M[i].1));
                }
            }
        }
    }
    for &(prefix, out) in tables::CHAPTER_PREFIX {
        if let Some(n) = s.strip_prefix(prefix).and_then(|x| x.strip_prefix(' ')) {
            if let Some(v) = lookup(tables::ROMAN_TO_CARD, n) {
                return Some(spoken!("{out} {v}"));
            }
        }
    }
    for &(century, out) in tables::CENTURY_WORD {
        if let Some(n) = s.strip_prefix(century).and_then(|x| x.strip_prefix(' ')) {
            if let Some(v) = lookup(tables::ROMAN_TO_ORD_M, n) {
                return Some(spoken!("{out} {v}"));
            }
        }
        if let Some(n) = s.strip_suffix(century).and_then(|x| x.strip_suffix(' ')) {
            if let Some(v) = lookup(tables::ROMAN_TO_ORD_M, n) {
                return Some(spoken!("{v} {out}"));
            }
        }
    }
    None
}
pub(in crate::lang::el) fn roman_chapter_ordinal(s: &str) -> Option<String> {
    for &(prefix, out) in tables::CHAPTER_PREFIX {
        if let Some(n) = s.strip_prefix(prefix).and_then(|x| x.strip_prefix(' ')) {
            if let Some(v) = lookup(tables::ROMAN_TO_ORD_M, n) {
                return Some(spoken!("{out} {v}"));
            }
        }
    }
    None
}

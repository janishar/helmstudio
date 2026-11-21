//! Dot, colon and chapter ordinals.
use super::graph_utils::{is_positive, ordinal};
pub fn count(s: &str) -> usize {
    let mut n = usize::from(s.strip_suffix('.').is_some_and(is_positive));
    for suffix in [":a", ":inci", ":nci", ":üncü", ":uncu", ":ncı", ":ıncı"] {
        n += usize::from(s.strip_suffix(suffix).is_some_and(is_positive));
    }
    for suffix in ["kap", "kap.", "bölüm", "kısım"] {
        if let Some(v) = s.strip_suffix(suffix).and_then(|v| v.strip_suffix(' ')) {
            n += usize::from(is_positive(v.strip_suffix('.').unwrap_or(v)));
        }
    }
    n
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(n) = s.strip_suffix('.').and_then(ordinal) {
        out.push(n);
    }
    for suffix in [":a", ":inci", ":nci", ":üncü", ":uncu", ":ncı", ":ıncı"] {
        if let Some(n) = s.strip_suffix(suffix).and_then(ordinal) {
            out.push(n);
        }
    }
    for suffix in ["kap", "kap.", "bölüm", "kısım"] {
        if let Some(n) = s.strip_suffix(suffix).and_then(|v| v.strip_suffix(' ')) {
            let n = n.strip_suffix('.').unwrap_or(n);
            if let Some(n) = ordinal(n) {
                out.push(format!("{n} bölüm"));
            }
        }
    }
    out
}

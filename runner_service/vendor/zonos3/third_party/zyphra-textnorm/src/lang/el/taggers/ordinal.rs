use super::*;

pub(in crate::lang::el) fn parse_ordinal(s: &str) -> Option<String> {
    for suffix in ["ους", "ος", "ου", "ης", "οι", "ες", "ων", "η", "ο", "α"] {
        if let Some(x) = s.strip_suffix(suffix) {
            let x = x.strip_suffix('-').unwrap_or(x);
            if x.is_empty() || x.starts_with('0') || !x.bytes().all(|c| c.is_ascii_digit()) {
                continue;
            }
            if let Ok(n) = x.parse() {
                if let Some(v) = number::ordinal(n, suffix) {
                    return Some(v);
                }
            }
        }
    }
    None
}

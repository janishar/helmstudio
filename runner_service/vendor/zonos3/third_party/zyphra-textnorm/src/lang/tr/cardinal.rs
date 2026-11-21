//! Cardinal union branches from `tr/taggers/cardinal.py`.
use super::graph_utils::{digit_sequence, glue, is_positive, positive};

/// Count paths without constructing their spoken forms.
pub fn count(s: &str) -> usize {
    let mut n = usize::from(is_positive(s));
    if s.len() > 1 && s.starts_with('0') && s.bytes().all(|c| c.is_ascii_digit()) {
        n += 1;
    }
    if s.strip_prefix('-').or_else(|| s.strip_prefix('−')).is_some_and(is_positive) {
        n += 1;
    }
    for mark in ['~', '±'] {
        if let Some(rest) = s.strip_prefix(mark) {
            n += usize::from(is_positive(rest));
            if rest.strip_prefix('-').or_else(|| rest.strip_prefix('−')).is_some_and(is_positive) {
                n += 1;
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '\'' | '’' | '`' | '´') {
            let suffix = &s[i + c.len_utf8()..];
            if (1..=10).contains(&suffix.chars().count())
                && suffix.chars().all(|c| "abcçdefgğhıijklmnoöprsştuüvyzâîû".contains(c))
                && is_positive(&s[..i])
            {
                n += 1;
            }
        }
        if matches!(c, '-' | '–' | '—' | ':') {
            let left = &s[..i];
            let right = &s[i + c.len_utf8()..];
            let (left, right) = if c == ':' {
                (left, right)
            } else {
                (left.strip_suffix(' ').unwrap_or(left), right.strip_prefix(' ').unwrap_or(right))
            };
            if is_positive(left) && is_positive(right) {
                n += 1;
            }
        }
    }
    n
}

pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(v) = positive(s) {
        out.push(v);
    }
    if s.len() > 1 && s.starts_with('0') {
        if let Some(v) = digit_sequence(s) {
            out.push(v);
        }
    }
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        if let Some(v) = positive(rest) {
            out.push(format!("eksi {v}"));
        }
    }
    for (mark, prefix) in [('~', "yaklaşık"), ('±', "artı eksi")] {
        if let Some(rest) = s.strip_prefix(mark) {
            if let Some(v) = positive(rest) {
                out.push(format!("{prefix} {v}"));
            }
            if let Some(rest) = rest.strip_prefix('-').or_else(|| rest.strip_prefix('−')) {
                if let Some(v) = positive(rest) {
                    out.push(format!("{prefix} eksi {v}"));
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '\'' | '’' | '`' | '´') {
            let suffix = &s[i + c.len_utf8()..];
            if (1..=10).contains(&suffix.chars().count())
                && suffix.chars().all(|c| "abcçdefgğhıijklmnoöprsştuüvyzâîû".contains(c))
            {
                if let Some(v) = positive(&s[..i]) {
                    out.push(glue(&v, suffix));
                }
            }
        }
        if matches!(c, '-' | '–' | '—' | ':') {
            let left = &s[..i];
            let right = &s[i + c.len_utf8()..];
            let (left, right) = if c == ':' {
                (left, right)
            } else {
                (left.strip_suffix(' ').unwrap_or(left), right.strip_prefix(' ').unwrap_or(right))
            };
            if let (Some(a), Some(b)) = (positive(left), positive(right)) {
                out.push(format!("{a} {} {b}", if c == ':' { "iki nokta üst üste" } else { "ila" }));
            }
        }
    }
    out
}

//! Serial number, version and exponent paths.
use super::{cardinal, numeric::Reading};

/// Two short numeric serial segments have four raw/digit path costs.
pub(super) fn simple_weights(s: &str) -> Option<[f64; 4]> {
    let (a, b) = s.split_once(['-', '/', ':', '_'])?;
    fn raw_weight(s: &str) -> Option<f64> {
        if s.is_empty() || s.len() > 3 || (s.len() > 1 && s.starts_with('0')) || !s.bytes().all(|b| b.is_ascii_digit())
        {
            return None;
        }
        let n = s.parse::<u16>().ok()?;
        Some(if (101..=109).contains(&n) { 0.01 } else { 0.0 })
    }
    let a = raw_weight(a)?;
    let b = raw_weight(b)?;
    Some([a + b, a + 0.1, 0.1 + b, 0.2])
}
fn number(s: &str) -> Vec<Reading> {
    let mut out = cardinal::paths(s);
    if !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit()) {
        out.push((s.chars().map(|c| cardinal::digit(c).unwrap()).collect::<Vec<_>>().join(" "), 0.1));
    }
    out
}
fn version(s: &str) -> Vec<Reading> {
    let mut out = vec![(String::new(), 0.0)];
    for part in s.split('.') {
        let mut next = Vec::new();
        for (a, aw) in &out {
            for (b, bw) in number(part) {
                next.push((if a.is_empty() { b } else { format!("{a} punt {b}") }, aw + bw));
            }
        }
        out = next;
    }
    out
}
fn segment(s: &str) -> Vec<Reading> {
    if s.is_empty() {
        return vec![];
    }
    let mut runs = Vec::new();
    let mut start = 0;
    let mut last = None;
    for (i, c) in s.char_indices() {
        let alpha = c.is_ascii_alphabetic();
        if !alpha && !c.is_ascii_digit() && c != '.' {
            return vec![];
        }
        if let Some(prev) = last {
            if alpha != prev {
                runs.push((&s[start..i], prev));
                start = i;
            }
        }
        last = Some(alpha);
    }
    runs.push((&s[start..], last.unwrap()));
    if runs.len() > 3 {
        return vec![];
    }
    let mut out = vec![(String::new(), 0.0)];
    for (run, alpha) in runs {
        let choices = if alpha { vec![(run.to_owned(), 0.0)] } else { version(run) };
        let mut next = Vec::new();
        for (a, aw) in &out {
            for (b, bw) in &choices {
                next.push((if a.is_empty() { b.clone() } else { format!("{a} {b}") }, aw + bw));
            }
        }
        out = next;
    }
    out
}
pub(super) fn parse(s: &str) -> Vec<Reading> {
    let mut out = Vec::new();
    if s.chars().any(|c| c.is_ascii_digit()) {
        let mut result = vec![(String::new(), 0.0)];
        for part in s.split(['-', '/', ':', '_']) {
            let mut next = Vec::new();
            for (a, aw) in &result {
                for (b, bw) in segment(part) {
                    next.push((if a.is_empty() { b } else { format!("{a} {b}") }, aw + bw));
                }
            }
            result = next;
        }
        out.extend(result);
    }
    let mut chars = s.chars();
    if let Some(c) = chars.next().filter(|c| c.is_ascii_alphabetic()) {
        let rest = chars.as_str();
        if rest == "^2" || rest == "²" {
            out.push((format!("{c} kwadraat"), 0.0));
        }
        if rest == "^3" || rest == "³" {
            out.push((format!("{c} tot de derde macht"), 0.0));
        }
        if let Some(n) = rest.strip_prefix('^') {
            for (v, w) in number(n) {
                out.push((format!("{c} tot de macht {v}"), w));
            }
        }
    }
    out
}

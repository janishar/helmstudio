//! Dutch local and international digit sequences.
use super::{cardinal, numeric::Reading};
fn digits(s: &str, min: usize, max: usize) -> Option<String> {
    if s.len() < min || s.len() > max {
        return None;
    }
    s.chars().map(cardinal::digit).collect::<Option<Vec<_>>>().map(|v| v.join(" "))
}
fn sep(c: char) -> bool {
    matches!(c, '-' | ' ' | '.' | '/')
}
fn part(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(v) = digits(s, 1, 4) {
        out.push(v);
    }
    if s == "800" {
        out.push("achthonderd".to_owned());
    }
    out
}
fn combinations(groups: &[&str]) -> Vec<String> {
    let mut result = vec![String::new()];
    for group in groups {
        let parts = part(group);
        let mut next = Vec::new();
        for a in &result {
            for b in &parts {
                next.push(if a.is_empty() { b.clone() } else { format!("{a} {b}") });
            }
        }
        result = next;
    }
    result
}
pub(super) fn parse(s: &str) -> Vec<Reading> {
    let mut out = Vec::new();
    if let Some(rest) = s.strip_prefix('+') {
        let mut count = 0;
        let mut words = Vec::new();
        let mut valid = true;
        for c in rest.chars() {
            if let Some(w) = cardinal::digit(c) {
                count += 1;
                words.push(w);
            } else if !matches!(c, ' ' | '-' | '.' | '(' | ')') {
                valid = false;
                break;
            }
        }
        if valid && (7..=15).contains(&count) {
            out.push((format!("plus {}", words.join(" ")), 0.90));
        }
        let groups: Vec<_> = rest.split(sep).collect();
        if (3..=6).contains(&groups.len()) {
            if let Some(country) = digits(groups[0], 1, 3) {
                for v in combinations(&groups[1..]) {
                    out.push((format!("plus {country}{v}"), 0.95));
                }
            }
        }
    }
    if let Some(rest) = s.strip_prefix('(') {
        if let Some((area, body)) = rest.split_once(')') {
            let body = if body.starts_with(sep) { &body[body.chars().next().unwrap().len_utf8()..] } else { body };
            let groups: Vec<_> = body.split(sep).collect();
            if (2..=4).contains(&groups.len()) {
                if let Some(a) = digits(area, 2, 4) {
                    for v in combinations(&groups) {
                        out.push((format!("{a} {v}"), 0.96));
                    }
                }
            }
        }
    }
    let parts: Vec<_> = s.split(sep).collect();
    if parts.len() == 2 {
        if let (Some(a), Some(b)) = (digits(parts[0], 2, 4), digits(parts[1], 3, 8)) {
            out.push((format!("{a} {b}"), 0.98));
        }
    }
    out
}

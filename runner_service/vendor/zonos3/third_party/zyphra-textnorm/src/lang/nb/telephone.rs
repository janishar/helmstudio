use super::{basic, number};
fn group(s: &str) -> Option<String> {
    number::cardinal(s, false).or_else(|| {
        let x = s.strip_prefix('0')?;
        if x.len() != 1 {
            return None;
        }
        Some(format!("null {}", basic::digits(x)?))
    })
}
fn group_three(s: &str) -> Option<String> {
    number::cardinal(s, false).or_else(|| Some(format!("null {}", group(s.strip_prefix('0')?)?)))
}
fn country(s: &str) -> Option<String> {
    Some(format!("pluss {}", number::cardinal(s.strip_prefix('+')?, false)?))
}
fn norwegian(parts: &[&str]) -> Option<String> {
    if parts.len() != 4 {
        return None;
    }
    Some(parts.iter().map(|p| group(p)).collect::<Option<Vec<_>>>()?.join(" "))
}
pub fn parse(s: &str) -> Vec<(String, f64)> {
    // Every local and international branch uses only ASCII digits and these
    // separators. Reject other spans before constructing split collections.
    if !s.bytes().all(|b| b.is_ascii_digit() || matches!(b, b'+' | b' ' | b'-' | b'.' | b'(' | b')')) {
        return Vec::new();
    }
    let mut v = Vec::new();
    if let Some(rest) = s.strip_prefix('+') {
        if rest.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
            let digits: String = rest.chars().filter(char::is_ascii_digit).collect();
            if (7..=15).contains(&digits.len()) {
                if let Some(x) = basic::digits(&digits) {
                    v.push((format!("pluss {x}"), 0.0));
                }
            }
        }
    }
    let mut parts = [""; 5];
    let mut count = 0;
    for part in s.split(' ') {
        if count == parts.len() {
            count = 0;
            break;
        }
        parts[count] = part;
        count += 1;
    }
    let p = &parts[..count];
    if let Some(x) = norwegian(&p) {
        v.push((x, 0.0));
    }
    if p.len() == 5 {
        if let Some(c) = country(p[0]) {
            if let Some(n) = norwegian(&p[1..]) {
                v.push((format!("{c}, {n}"), 0.0));
            }
            if let (Some(a), Some(b), Some(d), Some(e)) = (group(p[1]), group(p[2]), group(p[3]), group(p[4])) {
                v.push((format!("{c} {a}, {b} {d} {e}"), 0.0));
            }
        }
    }
    if p.len() == 4 && p[3].is_ascii() {
        if let Some(c) = country(p[0]) {
            for i in 1..p[3].len() {
                if let (Some(a), Some(b), Some(c1), Some(d)) =
                    (group_three(p[1]), group_three(p[2]), group(&p[3][..i]), group(&p[3][i..]))
                {
                    v.push((format!("{c} {a}, {b} {c1} {d}"), 0.0));
                }
            }
        }
    }
    if p.len() == 3 {
        if let Some((a, b)) = p[0].split_once('-') {
            if let (Some(a), Some(b), Some(c), Some(d)) = (group_three(a), group_three(b), group(p[1]), group(p[2])) {
                v.push((format!("{a}, {b} {c} {d}"), 0.0));
            }
        }
    }
    if p.len() == 2 {
        if let Some(a) = p[0].strip_prefix('(').and_then(|x| x.strip_suffix(')')) {
            if let Some((b, cd)) = p[1].split_once('-') {
                if cd.is_ascii() {
                    for i in 1..cd.len() {
                        if let (Some(a), Some(b), Some(c), Some(d)) =
                            (group_three(a), group_three(b), group(&cd[..i]), group(&cd[i..]))
                        {
                            v.push((format!("{a}, {b} {c} {d}"), 0.0));
                        }
                    }
                }
            }
        }
    }
    let mut parts = [""; 4];
    let mut count = 0;
    for part in s.split('-') {
        if count == parts.len() {
            count = 0;
            break;
        }
        parts[count] = part;
        count += 1;
    }
    let p = &parts[..count];
    if p.len() == 4 && p[3].is_ascii() {
        if let (Some(c), Some(a), Some(b)) = (country(p[0]), group_three(p[1]), group_three(p[2])) {
            for i in 1..p[3].len() {
                if let (Some(x), Some(y)) = (group(&p[3][..i]), group(&p[3][i..])) {
                    v.push((format!("{c} {a}, {b} {x} {y}"), 0.0));
                }
            }
        }
    }
    v
}

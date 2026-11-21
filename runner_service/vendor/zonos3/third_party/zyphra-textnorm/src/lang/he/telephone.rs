//! Digit-preserving local and international telephone shapes.
const D: [&str; 10] = ["אפס", "אחת", "שתיים", "שלוש", "ארבע", "חמש", "שש", "שבע", "שמונה", "תשע"];
fn digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    Some(s.bytes().map(|b| D[(b - b'0') as usize]).collect::<Vec<_>>().join(" "))
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if let Some(p) = s.strip_prefix('+') {
        if p.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
            let n: String = p.chars().filter(|c| c.is_ascii_digit()).collect();
            if (7..=15).contains(&n.len()) {
                if let Some(v) = digits(&n) {
                    out.push((format!("פלוס {v}"), 850_000));
                }
            }
        }
    }
    // Country code is immediately followed by the area code; the source's country digits can end at every split.
    let mut prefixes = vec![("".to_owned(), s)];
    if let Some(p) = s.strip_prefix('+') {
        for (i, _) in p.char_indices().skip(1) {
            if let Some(n) = digits(&p[..i]) {
                prefixes.push((format!("פלוס {n}, "), &p[i..]));
            }
        }
    }
    for (prefix, rest) in prefixes {
        let mut areas = Vec::new();
        if let Some(p) = rest.strip_prefix('(') {
            if let Some((a, tail)) = p.split_once(')') {
                if let Some(a) = digits(a) {
                    areas.push((a, tail.strip_prefix(' ').unwrap_or(tail)));
                }
            }
        }
        for (i, c) in rest.char_indices() {
            if matches!(c, '-' | ' ') {
                if let Some(a) = digits(&rest[..i]) {
                    areas.push((a, &rest[i + 1..]));
                }
            }
        }
        for (area, tail) in areas {
            let parts: Vec<_> = tail.split(|c| c == '-' || c == ' ').collect();
            if parts.iter().any(|p| p.is_empty()) {
                continue;
            }
            let local = (parts.len() == 1 && (6..=8).contains(&parts[0].len()))
                || (parts.len() == 2 && parts.iter().all(|p| (2..=4).contains(&p.len())))
                || ((2..=4).contains(&parts.len()) && parts.iter().all(|p| (2..=4).contains(&p.len())));
            if !local {
                continue;
            }
            let names: Option<Vec<_>> = parts.iter().map(|p| digits(p)).collect();
            if let Some(names) = names {
                out.push((format!("{prefix}{area}, {}", names.join(", ")), 950_000));
            }
        }
    }
    out
}

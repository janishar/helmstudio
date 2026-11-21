use super::graph_utils::digit_word;
fn alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || ('\u{10d0}'..='\u{10fa}').contains(&c) || ('\u{1c90}'..='\u{1cba}').contains(&c)
}
fn allowed(c: char) -> bool {
    alpha(c) || c.is_ascii_digit() || "@./-_:?=&#%~+".contains(c)
}
fn domain(s: &str) -> bool {
    s.split('.').count() >= 2
        && s.split('.').all(|x| !x.is_empty() && x.chars().all(|c| allowed(c) && c != '@' && c != '.'))
}
pub fn electronic(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some((u, d)) = s.split_once('@') {
        if !u.is_empty() && u.chars().all(|c| allowed(c) && c != '@') && domain(d) {
            out.push(format!("electronic {{ username: \"{u}\" domain: \"{d}\" preserve_order: true }}"));
        }
    }
    for protocol in ["https://www.", "http://www.", "https://", "http://"] {
        if let Some(d) = s.strip_prefix(protocol) {
            if !d.is_empty() && d.chars().all(|c| allowed(c) && c != '@') {
                out.push(format!("electronic {{ protocol: \"{protocol}\" domain: \"{d}\" preserve_order: true }}"));
            }
        }
    }
    if let Some(d) = s.strip_prefix("www.") {
        if domain(d) {
            out.push(format!("electronic {{ protocol: \"www.\" domain: \"{d}\" preserve_order: true }}"));
        }
    }
    if domain(s) {
        let Some((prefix, last)) = s.split_once('.') else { return out };
        let (tld, path) = last.split_once('/').map_or((last, None), |(a, b)| (a, Some(b)));
        let path_ok = path.map_or(true, |p| p.chars().all(allowed));
        if !prefix.is_empty() && tld.chars().count() >= 2 && tld.chars().all(alpha) && path_ok {
            out.push(format!("electronic {{ domain: \"{s}\" preserve_order: true }}"));
        }
    }
    out
}
fn digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut out = String::with_capacity(s.len() * 16);
    for (i, digit) in s.bytes().enumerate() {
        if i != 0 {
            out.push(' ');
        }
        out.push_str(digit_word(digit));
    }
    Some(out)
}
fn groups(s: &str) -> Option<String> {
    let (prefix, rest) = if let Some(x) = s.strip_prefix('(') {
        let (p, r) = x.split_once(')')?;
        if p.is_empty() || p.len() > 8 {
            return None;
        }
        let r = r.strip_prefix(' ').unwrap_or(r);
        (Some(digits(p)?), r)
    } else {
        (None, s)
    };
    let mut parts = Vec::new();
    if let Some(p) = prefix {
        parts.push(p);
    }
    for p in rest.split([' ', '-', '.', '–', '—', '֊']) {
        if p.is_empty() || p.len() > 8 {
            return None;
        }
        parts.push(digits(p)?);
    }
    if parts.len() < 2 {
        return None;
    }
    Some(parts.join(", "))
}
fn paren_local(s: &str) -> bool {
    let Some(rest) = s.strip_prefix('(') else { return false };
    let Some((area, rest)) = rest.split_once(')') else { return false };
    if !(2..=5).contains(&area.len()) || !area.bytes().all(|b| b.is_ascii_digit()) {
        return false;
    }
    let rest = rest.strip_prefix(' ').unwrap_or(rest);
    if (6..=8).contains(&rest.len()) && rest.bytes().all(|b| b.is_ascii_digit()) {
        return true;
    }
    let chunks: Vec<_> = rest.split([' ', '-', '.', '–', '—', '֊']).collect();
    chunks.len() >= 2
        && (2..=4).contains(&chunks[0].len())
        && (2..=6).contains(&chunks[1].len())
        && chunks.iter().skip(2).all(|v| (1..=4).contains(&v.len()))
        && chunks.iter().all(|v| v.bytes().all(|b| b.is_ascii_digit()))
}
pub fn telephone(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(rest) = s.strip_prefix('+') {
        if (7..=15).contains(&rest.len()) {
            if let Some(d) = digits(rest) {
                out.push(format!("telephone {{ number_part: \"პლუს {d}\" preserve_order: true }}"));
            }
        }
        if let Some(i) = rest.find([' ', '-']) {
            let (code, local) = rest.split_at(i);
            if (1..=3).contains(&code.len()) {
                if let (Some(code), Some(local)) = (digits(code), groups(&local[1..])) {
                    out.push(format!(
                        "telephone {{ country_code: \"პლუს {code}\" number_part: \"{local}\" preserve_order: true }}"
                    ));
                }
            }
        }
    } else if s.starts_with('0') {
        if (9..=12).contains(&s.len()) {
            if let Some(d) = digits(s) {
                out.push(format!("telephone {{ number_part: \"{d}\" preserve_order: true }}"));
            }
        }
        if s.chars().filter(|c| c.is_ascii_digit()).count() >= 7 {
            if let Some(d) = groups(s) {
                out.push(format!("telephone {{ number_part: \"{d}\" preserve_order: true }}"));
            }
        }
    } else if paren_local(s) {
        if let Some(d) = groups(s) {
            out.push(format!("telephone {{ number_part: \"{d}\" preserve_order: true }}"));
        }
    }
    out
}

use super::number;
const TLDS: &[&str] = &[
    ".com", ".org", ".net", ".edu", ".gov", ".io", ".ai", ".dev", ".app", ".co.th", ".go.th", ".ac.th", ".in.th",
    ".co.jp", ".cn", ".th",
];
fn run(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    if s.chars().all(|c| c.is_ascii_alphabetic()) {
        Some(s.to_owned())
    } else if s.chars().all(|c| c.is_ascii_digit()) {
        number::digit_words(s)
    } else {
        None
    }
}
fn body(s: &str, email: bool) -> Option<String> {
    let mut result = String::new();
    let mut start = 0;
    for (i, c) in s.char_indices() {
        let spoken = match c {
            '-' => " ขีด ",
            '_' => " อันเดอร์สโกร ",
            '.' if email => " ดอท ",
            _ => continue,
        };
        result.push_str(&run(&s[start..i])?);
        result.push_str(spoken);
        start = i + c.len_utf8();
    }
    result.push_str(&run(&s[start..])?);
    Some(result)
}
fn domain(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for t in TLDS {
        if let Some(prefix) = s.strip_suffix(t) {
            if let Some(b) = body(prefix, false) {
                out.push(format!("{b}{}", t.replace('.', " ดอท ")));
            }
        }
    }
    out
}
fn url(s: &str) -> Option<String> {
    let (p, mut rest) =
        ["http://", "https://", "HTTP://", "HTTPS://"].iter().find_map(|p| s.strip_prefix(p).map(|r| (*p, r)))?;
    let mut out = format!("{} โคลอน สแลช สแลช ", &p[..p.len() - 3]);
    let end = rest.find('/').unwrap_or(rest.len());
    let hostport = &rest[..end];
    rest = &rest[end..];
    let mut hp = hostport.split(':');
    let host = hp.next()?;
    let hosts: Option<Vec<String>> = host.split('.').map(|x| body(x, false)).collect();
    out.push_str(&hosts?.join(" ดอท "));
    for p in hp {
        out.push_str(" โคลอน ");
        out.push_str(&run(p)?);
        if !p.chars().all(|c| c.is_ascii_digit()) {
            return None;
        }
    }
    if !rest.is_empty() {
        for elem in rest.strip_prefix('/')?.split('/') {
            out.push_str(" สแลช ");
            if let Some(n) = elem.strip_prefix('v').filter(|n| n.len() == 1 && n.chars().all(|c| c.is_ascii_digit())) {
                out.push_str("v ");
                out.push_str(&number::positive(n)?);
            } else {
                out.push_str(&body(elem, false)?);
            }
        }
    }
    Some(out)
}
pub(crate) fn parse(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if let Some(x) = url(s) {
        out.push((x, 1_010_000));
    }
    if let Some((name, host)) = s.split_once('@') {
        if let Some(name) = body(name, true) {
            for host in domain(host) {
                out.push((format!("{name} แอท {host}"), 1_020_000));
            }
        }
    }
    for d in domain(s) {
        out.push((d, 1_030_000));
    }
    let mut parts = s.split('.');
    if let (Some(a), Some(b), Some(c), Some(d), None) =
        (parts.next(), parts.next(), parts.next(), parts.next(), parts.next())
    {
        if [a, b, c, d].iter().all(|x| (1..=3).contains(&x.len()) && x.bytes().all(|c| c.is_ascii_digit())) {
            let mut value = String::new();
            for (i, part) in [a, b, c, d].iter().enumerate() {
                if i != 0 {
                    value.push_str("จุด");
                }
                value.push_str(&number::digit_words(part).unwrap());
            }
            out.push((value, 1_040_000));
        }
    }
    out
}

use super::numeric::digits;
fn read(s: &str, allowed: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    if !super::building() {
        return s.chars().all(|c| c.is_ascii_alphanumeric() || allowed.contains(c)).then(String::new);
    }
    let mut out = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            out.push(c)
        } else if c.is_ascii_digit() {
            out.push(' ');
            out.push_str(&digits(&c.to_string())?);
            out.push(' ')
        } else if allowed.contains(c) {
            out.push_str(match c {
                '.' => " bodka ",
                '/' => " lomka ",
                ':' => " dvojbodka ",
                '-' => " pomlčka ",
                '_' => " podčiarkovník ",
                '?' => " otáznik ",
                '=' => " rovná sa ",
                '&' => " a ",
                '#' => " mriežka ",
                '%' => " percent ",
                '+' => " plus ",
                '~' => " tilda ",
                _ => return None,
            })
        } else {
            return None;
        }
    }
    Some(out)
}
fn label(s: &str) -> bool {
    !s.is_empty() && s.chars().all(|c| c.is_ascii_alphanumeric() || c == '-')
}
fn host(s: &str) -> bool {
    let Some((prefix, tld)) = s.rsplit_once('.') else { return false };
    tld.len() >= 2 && tld.chars().all(|c| c.is_ascii_alphabetic()) && prefix.split('.').all(label)
}
fn numeric_piece(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|b| b.is_ascii_digit())
}
fn version(s: &str, count: usize) -> Option<String> {
    if s.split('.').count() < count || !s.split('.').all(numeric_piece) {
        return None;
    }
    if !super::building() {
        return Some(String::new());
    }
    Some(s.split('.').map(digits).collect::<Option<Vec<_>>>()?.join(" bodka "))
}
pub(super) fn electronic(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (prefix, spoken) in [
        ("https://", "h t t p s dvojbodka lomka lomka "),
        ("http://", "h t t p dvojbodka lomka lomka "),
        ("www.", "w w w bodka "),
    ] {
        if let Some(x) = s.strip_prefix(prefix).and_then(|x| read(x, "./:-_?=&#%+~")) {
            out.push(spoken!("{spoken}{x}"));
        }
    }
    if let Some((a, b)) = s.split_once('@') {
        if host(b) {
            if let (Some(a), Some(b)) = (read(a, ".-_+"), read(b, ".-")) {
                out.push(spoken!("{a} zavináč {b}"));
            }
        }
    }
    if host(s) {
        if let Some(v) = read(s, ".-") {
            out.push(v);
        }
    }
    let mut pieces = s.split('.');
    let a = pieces.next().unwrap_or("");
    let b = pieces.next();
    let c = pieces.next();
    let d = pieces.next();
    let more = pieces.next().is_some();
    if let (Some(b), Some(c), Some(d), false) = (b, c, d, more) {
        let p = [a, b, c, d];
        if p.iter().all(|x| {
            numeric_piece(x) && !((x.len() > 1) && x.starts_with('0')) && x.parse::<u16>().is_ok_and(|n| n <= 255)
        }) {
            out.push(if super::building() {
                p.iter().map(|x| digits(x).unwrap()).collect::<Vec<_>>().join(" bodka ")
            } else {
                String::new()
            });
        }
    }
    if let (Some(b), Some(c), None, false) = (b, c, d, more) {
        if [a, b, c].iter().all(|x| numeric_piece(x)) {
            let grouped = a.len() <= 3 && !a.starts_with('0') && b.len() == 3 && c.len() == 3;
            let date = a.len() <= 2 && b.len() <= 2 && (2..=4).contains(&c.len());
            if !grouped && !date {
                if let Some(x) = version(s, 3) {
                    out.push(x);
                }
            }
        }
    }
    if let Some(x) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) {
        if let Some(x) = version(x, 2) {
            out.push(spoken!("verzia {x}"));
        }
    }
    if let Some((name, v)) = s.split_once(' ') {
        if !name.is_empty() && name.chars().all(|c| c.is_ascii_alphabetic()) {
            if let Some(v) = version(v, 2) {
                out.push(spoken!("{name} {v}"));
            }
        }
    }
    out
}
pub(super) fn telephone(s: &str) -> Option<String> {
    let (intl, body) = if let Some(x) = s.strip_prefix('+') { (true, x) } else { (false, s) };
    if !body.chars().all(|c| c.is_ascii_digit() || if intl { " - .()".contains(c) } else { " -".contains(c) }) {
        return None;
    }
    if !intl && (!body.starts_with(|c: char| c.is_ascii_digit()) || !body.ends_with(|c: char| c.is_ascii_digit())) {
        return None;
    }
    let count = body.bytes().filter(|b| b.is_ascii_digit()).count();
    if if intl { !(7..=15).contains(&count) } else { count != 10 } {
        return None;
    }
    if !super::building() {
        return Some(String::new());
    }
    let d: String = body.chars().filter(|c| c.is_ascii_digit()).collect();
    Some(spoken!("{}{}", if intl { "plus " } else { "" }, digits(&d)?))
}

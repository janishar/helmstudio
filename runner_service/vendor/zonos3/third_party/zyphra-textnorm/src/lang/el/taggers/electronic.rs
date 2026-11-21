use super::*;

pub(in crate::lang::el) fn electronic(s: &str) -> Option<String> {
    let ascii_letters = |s: &str| !s.is_empty() && s.bytes().all(|c| c.is_ascii_alphabetic());
    let chunks = |s: &str, separators: &[char]| {
        !s.is_empty()
            && s.split(separators).all(|part| !part.is_empty() && part.bytes().all(|c| c.is_ascii_alphanumeric()))
    };
    let ip = s.split('.');
    if ip.clone().count() == 4
        && ip.clone().all(|x| (1..=3).contains(&x.len()) && x.bytes().all(|c| c.is_ascii_digit()))
    {
        if !writing_tags() {
            return Some(String::new());
        }
        return ip.map(number::digits).collect::<Option<Vec<_>>>().map(|x| x.join(" τελεία "));
    }
    let (prefix, body) = if let Some(x) = s.strip_prefix("https://") {
        if !chunks(x, &['.', '/', ':', '?', '=', '&', '-', '_']) {
            return None;
        }
        ("https κάθετος κάθετος ", x)
    } else if let Some(x) = s.strip_prefix("http://") {
        if !chunks(x, &['.', '/', ':', '?', '=', '&', '-', '_']) {
            return None;
        }
        ("http κάθετος κάθετος ", x)
    } else if let Some((user, domain)) = s.split_once('@') {
        let (a, b) = domain.split_once('.')?;
        if !chunks(user, &['.', '_', '-']) || !ascii_letters(a) || !ascii_letters(b) {
            return None;
        }
        ("", s)
    } else if let Some((domain, tld)) = s.split_once('.') {
        if !ascii_letters(domain) || !["dev", "ai", "io", "com", "org", "net", "gr", "edu", "gov"].contains(&tld) {
            return None;
        }
        ("", s)
    } else {
        return None;
    };
    if !writing_tags() {
        return Some(String::new());
    }
    let mut out = prefix.to_string();
    for c in body.chars() {
        if c.is_ascii_digit() {
            out.push_str(&number::digits(&c.to_string())?);
            out.push(' ');
            continue;
        }
        let spoken = match c {
            '@' => Some(" παπάκι "),
            '.' => Some(" τελεία "),
            '/' => Some(" κάθετος "),
            ':' => Some(" άνω κάτω τελεία "),
            '?' => Some(" ερωτηματικό "),
            '=' => Some(" ίσον "),
            '&' => Some(" και "),
            '-' => Some(" παύλα "),
            '_' => Some(" κάτω παύλα "),
            _ => None,
        };
        if let Some(v) = spoken {
            out.push_str(v);
        } else {
            out.push(c);
        }
    }
    // Separators and digit transductions intentionally retain their source spaces.
    Some(out)
}

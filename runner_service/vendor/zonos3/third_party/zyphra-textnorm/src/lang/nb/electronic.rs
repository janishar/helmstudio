use super::{basic, number};
fn octet(s: &str) -> Option<String> {
    if s.len() > 3 { None } else { number::cardinal(s, false) }
}
fn symbol(c: char) -> Option<&'static str> {
    Some(match c {
        '.' => " punktum ",
        ':' => " kolon ",
        '/' => " skråstrek ",
        '?' => " spørsmålstegn ",
        '=' => " lik ",
        '&' => " og ",
        '#' => " nummer ",
        '%' => " prosent ",
        '-' => " bindestrek ",
        '_' => " understrek ",
        '+' => " pluss ",
        '~' => " tilde ",
        _ => return None,
    })
}
fn render(s: &str, allowed: &str) -> Option<String> {
    let mut out = String::new();
    if s.is_empty() {
        return None;
    }
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            out.push(c)
        } else if c.is_ascii_digit() {
            out.push(' ');
            out.push_str(&basic::digits(&c.to_string())?);
            out.push(' ')
        } else if allowed.contains(c) {
            out.push_str(symbol(c)?)
        } else {
            return None;
        }
    }
    Some(out)
}
pub fn parse(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (p, w) in [("http://", "h t t p kolon dobbelt skråstrek "), ("https://", "h t t p s kolon dobbelt skråstrek ")]
    {
        if let Some(rest) = s.strip_prefix(p).and_then(|x| render(x, ".:/?=&#%-_+~")) {
            out.push((format!("{w}{rest}"), 0.0));
        }
    }
    for (suffix, word) in
        [(".dev", "d e v"), (".ai", "a i"), (".io", "i o"), (".no", "n o"), (".com", "com"), (".org", "org")]
    {
        if let Some(x) = s.strip_suffix(suffix) {
            if !x.is_empty() && x.chars().all(|c| c.is_ascii_alphabetic()) {
                out.push((format!("{x} punktum {word}"), 0.0));
            }
        }
    }
    if let Some((local, host)) = s.split_once('@') {
        let labels: Vec<&str> = host.split('.').collect();
        if labels.len() > 1 {
            if let (Some(a), Some(parts)) =
                (render(local, "._-+"), labels.iter().map(|x| render(x, "-")).collect::<Option<Vec<_>>>())
            {
                out.push((format!("{a} krøllalfa {}", parts.join(" punktum ")), 0.0));
            }
        }
    }
    let mut parts = s.split('.');
    let first = parts.next();
    let second = parts.next();
    let third = parts.next();
    let fourth = parts.next();
    if parts.next().is_none() {
        if let (Some(a), Some(b), Some(c)) = (first, second, third) {
            if let (Some(a), Some(b), Some(c)) = (octet(a), octet(b), octet(c)) {
                if let Some(d) = fourth {
                    if let Some(d) = octet(d) {
                        out.push((format!("{a} punktum {b} punktum {c} punktum {d}"), -0.1));
                    }
                } else {
                    out.push((format!("{a} punktum {b} punktum {c}"), 0.0));
                }
            }
        }
    }
    if let Some(x) = s.strip_prefix("iOS ") {
        let p: Vec<&str> = x.split('.').collect();
        if p.len() == 3 {
            if let Some(parts) = p.iter().map(|x| octet(x)).collect::<Option<Vec<_>>>() {
                out.push((format!("iOS {}", parts.join(" punktum ")), 0.0));
            }
        }
    }
    if let Some(x) = s.strip_prefix('v') {
        if let Some((a, b)) = x.split_once('.') {
            if b.len() == 1 {
                if let (Some(a), Some(b)) = (octet(a), basic::digits(b)) {
                    out.push((format!("v {a} punktum {b}"), 0.0));
                }
            }
        }
    }
    if let Some((a, b)) = s.split_once('^') {
        let base = number::cardinal(a, false)
            .or_else(|| if a.len() == 1 && a.chars().all(|x| x.is_ascii_alphabetic()) { Some(a.into()) } else { None });
        if let Some(a) = base {
            if b == "2" {
                out.push((format!("{a} i andre"), 0.0));
            }
            if b == "3" {
                out.push((format!("{a} i tredje"), 0.0));
            }
            if let Some(b) = number::cardinal(b, false) {
                out.push((format!("{a} opphøyd i {b}"), 0.0));
            }
        }
    }
    // Alternating letter and cardinal runs, with an optional hyphen at each join.
    let bytes = s.as_bytes();
    if !bytes.is_empty() && bytes.iter().all(|c| c.is_ascii_alphanumeric() || *c == b'-') {
        if bytes[0].is_ascii_digit() {
            let i = bytes.iter().position(|x| !x.is_ascii_digit()).unwrap_or(bytes.len());
            if i < bytes.len() && bytes[i..].iter().all(u8::is_ascii_alphabetic) {
                if let Some(n) = number::cardinal(&s[..i], false) {
                    out.push((format!("{n} {}", &s[i..]), 0.0));
                }
            }
        }
        if bytes[0].is_ascii_alphabetic() {
            let mut i = 0;
            while i < bytes.len() && bytes[i].is_ascii_alphabetic() {
                i += 1
            }
            let mut rendered = s[..i].to_string();
            let mut count = 0;
            let mut ok = true;
            while i < bytes.len() {
                let hyphen = bytes[i] == b'-';
                if hyphen {
                    i += 1;
                }
                let start = i;
                while i < bytes.len() && bytes[i].is_ascii_digit() {
                    i += 1
                }
                if start == i {
                    ok = false;
                    break;
                }
                let Some(n) = number::cardinal(&s[start..i], false) else {
                    ok = false;
                    break;
                };
                rendered.push_str(if hyphen { " bindestrek " } else { " " });
                rendered.push_str(&n);
                count += 1;
                if i < bytes.len() {
                    let hyphen = bytes[i] == b'-';
                    if hyphen {
                        i += 1;
                    }
                    let start = i;
                    while i < bytes.len() && bytes[i].is_ascii_alphabetic() {
                        i += 1
                    }
                    if start == i {
                        ok = false;
                        break;
                    }
                    rendered.push_str(if hyphen { " bindestrek " } else { " " });
                    rendered.push_str(&s[start..i]);
                }
            }
            if ok && count > 0 {
                out.push((rendered, 0.0));
            }
        }
    }
    out
}

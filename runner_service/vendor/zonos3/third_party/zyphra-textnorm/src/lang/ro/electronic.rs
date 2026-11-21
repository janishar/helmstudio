// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
use super::graph_utils::{Gender, number};
const D: [&str; 10] = ["zero", "unu", "doi", "trei", "patru", "cinci", "șase", "șapte", "opt", "nouă"];
fn chars(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_alphanumeric()) {
        return None;
    }
    Some(
        s.bytes()
            .map(|b| {
                if b.is_ascii_digit() {
                    D[(b - b'0') as usize].to_owned()
                } else {
                    (b as char).to_ascii_lowercase().to_string()
                }
            })
            .collect::<Vec<_>>()
            .join(" "),
    )
}
fn host(s: &str) -> Option<String> {
    let mut v = Vec::new();
    for p in s.split('.') {
        v.push(chars(p)?);
    }
    Some(v.join(" punct "))
}
pub fn electronic(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let parts: Vec<_> = s.split('.').collect();
    if parts.len() == 4 && parts.iter().all(|p| (1..=3).contains(&p.len())) {
        let numbers: Option<Vec<_>> = parts.iter().map(|p| number(p, Gender::Masculine)).collect();
        if let Some(n) = numbers {
            out.push(n.join(" punct "));
        }
    }
    const TLDS: &[&str] = &[
        "ro", "com", "org", "net", "edu", "gov", "eu", "info", "biz", "dev", "ai", "io", "app", "tech", "online",
        "store", "co", "uk", "de", "fr",
    ];
    if let Some((name, tld)) = s.split_once('.') {
        if TLDS.contains(&tld) {
            if let Some(name) = chars(name) {
                out.push(format!("{} punct {}", name, tld));
            }
        }
    }
    if let Some((user, domain)) = s.split_once('@') {
        if !user.is_empty() {
            if let Some((h, tld)) = domain.split_once('.') {
                if let (Some(h), Some(tld)) = (chars(h), chars(tld)) {
                    let mut elems = Vec::new();
                    let mut valid = true;
                    for c in user.chars() {
                        let v = match c {
                            '.' => " punct ".to_owned(),
                            '-' => " liniuță ".to_owned(),
                            '_' => " linie jos ".to_owned(),
                            _ => match chars(&c.to_string()) {
                                Some(v) => v,
                                None => {
                                    valid = false;
                                    break;
                                }
                            },
                        };
                        elems.push(v);
                    }
                    if valid {
                        let u = elems.join(" ");
                        out.push(format!("{} arond {} punct {}", u, h, tld));
                    }
                }
            }
        }
    }
    for (prefix, spoken) in [
        ("http://", "h t t p două puncte slash slash "),
        ("https://", "h t t p s două puncte slash slash "),
        ("ftp://", "f t p două puncte slash slash "),
        ("www.", "w w w punct "),
    ] {
        if let Some(rest) = s.strip_prefix(prefix) {
            let mut path = rest.split('/');
            let authority = path.next().unwrap_or("");
            let mut hp = authority.split(':');
            if let Some(h) = hp.next().and_then(host) {
                let mut result = format!("{}{}", spoken, h);
                let mut valid = true;
                if let Some(port) = hp.next() {
                    if port.is_empty() || !port.bytes().all(|b| b.is_ascii_digit()) || hp.next().is_some() {
                        valid = false;
                    } else {
                        result.push_str(" două puncte ");
                        result.push_str(&port.bytes().map(|b| D[(b - b'0') as usize]).collect::<Vec<_>>().join(" "));
                    }
                }
                for p in path {
                    if let Some(p) = chars(p) {
                        result.push_str(" slash ");
                        result.push_str(&p);
                    } else {
                        valid = false;
                    }
                }
                if valid {
                    out.push(result);
                }
            }
        }
    }
    out
}

//! Email, domain, URL and IPv4 pronunciation.
use super::{cardinal::base_num, serial::letters};
const WORDS: &[(&str, &str)] = &[
    ("example", "אגזמפל"),
    ("gmail", "ג'ימייל"),
    ("google", "גוגל"),
    ("yahoo", "יאהו"),
    ("hotmail", "הוטמייל"),
    ("outlook", "אאוטלוק"),
    ("localhost", "לוקלהוסט"),
    ("app", "אפ"),
    ("model", "מודל"),
    ("tool", "טול"),
    ("info", "אינפו"),
    ("admin", "אדמין"),
    ("support", "ספורט"),
    ("api", "איי פי איי"),
    ("test", "טסט"),
    ("com", "קום"),
    ("org", "אורג"),
    ("net", "נט"),
    ("edu", "אדו"),
    ("gov", "גוב"),
    ("mil", "מיל"),
    ("io", "איי או"),
    ("ai", "איי איי"),
    ("dev", "דב"),
    ("co", "קו"),
    ("il", "איי אל"),
    ("uk", "יו קיי"),
    ("de", "די אי"),
    ("fr", "אף אר"),
];
const DIGIT: [&str; 10] = ["אפס", "אחת", "שתיים", "שלוש", "ארבע", "חמש", "שש", "שבע", "שמונה", "תשע"];
fn chunk(s: &str) -> Option<(String, i64)> {
    if let Some((_, v)) = WORDS.iter().find(|(k, _)| *k == s) {
        return Some(((*v).into(), 900_000));
    }
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_alphanumeric()) {
        return None;
    }
    let mut p = Vec::new();
    for c in s.chars() {
        if c.is_ascii_digit() {
            p.push(DIGIT[c as usize - '0' as usize].to_owned())
        } else if c == 'x' || c == 'X' {
            p.push("אקס".into())
        } else {
            p.push(letters(&c.to_string())?)
        }
    }
    Some((p.join(" "), 1_100_000))
}
fn domain_name(s: &str) -> Option<String> {
    if let Some((_, v)) = WORDS.iter().find(|(k, _)| *k == s) {
        return Some((*v).into());
    }
    if !s.bytes().all(|b| b.is_ascii_alphabetic()) {
        return None;
    }
    let l = letters(s)?;
    Some(l.replace("איקס", "אקס"))
}
fn bare(s: &str) -> Option<String> {
    for (end, v) in [
        (".dev", "דב"),
        (".ai", "איי איי"),
        (".io", "איי או"),
        (".com", "קום"),
        (".org", "אורג"),
        (".net", "נט"),
        (".co.il", "קו נקודה איי אל"),
        (".org.il", "אורג נקודה איי אל"),
        (".gov.il", "גוב נקודה איי אל"),
        (".ac.il", "איי סי נקודה איי אל"),
    ] {
        if let Some(n) = s.strip_suffix(end) {
            if let Some(n) = domain_name(n) {
                return Some(format!("{n} נקודה {v}"));
            }
        }
    }
    None
}
fn seq(s: &str, allowed: &str) -> Option<(String, i64)> {
    let mut parts = Vec::new();
    let mut last = 0;
    let mut cost = 0;
    for (i, c) in s.char_indices() {
        if allowed.contains(c) {
            let (v, w) = chunk(&s[last..i])?;
            parts.push(v);
            cost += w;
            parts.push(
                match c {
                    '.' => "נקודה",
                    '/' => "סלאש",
                    ':' => "נקודתיים",
                    '?' => "סימן שאלה",
                    '=' => "שווה",
                    '&' => "וגם",
                    '-' => "מקף",
                    '_' => "קו תחתון",
                    _ => return None,
                }
                .into(),
            );
            last = i + c.len_utf8();
        }
    }
    let (v, w) = chunk(&s[last..])?;
    parts.push(v);
    cost += w;
    Some((parts.join(" "), cost))
}
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if let Some(v) = bare(s) {
        out.push((v, 1_880_000));
    }
    for (prefix, name) in [
        ("https://", "אייץ' טי טי פי אס נקודתיים סלאש סלאש"),
        ("http://", "אייץ' טי טי פי נקודתיים סלאש סלאש"),
        ("ftp://", "אף טי פי נקודתיים סלאש סלאש"),
    ] {
        if let Some(t) = s.strip_prefix(prefix) {
            if let Some((v, w)) = seq(t, "./:?=&-_") {
                out.push((format!("{name} {v}"), w + 1_840_000));
            }
        }
    }
    if let Some((u, d)) = s.split_once('@') {
        if let Some((u, uw)) = seq(u, ".-_") {
            if let Some(d) = bare(d) {
                out.push((format!("{u} שטרודל {d}"), uw + 1_860_000));
            }
            let names: Option<Vec<_>> = d.split('.').map(domain_name).collect();
            if let Some(names) = names {
                out.push((format!("{u} שטרודל {}", names.join(" נקודה ")), uw + 1_860_000));
            }
        }
    }
    let p: Vec<_> = s.split('.').collect();
    if p.len() == 4 {
        let n: Option<Vec<_>> = p.iter().map(|x| base_num(x, false)).collect();
        if let Some(n) = n {
            out.push((n.join(" נקודה "), 1_890_000));
        }
    }
    out
}

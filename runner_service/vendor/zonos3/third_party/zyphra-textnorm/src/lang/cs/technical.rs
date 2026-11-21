use super::graph_utils::{Gender, digit};
use super::numbers::cardinal;
pub fn may_cross_space(first: &str) -> bool {
    matches!(first, "iOS" | "ios" | "Android" | "android" | "Python" | "python" | "Windows" | "macOS")
}
fn num(s: &str) -> Option<String> {
    if s.len() > 3 || s.is_empty() || (!s.bytes().all(|b| b.is_ascii_digit())) || (s.len() > 1 && s.starts_with('0')) {
        return None;
    }
    cardinal(s)
}
pub fn telephone(s: &str) -> Option<String> {
    let (intl, s) = if let Some(s) = s.strip_prefix('+') { (true, s) } else { (false, s) };
    let mut words = Vec::new();
    let mut digits = 0;
    let mut last_digit = false;
    for c in s.chars() {
        if c.is_ascii_digit() {
            digits += 1;
            words.push(digit(c as usize - '0' as usize, Gender::Default));
            last_digit = true
        } else if if intl { matches!(c, ' ' | '-' | '.' | '(' | ')') } else { matches!(c, ' ' | '-' | '.') } {
            if !intl && digits == 0 {
                return None;
            }
            last_digit = false
        } else {
            return None;
        }
    }
    if (intl && (7..=15).contains(&digits)) || (!intl && digits == 9 && last_digit) {
        Some(format!("{}{}", if intl { "plus " } else { "" }, words.join(" ")))
    } else {
        None
    }
}
fn spell_chars(s: &str, email: bool) -> Option<String> {
    let mut out = String::new();
    let mut iter = s.chars().peekable();
    while let Some(c) = iter.next() {
        if c.is_ascii_alphabetic() {
            out.push(c)
        } else if c.is_ascii_digit() {
            let mut digits = c.to_string();
            if email && c != '0' {
                while digits.len() < 3 && iter.peek().is_some_and(|x| x.is_ascii_digit()) {
                    digits.push(iter.next()?)
                }
            }
            let word = num(&digits)?;
            out.push(' ');
            out.push_str(&word);
            out.push(' ')
        } else {
            out.push_str(match c {
                '.' => " tečka ",
                '-' => " pomlčka ",
                '_' => " podtržítko ",
                '/' => " lomítko ",
                ':' => " dvojtečka ",
                '?' => " otazník ",
                '=' => " rovná se ",
                '&' => " a ",
                '~' => " tilda ",
                _ => return None,
            })
        }
    }
    Some(out)
}
fn clean(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    for part in s.split_whitespace() {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(part);
    }
    out
}
fn domain(s: &str) -> Option<String> {
    let (host, tld) = s.rsplit_once('.')?;
    if !tld.bytes().all(|b| b.is_ascii_alphabetic())
        || !(tld.len() == 2 || ["com", "org", "net", "edu", "gov", "dev", "app", "info"].contains(&tld))
    {
        return None;
    }
    if host.split('.').any(|x| x.is_empty() || !x.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_')))
    {
        return None;
    }
    let mut out = spell_chars(host, false)?;
    out.push_str(" tečka ");
    out.push_str(if tld == "edu" { "e d u" } else { tld });
    Some(out)
}
fn url_tail(s: &str) -> Option<String> {
    let mut rest = s;
    let mut out = String::new();
    if let Some(after) = rest.strip_prefix(':') {
        let end = after.find('/').unwrap_or(after.len());
        let port = &after[..end];
        // The port graph is n1_999, not the single-digit graph.
        if port == "0" {
            return None;
        }
        out.push_str(" dvojtečka ");
        out.push_str(&num(port)?);
        rest = &after[end..];
    }
    if !rest.is_empty() {
        for segment in rest.strip_prefix('/')?.split('/') {
            if segment.is_empty()
                || !segment
                    .chars()
                    .all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '=' | '&' | '?' | '~' | '.'))
            {
                return None;
            }
            out.push_str(" lomítko ");
            out.push_str(&spell_chars(segment, false)?);
        }
    }
    Some(out)
}
fn email_users(s: &str) -> Vec<String> {
    fn visit(s: &str, buf: &mut String, out: &mut Vec<String>) {
        if s.is_empty() {
            out.push(clean(buf));
            return;
        }
        let c = s.as_bytes()[0];
        if c.is_ascii_digit() {
            for n in 1..=3.min(s.len()) {
                let part = &s[..n];
                if !part.bytes().all(|x| x.is_ascii_digit()) {
                    break;
                }
                if let Some(word) = num(part) {
                    let old = buf.len();
                    buf.push(' ');
                    buf.push_str(&word);
                    buf.push(' ');
                    visit(&s[n..], buf, out);
                    buf.truncate(old)
                }
            }
        } else {
            let piece = match c {
                b'a'..=b'z' | b'A'..=b'Z' => None,
                b'.' => Some(" tečka "),
                b'-' => Some(" pomlčka "),
                b'_' => Some(" podtržítko "),
                _ => return,
            };
            let old = buf.len();
            if let Some(piece) = piece {
                buf.push_str(piece)
            } else {
                buf.push(c as char)
            }
            visit(&s[1..], buf, out);
            buf.truncate(old)
        }
    }
    let mut out = Vec::new();
    if s.is_ascii() && !s.is_empty() {
        visit(s, &mut String::new(), &mut out)
    }
    out
}
pub fn electronic(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let p: Vec<_> = s.split('.').collect();
    if p.len() == 4 {
        if let Some(v) = p.iter().map(|x| num(x)).collect::<Option<Vec<_>>>() {
            out.push((v.join(" tečka "), 0.0))
        }
    }
    if let Some((user, dom)) = s.split_once('@') {
        if let Some(dom) = domain(dom) {
            for user in email_users(user) {
                out.push((clean(&format!("{user} zavináč {dom}")), 0.0))
            }
        }
    }
    let mut protocols = vec![("", s, false)];
    for (input, spoken) in [
        ("https://", "h t t p s dvojtečka dvě lomítka "),
        ("http://", "h t t p dvojtečka dvě lomítka "),
        ("www.", "w w w tečka "),
    ] {
        if let Some(rest) = s.strip_prefix(input) {
            protocols.push((spoken, rest, true))
        }
    }
    for (prefix, rest, has_protocol) in protocols {
        let cut = rest.find(|c| c == '/' || c == ':').unwrap_or(rest.len());
        let host = &rest[..cut];
        let tail = &rest[cut..];
        if let (Some(host), Some(tail)) = (domain(host), url_tail(tail)) {
            out.push((clean(&format!("{prefix}{host}{tail}")), 0.0))
        }
        if has_protocol
            && !rest.is_empty()
            && rest.is_ascii()
            && rest
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '=' | '&' | '?' | '~' | '.' | '/' | ':'))
        {
            if let Some(rest) = spell_chars(rest, false) {
                out.push((clean(&format!("{prefix}{rest}")), 0.5))
            }
        }
    }
    out
}
const FIXED: &[(&str, &str)] = &[
    ("COVID-19", "COVID devatenáct"),
    ("covid-19", "covid devatenáct"),
    ("5G", "pět G"),
    ("5g", "pět g"),
    ("4K", "čtyři K"),
    ("4k", "čtyři k"),
    ("CO2", "C O dva"),
    ("A4", "A čtyři"),
    ("A3", "A tři"),
    ("A5", "A pět"),
    ("BA249", "BA dvě stě čtyřicet devět"),
    ("B-52", "B padesát dva"),
    ("MP3", "M P tři"),
    ("H2O", "H dva O"),
];
fn version(s: &str, count: std::ops::RangeInclusive<usize>) -> Option<String> {
    let p: Vec<_> = s.split('.').collect();
    if !count.contains(&p.len()) {
        return None;
    }
    if p.len() >= 3
        && p[0].len() <= 3
        && p.iter().skip(1).all(|x| x.len() == 3 && x.bytes().all(|b| b.is_ascii_digit()))
    {
        return None;
    }
    Some(p.iter().map(|x| num(x)).collect::<Option<Vec<_>>>()?.join(" tečka "))
}
pub fn serial(s: &str) -> Option<String> {
    if let Some((_, v)) = FIXED.iter().find(|(k, _)| *k == s) {
        return Some((*v).into());
    }
    if let Some(v) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) {
        if let Some(v) = version(v, 2..=2) {
            return Some(format!("verze {v}"));
        }
    }
    if let Some((name, v)) = s.split_once(' ') {
        if ["iOS", "ios", "Android", "android", "Python", "python", "Windows", "macOS"].contains(&name) {
            if let Some(v) = version(v, 2..=3) {
                return Some(format!("{name} {v}"));
            }
        }
    }
    if let Some(v) = version(s, 3..=4) {
        return Some(v);
    }
    if let Some((base, p)) = s.split_once('^') {
        let power = match p {
            "2" => "druhou",
            "3" => "třetí",
            "4" => "čtvrtou",
            "5" => "pátou",
            "6" => "šestou",
            "7" => "sedmou",
            "8" => "osmou",
            "9" => "devátou",
            "10" => "desátou",
            _ => return None,
        };
        let b = if ["x", "X", "y", "Y", "z", "Z", "a", "b", "c", "n"].contains(&base) {
            base.to_owned()
        } else {
            cardinal(base)?
        };
        return Some(format!("{b} na {power}"));
    }
    let p: Vec<_> = s.split('-').collect();
    if p.len() == 3
        && !p[0].is_empty()
        && !p[2].is_empty()
        && p[0].bytes().all(|b| b.is_ascii_uppercase())
        && p[2].bytes().all(|b| b.is_ascii_uppercase())
    {
        return Some(format!("{} {} {}", p[0], num(p[1])?, p[2]));
    }
    None
}

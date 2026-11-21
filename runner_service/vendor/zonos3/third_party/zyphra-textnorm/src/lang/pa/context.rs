//! Whitelist, Roman, serial and electronic classifiers.
use super::graph_utils::{cardinal, cardinal_input_weight, digit, digitwise, lookup, number, number_weight};
use crate::text::{Cost, cost};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;
const ABBR: &str = include_str!("../../../data/pa/whitelist/abbreviations.tsv");
const ROMAN: &str = include_str!("../../../data/pa/roman/roman_to_spoken.tsv");
const ROMAN_EX: &str = include_str!("../../../data/pa/roman/roman_ordinal_exceptions.tsv");
const SUFFIX: &str = include_str!("../../../data/pa/ordinal/suffixes.tsv");
const LETTERS: &str = include_str!("../../../data/pa/address/letters.tsv");
const POWERS: &str = include_str!("../../../data/pa/serial/power_special.tsv");
const SERIAL_SYMBOLS: &str = include_str!("../../../data/pa/serial/special_symbols.tsv");
const PA_CHARS: &str = include_str!("../../../data/pa/serial/chars.tsv");
const DOMAINS: &str = include_str!("../../../data/pa/electronic/domain.tsv");
const EXTENSIONS: &str = include_str!("../../../data/pa/electronic/file_extensions.tsv");
const CLASSES: &str = include_str!("../../../data/pa/electronic/symbol_classes.tsv");
const ELEMENTS: &str = include_str!("../../../data/pa/electronic/elements.tsv");
static ABBR_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for row in ABBR.lines() {
        if let Some((key, _)) = row.split_once('\t') {
            for (i, _) in key.char_indices().skip(1) {
                prefixes.insert(&key[..i]);
            }
            prefixes.insert(key);
        }
    }
    prefixes
});

fn name(value: &str) -> String {
    format!("name: \"{}\"", value.replace(' ', "\u{a0}"))
}
pub(crate) fn whitelist_prefix(s: &str) -> bool {
    if ABBR_PREFIXES.contains(s) {
        return true;
    }
    for key in ["§", "©", "ਨੰ.", "ਨੰ", "No.", "Nº", "№", "#"] {
        if key.starts_with(s) {
            return true;
        }
        if let Some(rest) = s.strip_prefix(key) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            if rest.chars().all(|c| digit(c).is_some() || c == ',') {
                return true;
            }
        }
    }
    s.as_bytes().first().is_some_and(u8::is_ascii_uppercase)
        && s.chars().all(|c| c.is_ascii_uppercase() || c == '.' || c == ' ')
}
pub(crate) fn whitelist_cost(s: &str) -> Option<Cost> {
    if lookup(ABBR, s).is_some() {
        return Some(0);
    }
    for key in ["§", "©", "ਨੰ.", "ਨੰ", "No.", "Nº", "№", "#"] {
        if let Some(rest) = s.strip_prefix(key) {
            return number_weight(rest.strip_prefix(' ').unwrap_or(rest));
        }
    }
    if s.as_bytes().first().is_some_and(u8::is_ascii_uppercase) {
        let mut count = 0;
        for part in s.trim_end_matches('.').split('.').map(|x| x.strip_prefix(' ').unwrap_or(x)) {
            if part.len() != 1 || !part.as_bytes()[0].is_ascii_uppercase() {
                return None;
            }
            count += 1;
        }
        if count >= 3 {
            return Some(0);
        }
    }
    None
}
pub(crate) fn whitelist_tag(s: &str) -> Option<(String, Cost)> {
    if let Some(v) = lookup(ABBR, s) {
        return Some((name(v), 0));
    }
    for (key, value) in [
        ("§", "ਧਾਰਾ"),
        ("©", "ਕਾਪੀਰਾਈਟ"),
        ("ਨੰ.", "ਨੰਬਰ"),
        ("ਨੰ", "ਨੰਬਰ"),
        ("No.", "ਨੰਬਰ"),
        ("Nº", "ਨੰਬਰ"),
        ("№", "ਨੰਬਰ"),
        ("#", "ਨੰਬਰ"),
    ] {
        if let Some(rest) = s.strip_prefix(key) {
            let (n, w) = number(rest.strip_prefix(' ').unwrap_or(rest))?;
            return Some((name(&format!("{value} {n}")), w));
        }
    }
    if s.as_bytes().first().is_some_and(u8::is_ascii_uppercase) {
        let segments = s.trim_end_matches('.').split('.').map(|x| x.strip_prefix(' ').unwrap_or(x));
        let mut result = String::new();
        let mut count = 0;
        for segment in segments {
            if segment.len() != 1 || !segment.as_bytes()[0].is_ascii_uppercase() {
                return None;
            }
            result.push(segment.as_bytes()[0] as char);
            count += 1;
        }
        if count >= 3 {
            return Some((name(&result), 0));
        }
    }
    None
}
const KEYS: &[&str] = &[
    "ਅਧਿਆਇ",
    "ਭਾਗ",
    "ਕਾਂਡ",
    "ਜਮਾਤ",
    "ਸ਼੍ਰੇਣੀ",
    "ਸਦੀ",
    "ਅੰਕ",
    "ਦ੍ਰਿਸ਼",
    "ਧਾਰਾ",
    "ਅਨੁਛੇਦ",
    "ਪਰਿਛੇਦ",
    "ਹੈਨਰੀ",
    "ਲੂਈ",
    "ਐਡਵਰਡ",
    "ਜਾਰਜ",
    "ਚਾਰਲਸ",
    "ਐਲਿਜ਼ਾਬੈਥ",
    "ਪੋਪ",
    "ਜਾਨ ਪਾਲ",
];
pub(crate) fn roman_key_prefix(s: &str) -> bool {
    KEYS.iter().any(|key| {
        if key.starts_with(s) {
            return true;
        }
        let Some(rest) = s.strip_prefix(key) else { return false };
        let Some(rest) = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')) else { return false };
        rest.chars().all(|c| "IVXLCDM".contains(c))
    })
}
pub(crate) fn roman_cost(s: &str) -> Option<Cost> {
    for &key in KEYS {
        for sep in [' ', '-'] {
            if s.strip_prefix(key).and_then(|x| x.strip_prefix(sep)).is_some_and(|n| lookup(ROMAN, n).is_some())
                || s.strip_suffix(key).and_then(|x| x.strip_suffix(sep)).is_some_and(|n| lookup(ROMAN, n).is_some())
            {
                return Some(0);
            }
        }
    }
    let (head, tail) = s.split_once(' ').map_or((s, None), |(a, b)| (a, Some(b)));
    if let Some(phrase) = tail {
        if phrase.split(|c| c == ' ' || c == '-').any(|part| {
            part.is_empty()
                || !part.chars().all(|c| {
                    let mut bytes = [0; 4];
                    lookup(PA_CHARS, c.encode_utf8(&mut bytes)).is_some()
                })
        }) {
            return None;
        }
    }
    for row in ROMAN.lines() {
        let (num, _) = row.split_once('\t')?;
        let Some(suffix) = head.strip_prefix(num).map(|x| x.strip_prefix('-').unwrap_or(x)) else { continue };
        if suffix.is_empty() {
            continue;
        }
        if ROMAN_EX.lines().filter_map(|row| row.split_once('\t')).any(|(key, _)| key.strip_prefix(num) == Some(suffix))
        {
            return Some(cost(-0.1));
        }
        if lookup(SUFFIX, suffix).is_some() {
            return Some(0);
        }
    }
    None
}
pub(crate) fn roman_tag(s: &str) -> Option<(String, Cost)> {
    for &key in KEYS {
        for sep in [' ', '-'] {
            if let Some(n) = s.strip_prefix(key).and_then(|x| x.strip_prefix(sep)) {
                if lookup(ROMAN, n).is_some() {
                    return Some((
                        format!(
                            "roman {{ preserve_order: true key_cardinal: \"{}\" integer: \"{n}\" }}",
                            key.replace(' ', "\u{a0}")
                        ),
                        0,
                    ));
                }
            }
            if let Some(n) = s.strip_suffix(key).and_then(|x| x.strip_suffix(sep)) {
                if lookup(ROMAN, n).is_some() {
                    return Some((
                        format!(
                            "roman {{ preserve_order: true integer: \"{n}\" key_cardinal: \"{}\" }}",
                            key.replace(' ', "\u{a0}")
                        ),
                        0,
                    ));
                }
            }
        }
    }
    let (head, tail) = s.split_once(' ').map_or((s, None), |(a, b)| (a, Some(b)));
    if let Some(phrase) = tail {
        if phrase
            .split(|c| c == ' ' || c == '-')
            .any(|part| part.is_empty() || !part.chars().all(|c| lookup(PA_CHARS, &c.to_string()).is_some()))
        {
            return None;
        }
    }
    for row in ROMAN.lines() {
        let (num, spoken) = row.split_once('\t')?;
        let Some(suffix) = head.strip_prefix(num).map(|x| x.strip_prefix('-').unwrap_or(x)) else { continue };
        if suffix.is_empty() {
            continue;
        }
        let fused = format!("{num}{suffix}");
        let (value, w) = if let Some(v) = lookup(ROMAN_EX, &fused) {
            (v.to_owned(), cost(-0.1))
        } else if let Some(suffix) = lookup(SUFFIX, suffix) {
            (format!("{spoken}{suffix}"), 0)
        } else {
            continue;
        };
        let tail = tail.map(|x| format!(" key_cardinal: \"{}\"", x.replace(' ', "\u{a0}"))).unwrap_or_default();
        return Some((
            format!("roman {{ preserve_order: true integer: \"{num}\" default_ordinal: \"{value}\"{tail} }}"),
            w,
        ));
    }
    None
}
fn letter(c: char) -> Option<&'static str> {
    let mut bytes = [0; 4];
    lookup(LETTERS, c.to_ascii_lowercase().encode_utf8(&mut bytes))
}
fn code_number(s: &str) -> Option<(String, Cost)> {
    if s.chars().count() >= 4 { Some((digitwise(s)?, 0)) } else { cardinal(s) }
}
fn code_weight(s: &str) -> Option<Cost> {
    if s.chars().count() >= 4 { s.chars().all(|c| digit(c).is_some()).then_some(0) } else { cardinal_input_weight(s) }
}

pub(crate) fn serial_cost(s: &str) -> Option<Cost> {
    if s.chars().any(char::is_whitespace) || !s.chars().any(|c| digit(c).is_some() || matches!(c, '%' | '&' | '@')) {
        return None;
    }
    if let Some((base, power)) = s.split_once('^') {
        let weight = code_weight(base).or_else(|| {
            let mut chars = base.chars();
            let c = chars.next()?;
            if chars.next().is_some() {
                return None;
            }
            letter(c).map(|_| 0)
        })?;
        if matches!(power, "2" | "੨" | "3" | "੩") {
            return Some(weight + cost(-1.0));
        }
        return Some(weight + code_weight(power)? + cost(1.0));
    }
    if let Some(v) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) {
        if v.contains('.') {
            let mut weight = 0;
            for part in v.split('.') {
                weight += number_weight(part)?;
            }
            letter('v')?;
            return Some(weight);
        }
    }
    if s.bytes().filter(|&c| c == b'.').count() == 2 {
        let mut weight = 0;
        for part in s.split('.') {
            weight += number_weight(part)?;
        }
        return Some(weight);
    }
    if s.chars().all(|c| c.is_ascii_alphabetic() || c == '-' || c == '/')
        || !s.chars().any(|c| c.is_alphabetic() || c == '-' || c == '/')
    {
        return None;
    }
    if s.chars().next().and_then(digit).is_some() && super::date_time::date_cost(s).is_some()
        || super::numeric::ordinal_cost(s).is_some()
        || super::commerce::measure_cost(s).is_some()
    {
        return None;
    }
    if s.starts_with('-')
        || s.starts_with('/')
        || s.ends_with('-')
        || s.ends_with('/')
        || s.contains("--")
        || s.contains("//")
        || s.contains("-/")
        || s.contains("/-")
    {
        return None;
    }
    let mut weight = 0;
    let mut i = 0;
    while i < s.len() {
        let c = s[i..].chars().next()?;
        if c == '-' || c == '/' {
            i += 1;
            continue;
        }
        if digit(c).is_some() {
            let start = i;
            while i < s.len() {
                let c = s[i..].chars().next()?;
                if digit(c).is_none() {
                    break;
                }
                i += c.len_utf8();
            }
            weight += code_weight(&s[start..i])?;
        } else if c.is_ascii_alphabetic() {
            letter(c)?;
            i += 1;
        } else if ('\u{a00}'..='\u{a7f}').contains(&c) {
            let start = i;
            while i < s.len() {
                let c = s[i..].chars().next()?;
                if !('\u{a00}'..='\u{a7f}').contains(&c) || digit(c).is_some() {
                    break;
                }
                i += c.len_utf8();
            }
            if s[start..i].chars().count() < 2 {
                return None;
            }
        } else if matches!(c, '%' | '&' | '@') {
            if i == 0 || !matches!(s.as_bytes()[i - 1], b'-' | b'/') {
                return None;
            }
            let mut bytes = [0; 4];
            lookup(SERIAL_SYMBOLS, c.encode_utf8(&mut bytes))?;
            i += c.len_utf8();
            if i < s.len() && !matches!(s.as_bytes()[i], b'-' | b'/') {
                return None;
            }
        } else {
            return None;
        }
    }
    Some(weight)
}
pub(crate) fn serial_tag(s: &str) -> Option<(String, Cost)> {
    if s.chars().any(char::is_whitespace) || !s.chars().any(|c| digit(c).is_some() || matches!(c, '%' | '&' | '@')) {
        return None;
    }
    if let Some((base, power)) = s.split_once('^') {
        let (base, w) = code_number(base).or_else(|| {
            let mut chars = base.chars();
            let c = chars.next()?;
            if chars.next().is_some() {
                return None;
            }
            Some((letter(c)?.to_owned(), 0))
        })?;
        let suffix = format!("^{power}");
        if let Some(v) = lookup(POWERS, &suffix) {
            return Some((name(&format!("{base}{v}")), w + cost(-1.0)));
        }
        let (p, pw) = code_number(power)?;
        return Some((name(&format!("{base} ਦੀ ਘਾਤ {p}")), w + pw + cost(1.0)));
    }
    let body = s.strip_prefix('v').or_else(|| s.strip_prefix('V'));
    if let Some(v) = body {
        if v.contains('.') {
            let parts = v.split('.');
            let mut values = Vec::new();
            let mut weight = 0;
            for p in parts {
                let (n, w) = number(p)?;
                values.push(n);
                weight += w;
            }
            return Some((name(&format!("{} {}", letter('v')?, values.join(" ਡਾਟ "))), weight));
        }
    }
    if s.bytes().filter(|&c| c == b'.').count() == 2 {
        let parts = s.split('.');
        let mut values = Vec::new();
        let mut weight = 0;
        for p in parts {
            let (n, w) = number(p)?;
            values.push(n);
            weight += w;
        }
        return Some((name(&values.join(" ਡਾਟ ")), weight));
    }
    if s.chars().all(|c| c.is_ascii_alphabetic() || c == '-' || c == '/') {
        return None;
    }
    if !s.chars().any(|c| c.is_alphabetic() || c == '-' || c == '/') {
        return None;
    }
    if s.chars().next().and_then(digit).is_some() && super::date_time::date_cost(s).is_some()
        || super::numeric::ordinal_cost(s).is_some()
        || super::commerce::measure_cost(s).is_some()
    {
        return None;
    }
    if s.starts_with('-')
        || s.starts_with('/')
        || s.ends_with('-')
        || s.ends_with('/')
        || s.contains("--")
        || s.contains("//")
        || s.contains("-/")
        || s.contains("/-")
    {
        return None;
    }
    let mut result = String::new();
    let mut weight = 0;
    let mut i = 0;
    while i < s.len() {
        let c = s[i..].chars().next()?;
        if c == '-' || c == '/' {
            result.push(c);
            i += 1;
            continue;
        }
        if !result.is_empty() && !result.ends_with('-') && !result.ends_with('/') {
            result.push(' ');
        }
        if digit(c).is_some() {
            let start = i;
            while i < s.len() {
                let c = s[i..].chars().next()?;
                if digit(c).is_none() {
                    break;
                }
                i += c.len_utf8();
            }
            let (v, w) = code_number(&s[start..i])?;
            result.push_str(&v);
            weight += w;
        } else if c.is_ascii_alphabetic() {
            result.push_str(letter(c)?);
            i += 1;
        } else if ('\u{a00}'..='\u{a7f}').contains(&c) {
            let start = i;
            while i < s.len() {
                let c = s[i..].chars().next()?;
                if !('\u{a00}'..='\u{a7f}').contains(&c) || digit(c).is_some() {
                    break;
                }
                result.push(c);
                i += c.len_utf8();
            }
            if s[start..i].chars().count() < 2 {
                return None;
            }
        } else if matches!(c, '%' | '&' | '@') {
            if i == 0 || !matches!(s.as_bytes()[i - 1], b'-' | b'/') {
                return None;
            }
            result.push_str(lookup(SERIAL_SYMBOLS, &c.to_string())?);
            i += c.len_utf8();
            if i < s.len() && !matches!(s.as_bytes()[i], b'-' | b'/') {
                return None;
            }
        } else {
            return None;
        }
    }
    Some((name(&result), weight))
}
static SYMBOL_CLASSES: LazyLock<HashMap<char, u8>> = LazyLock::new(|| {
    let mut classes = HashMap::new();
    for row in CLASSES.lines() {
        if let Some((symbol, names)) = row.split_once('\t') {
            if let Some(c) = symbol.chars().next() {
                let mut mask = 0;
                for name in names.split(',') {
                    mask |= match name {
                        "email" => 1,
                        "url" => 2,
                        "unix" => 4,
                        "windows" => 8,
                        "chem" => 16,
                        _ => 0,
                    };
                }
                classes.insert(c, mask);
            }
        }
    }
    classes
});

fn symbol_allowed(c: char, class: &str) -> bool {
    let bit = match class {
        "email" => 1,
        "url" => 2,
        "unix" => 4,
        "windows" => 8,
        "chem" => 16,
        _ => 0,
    };
    SYMBOL_CLASSES.get(&c).is_some_and(|mask| *mask & bit != 0)
}
fn alpha_digit(c: char) -> bool {
    c.is_ascii_alphanumeric() || digit(c).is_some() || ('₀'..='₉').contains(&c)
}
fn valid_chars(s: &str, class: &str) -> bool {
    !s.is_empty() && s.chars().all(|c| alpha_digit(c) || symbol_allowed(c, class))
}
enum Electronic<'a> {
    Email(&'a str, &'a str),
    Url(&'static str, &'a str),
    Path(&'a str),
    Domain(&'a str),
    Chemical(&'a str),
}

fn electronic_read(s: &str) -> Option<(Electronic<'_>, Cost)> {
    if let Some((user, domain)) = s.split_once('@') {
        if !user.is_empty()
            && user.chars().all(|c| c.is_ascii_alphanumeric() || symbol_allowed(c, "email"))
            && !domain.is_empty()
            && domain.chars().all(|c| c.is_ascii_alphanumeric() || c == '.' || c == '-')
        {
            return Some((Electronic::Email(user, domain), cost(1.0)));
        }
    }
    for (prefix, protocol, weight) in [
        ("https://www.", "httpswww", 1.0),
        ("http://www.", "httpwww", 1.0),
        ("https://", "https", 1.01),
        ("http://", "http", 1.01),
        ("www.", "www", 1.02),
    ] {
        if let Some(domain) = s.strip_prefix(prefix) {
            if valid_chars(domain, "url") {
                return Some((Electronic::Url(protocol, domain), cost(1.0 + weight)));
            }
        }
    }
    if s.starts_with('/') && valid_chars(&s[1..], "unix") {
        return Some((Electronic::Path(s), cost(1.0)));
    }
    if let Some(rest) = s.strip_prefix('\\') {
        if !rest.is_empty()
            && rest.chars().all(|c| alpha_digit(c) || c == ' ' || c == '\\' || c != '/' && symbol_allowed(c, "unix"))
        {
            return Some((Electronic::Path(s), cost(1.0)));
        }
    }
    if s.len() > 3 && s.as_bytes()[0].is_ascii_alphabetic() && s[1..].starts_with(":\\") {
        let rest = &s[3..];
        if !rest.is_empty() && rest.chars().all(|c| alpha_digit(c) || c == ' ' || symbol_allowed(c, "windows")) {
            return Some((Electronic::Path(s), cost(1.0)));
        }
    }
    if let Some((stem, _)) = s.split_once('/') {
        if !stem.is_empty() && valid_chars(s, "unix") {
            return Some((Electronic::Path(s), cost(1.0)));
        }
    }
    let domain = s.trim_end_matches('/');
    if domain.contains('.') {
        let mut parts = domain.split('.');
        let first = parts.next()?;
        let mut count = 1;
        let mut last = first;
        let mut ip = (1..=3).contains(&first.len()) && first.bytes().all(|b| b.is_ascii_digit());
        let mut name = !first.is_empty() && first.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-');
        for part in parts {
            count += 1;
            last = part;
            ip &= (1..=3).contains(&part.len()) && part.bytes().all(|b| b.is_ascii_digit());
            name &= !part.is_empty() && part.bytes().all(|b| b.is_ascii_alphanumeric() || b == b'-');
        }
        if count == 4 && ip {
            return Some((Electronic::Domain(s), cost(1.0)));
        }
        if count >= 2 && name && (lookup(DOMAINS, last).is_some() || lookup(EXTENSIONS, last).is_some()) {
            return Some((Electronic::Domain(s), cost(1.1)));
        }
    }
    if s.chars().any(|c| ('₀'..='₉').contains(&c)) {
        if s.ends_with(')') && !s.contains('(') {
            return None;
        }
        let mut rest = s;
        let mut seen = false;
        while !rest.is_empty() {
            let c = rest.chars().next()?;
            if c.is_ascii_digit() || ('₀'..='₉').contains(&c) {
                if !seen {
                    return None;
                }
                rest = &rest[c.len_utf8()..];
                continue;
            }
            if symbol_allowed(c, "chem") {
                rest = &rest[c.len_utf8()..];
                continue;
            }
            let mut found = None;
            for row in ELEMENTS.lines() {
                let e = row.split('\t').next()?;
                if rest.starts_with(e) && found.map_or(true, |x: &str| e.len() > x.len()) {
                    found = Some(e);
                }
            }
            let e = found?;
            rest = &rest[e.len()..];
            seen = true;
        }
        if seen {
            return Some((Electronic::Chemical(s), cost(1.2)));
        }
    }
    None
}

pub(crate) fn electronic_cost(s: &str) -> Option<Cost> {
    electronic_read(s).map(|(_, weight)| weight)
}

pub(crate) fn electronic_tag(s: &str) -> Option<(String, Cost)> {
    let (reading, weight) = electronic_read(s)?;
    let tag = match reading {
        Electronic::Email(user, domain) => format!("electronic {{ username: \"{user}\" domain: \"{domain}\" }}"),
        Electronic::Url(protocol, domain) => format!("electronic {{ protocol: \"{protocol}\" domain: \"{domain}\" }}"),
        Electronic::Path(path) => format!("electronic {{ path: \"{path}\" }}"),
        Electronic::Domain(domain) => format!("electronic {{ domain: \"{domain}\" }}"),
        Electronic::Chemical(formula) => format!("electronic {{ fragment_id: \"{formula}\" }}"),
    };
    Some((tag, weight))
}

use super::graph_utils::{Gender, under_thousand};
use super::numbers::{cardinal, split_range};
use std::sync::LazyLock;

const DAYS: [&str; 31] = [
    "prvního",
    "druhého",
    "třetího",
    "čtvrtého",
    "pátého",
    "šestého",
    "sedmého",
    "osmého",
    "devátého",
    "desátého",
    "jedenáctého",
    "dvanáctého",
    "třináctého",
    "čtrnáctého",
    "patnáctého",
    "šestnáctého",
    "sedmnáctého",
    "osmnáctého",
    "devatenáctého",
    "dvacátého",
    "dvacátého prvního",
    "dvacátého druhého",
    "dvacátého třetího",
    "dvacátého čtvrtého",
    "dvacátého pátého",
    "dvacátého šestého",
    "dvacátého sedmého",
    "dvacátého osmého",
    "dvacátého devátého",
    "třicátého",
    "třicátého prvního",
];
const MONTHS: [&str; 12] = [
    "ledna",
    "února",
    "března",
    "dubna",
    "května",
    "června",
    "července",
    "srpna",
    "září",
    "října",
    "listopadu",
    "prosince",
];
fn bounded(s: &str, max: usize) -> Option<usize> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|x| x.is_ascii_digit()) {
        return None;
    }
    let n = s.parse::<usize>().ok()?;
    if n == 0 || n > max {
        return None;
    }
    Some(n)
}
fn day(s: &str) -> Option<&'static str> {
    Some(DAYS[bounded(s, 31)? - 1])
}
fn month(s: &str) -> Option<&'static str> {
    if let Some(n) = bounded(s, 12) {
        return Some(MONTHS[n - 1]);
    }
    let x = s.strip_suffix('.').unwrap_or(s);
    if x.len() != s.len()
        && !["led", "úno", "bře", "dub", "kvě", "čvn", "čvc", "srp", "zář", "říj", "lis", "pro", "jan"].contains(&x)
    {
        return None;
    }
    let i = match x {
        "ledna" | "leden" | "led" | "jan" | "januari" => 0,
        "února" | "únor" | "úno" => 1,
        "března" | "březen" | "bře" => 2,
        "dubna" | "duben" | "dub" => 3,
        "května" | "květen" | "kvě" => 4,
        "června" | "červen" | "čvn" => 5,
        "července" | "červenec" | "čvc" => 6,
        "srpna" | "srpen" | "srp" => 7,
        "září" | "zář" => 8,
        "října" | "říjen" | "říj" => 9,
        "listopadu" | "listopad" | "lis" => 10,
        "prosince" | "prosinec" | "pro" => 11,
        _ => return None,
    };
    Some(MONTHS[i])
}
pub fn iso(s: &str) -> Option<(String, String, String)> {
    for (i, c) in s.char_indices() {
        if c != '-' && c != '.' {
            continue;
        }
        let Some(year) = cardinal(&s[..i]) else { continue };
        let rest = &s[i + 1..];
        if rest.len() < 5 {
            continue;
        }
        let Some(m) = rest.get(..2) else { continue };
        let sep = rest.as_bytes()[2] as char;
        if sep != '-' && sep != '.' {
            continue;
        }
        let Some(d) = rest.get(3..) else { continue };
        if d.len() != 2 {
            continue;
        }
        if let (Some(m), Some(d)) = (month(m), day(d)) {
            return Some((year, m.to_owned(), d.to_owned()));
        }
    }
    None
}
const ERAS: &[(&str, &str)] = &[
    ("př. n. l.", "před naším letopočtem"),
    ("př. n. l", "před naším letopočtem"),
    ("př. Kr.", "před Kristem"),
    ("př. Kr", "před Kristem"),
    ("BC", "před naším letopočtem"),
    ("B.C.", "před naším letopočtem"),
    ("BCE", "před naším letopočtem"),
    ("B.C.E.", "před naším letopočtem"),
    ("f.Kr.", "před Kristem"),
    ("f Kr", "před Kristem"),
    ("n. l.", "našeho letopočtu"),
    ("n. l", "našeho letopočtu"),
    ("po Kr.", "po Kristu"),
    ("po Kr", "po Kristu"),
    ("AD", "našeho letopočtu"),
    ("A.D.", "našeho letopočtu"),
    ("CE", "našeho letopočtu"),
    ("C.E.", "našeho letopočtu"),
];
static ERA_END: LazyLock<[bool; 256]> = LazyLock::new(|| {
    let mut bytes = [false; 256];
    for (input, _) in ERAS {
        if let Some(last) = input.as_bytes().last() {
            bytes[*last as usize] = true;
        }
    }
    bytes
});
pub fn possible_era_end(c: char) -> bool {
    let mut buf = [0; 4];
    let encoded = c.encode_utf8(&mut buf);
    ERA_END[*encoded.as_bytes().last().expect("nonempty encoded character") as usize]
}
pub fn date(s: &str) -> Option<String> {
    for &(e, v) in ERAS {
        if let Some(p) = s.strip_suffix(e) {
            let p = p.strip_suffix(' ').unwrap_or(p);
            if let Some(y) = cardinal(p).or_else(|| written_date(p, true)) {
                return Some(format!("{y} {v}"));
            }
        }
    }
    if let Some(v) = written_date(s, false) {
        return Some(v);
    }
    for sep in ['.', '/'] {
        let Some((day_part, rest)) = s.split_once(sep) else { continue };
        let Some((month_part, last)) = rest.split_once(sep) else { continue };
        let Some(d) = day(day_part) else { continue };
        let mut mpart = if sep == '.' { month_part.strip_prefix(' ').unwrap_or(month_part) } else { month_part };
        if sep == '.' && last.is_empty() {
            mpart = mpart.strip_suffix(' ').unwrap_or(mpart)
        }
        let Some(m) = bounded(mpart, 12).map(|n| MONTHS[n - 1]) else { continue };
        let last = if sep == '.' { last.strip_prefix(' ').unwrap_or(last) } else { last };
        if sep == '.' && last.is_empty() {
            return Some(format!("{d} {m}"));
        }
        if let Some(y) = cardinal(last) {
            return Some(format!("{d} {m} {y}"));
        }
    }
    for (a, b) in split_range(s) {
        if let (Some(a), Some(b)) = (cardinal(a), cardinal(b)) {
            return Some(format!("{a} až {b}"));
        }
    }
    None
}
fn written_date(s: &str, require_year: bool) -> Option<String> {
    let p = s.find('.')?;
    let d = day(&s[..p])?;
    let rest = &s[p + 1..];
    let rest = rest.strip_prefix(' ').unwrap_or(rest);
    for i in rest.char_indices().map(|(i, _)| i).chain(std::iter::once(rest.len())).rev() {
        let m = &rest[..i];
        if m.starts_with(|c: char| c.is_ascii_digit()) {
            continue;
        }
        if let Some(m) = month(m) {
            let tail = &rest[i..];
            if tail.is_empty() {
                if !require_year {
                    return Some(format!("{d} {m}"));
                }
                continue;
            }
            let tail = tail.strip_prefix(',').unwrap_or(tail);
            let tail = tail.strip_prefix(' ').unwrap_or(tail);
            if let Some(y) = cardinal(tail) {
                return Some(format!("{d} {m} {y}"));
            }
        }
    }
    None
}
/// Recognize the unambiguous clock core without spelling its fields.
pub fn simple_time(s: &str) -> bool {
    if s == "0:00" || s == "00:00" {
        return false;
    }
    let mut parts = s.split(':');
    let valid = |part: &str, max: u8| {
        !part.is_empty()
            && part.len() <= 2
            && part.bytes().all(|b| b.is_ascii_digit())
            && part.parse::<u8>().is_ok_and(|n| n <= max)
    };
    let Some(hours) = parts.next() else { return false };
    let Some(minutes) = parts.next() else { return false };
    if !valid(hours, 23) || !valid(minutes, 59) {
        return false;
    }
    match parts.next() {
        None => true,
        Some(seconds) => valid(seconds, 59) && parts.next().is_none(),
    }
}

/// After a complete clock and one space only these continuations can extend
/// its source span (a range, AM/PM designator, or timezone).
pub fn clock_continuation(rest: &[char]) -> bool {
    if matches!(rest.first(), Some('-' | '–' | '—')) {
        return true;
    }
    AMPM.iter().chain(ZONES.iter()).any(|(key, _)| {
        let mut chars = rest.iter().copied();
        key.chars().all(|c| chars.next() == Some(c))
    })
}
fn clock_number(s: &str, hour: bool) -> Option<String> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|x| x.is_ascii_digit()) {
        return None;
    }
    let n = s.parse::<usize>().ok()?;
    if n > if hour { 23 } else { 59 } {
        return None;
    }
    if !hour && s.len() == 2 && s.starts_with('0') {
        return Some(format!("nula {}", under_thousand(n, if n == 2 { Gender::Feminine } else { Gender::Default })));
    }
    Some(under_thousand(n, if n == 2 { Gender::Feminine } else { Gender::Default }))
}
fn core(s: &str) -> Option<String> {
    let mut p = s.split(':');
    let h = clock_number(p.next()?, true)?;
    let m = clock_number(p.next()?, false)?;
    let seconds = p.next();
    if p.next().is_some() {
        return None;
    }
    let mut out = format!("{h} {m}");
    if let Some(seconds) = seconds {
        out.push(' ');
        out.push_str(&clock_number(seconds, false)?);
    }
    if s == "00:00" || s == "0:00" {
        return Some("půlnoc".to_owned());
    }
    Some(out)
}
const AMPM: &[(&str, &str)] = &[
    ("AM", "dopoledne"),
    ("A.M.", "dopoledne"),
    ("am", "dopoledne"),
    ("a.m.", "dopoledne"),
    ("dop.", "dopoledne"),
    ("dop", "dopoledne"),
    ("PM", "odpoledne"),
    ("P.M.", "odpoledne"),
    ("pm", "odpoledne"),
    ("p.m.", "odpoledne"),
    ("odp.", "odpoledne"),
    ("odp", "odpoledne"),
    ("e.m.", "odpoledne"),
    ("e.m", "odpoledne"),
    ("fm", "dopoledne"),
    ("f.m.", "dopoledne"),
];
const ZONES: &[(&str, &str)] = &[
    ("UTC", "U T C"),
    ("GMT", "G M T"),
    ("CET", "C E T"),
    ("CEST", "C E S T"),
    ("EST", "E S T"),
    ("EDT", "E D T"),
    ("PST", "P S T"),
    ("PDT", "P D T"),
    ("SEČ", "středoevropského času"),
    ("SELČ", "středoevropského letního času"),
];
fn with_ampm(s: &str) -> Option<String> {
    if let Some(v) = core(s) {
        return Some(v);
    }
    for &(k, v) in AMPM {
        if let Some(p) = s.strip_suffix(k) {
            let p = p.strip_suffix(' ').unwrap_or(p);
            if let Some(x) = core(p) {
                return Some(format!("{x} {v}"));
            }
        }
    }
    None
}
pub fn time(s: &str) -> Option<String> {
    if let Some(v) = with_ampm(s) {
        return Some(v);
    }
    for (a, b) in split_range(s) {
        if let (Some(a), Some(b)) = (core(a), core(b)) {
            return Some(format!("{a} až {b}"));
        }
    }
    for &(key, val) in ZONES {
        for (i, _) in s.match_indices(key) {
            let p = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let Some(t) = with_ampm(p) else { continue };
            let tail = &s[i + key.len()..];
            if tail.is_empty() {
                return Some(format!("{t} {val}"));
            }
            let tail = tail.strip_prefix(' ').unwrap_or(tail);
            let (sign, n) = if let Some(n) = tail.strip_prefix('+') {
                ("plus", n)
            } else if let Some(n) = tail.strip_prefix('-').or_else(|| tail.strip_prefix('−')) {
                ("mínus", n)
            } else {
                continue;
            };
            if n.len() > 3 || n.starts_with('0') {
                continue;
            }
            if let Some(n) = cardinal(n) {
                return Some(format!("{t} {val} {sign} {n}"));
            }
        }
    }
    None
}

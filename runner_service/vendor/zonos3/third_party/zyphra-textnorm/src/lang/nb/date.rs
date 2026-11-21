use super::number;
static MONTHS: [&str; 12] = [
    "januar",
    "februar",
    "mars",
    "april",
    "mai",
    "juni",
    "juli",
    "august",
    "september",
    "oktober",
    "november",
    "desember",
];
const ALIASES: &[(&str, usize)] = &[
    ("jan.", 0),
    ("feb.", 1),
    ("mar.", 2),
    ("apr.", 3),
    ("jun.", 5),
    ("jul.", 6),
    ("aug.", 7),
    ("sep.", 8),
    ("sept.", 8),
    ("okt.", 9),
    ("nov.", 10),
    ("des.", 11),
];

/// Necessary suffix for the date union: four-digit year, month, decade or era.
pub fn possible_end(s: &str) -> bool {
    let b = s.as_bytes();
    if b.len() >= 4 && b[b.len() - 4..].iter().all(u8::is_ascii_digit) {
        return true;
    }
    if s.ends_with("-tallet") || (s.ends_with('s') && b.len() >= 2 && b[b.len() - 2].is_ascii_digit()) {
        return true;
    }
    if MONTHS.iter().any(|m| s.ends_with(m)) || ALIASES.iter().any(|(m, _)| s.ends_with(m)) {
        return true;
    }
    ["f.Kr.", "f. Kr.", "BC", "B.C.", "BCE", "e.Kr.", "e. Kr.", "AD", "A.D.", "CE"]
        .iter()
        .any(|suffix| s.ends_with(suffix))
}

fn month(s: &str) -> Option<&'static str> {
    if let Some(x) = MONTHS.iter().find(|x| **x == s) {
        return Some(x);
    }
    for &(k, i) in ALIASES {
        if s == k {
            return Some(MONTHS[i]);
        }
    }
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: usize = s.parse().ok()?;
    if n == 0 || n > 12 { None } else { Some(MONTHS[n - 1]) }
}
fn year(s: &str) -> Vec<(String, f64)> {
    if s.len() != 4 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return Vec::new();
    }
    let n: u32 = s.parse().unwrap();
    let mut out = Vec::new();
    if (1000..=2099).contains(&n) {
        let h = n / 100;
        let r = n % 100;
        if n == 1000 {
            out.push(("tusen".into(), 0.0));
        } else if n == 2000 {
            out.push(("to tusen".into(), 0.0));
        } else if h == 10 {
            out.push((format!("tusen og {}", number::cardinal(&r.to_string(), false).unwrap()), 0.0));
        } else if h == 20 {
            let w = number::cardinal(&r.to_string(), false).unwrap();
            out.push((format!("to tusen og {w}"), 0.0));
            out.push((format!("tjue {w}"), 0.0));
        } else {
            out.push((
                format!(
                    "{} {}",
                    number::cardinal(&h.to_string(), false).unwrap(),
                    if r == 0 { "hundre".into() } else { number::cardinal(&r.to_string(), false).unwrap() }
                ),
                0.0,
            ));
        }
    }
    out.extend(number::cardinal_variants(s, false).into_iter().map(|(w, c)| (w, c + 0.1)));
    out
}
fn day(s: &str) -> Vec<(String, f64)> {
    let n = s.strip_suffix('.').unwrap_or(s);
    let Some(x) = number::ordinal(n) else { return Vec::new() };
    let mut out = vec![(x.clone(), 0.0)];
    if n == "100" || n == "1000" {
        out.push((format!("ett {x}"), 0.0));
    } else if n.len() == 3 && n.starts_with('1') && n != "100" {
        out.push((format!("ett {x}"), 0.1));
    }
    out
}
pub fn iso(s: &str) -> Vec<(String, String, String, f64)> {
    let Some((y, rest)) = s.split_once('-') else { return Vec::new() };
    let Some((m, d)) = rest.split_once('-') else { return Vec::new() };
    if y.len() != 4 || m.len() != 2 || d.len() != 2 || !m.bytes().all(|b| b.is_ascii_digit()) {
        return Vec::new();
    }
    let d = if d.starts_with('0') { &d[1..] } else { d };
    let (Some(d), Some(m)) = (number::ordinal(d), month(m)) else { return Vec::new() };
    year(y).into_iter().map(|(y, w)| (d.clone(), m.into(), y, w)).collect()
}
pub fn parse(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let mut parts = s.split(' ');
    if let (Some(d), Some(m)) = (parts.next(), parts.next()) {
        let y = parts.next();
        if parts.next().is_none() && !m.bytes().any(|b| b.is_ascii_digit()) {
            if let Some(m) = month(m) {
                for (d, c) in day(d) {
                    if let Some(y) = y {
                        for (y, w) in year(y) {
                            out.push((format!("{d} {m} {y}"), c + w));
                        }
                    } else {
                        out.push((format!("{d} {m}"), c));
                    }
                }
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '.' | '/' | '-')) {
        let rest = &s[i + c.len_utf8()..];
        if !rest.bytes().any(|b| matches!(b, b'.' | b'/' | b'-')) {
            continue;
        }
        let days = day(&s[..i]);
        if days.is_empty() {
            continue;
        }
        for (j, sep) in rest.char_indices().filter(|(_, c)| matches!(c, '.' | '/' | '-')) {
            let m = &rest[..j];
            if !m.bytes().all(|b| b.is_ascii_digit()) {
                continue;
            }
            if let Some(m) = month(m) {
                for (d, c) in &days {
                    for (y, w) in year(&rest[j + sep.len_utf8()..]) {
                        out.push((format!("{d} {m} {y}"), c + w));
                    }
                }
            }
        }
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let b = &s[i + c.len_utf8()..];
        let b = b.strip_prefix(' ').unwrap_or(b);
        for (x, w) in year(a) {
            for (y, z) in year(b) {
                out.push((format!("{x} til {y}"), w + z));
            }
        }
    }
    let decade = match s {
        "1920-tallet" => Some("nitten tjuetallet"),
        "1930-tallet" => Some("nitten trettitallet"),
        "1940-tallet" => Some("nitten førtitallet"),
        "1950-tallet" => Some("nitten femtitallet"),
        "1960-tallet" => Some("nitten sekstitallet"),
        "1970-tallet" => Some("nitten syttitallet"),
        "1980-tallet" | "1980s" => Some("nitten åttitallet"),
        "1990-tallet" | "1990s" => Some("nitten nittitallet"),
        "2000-tallet" => Some("totusentallet"),
        "2010-tallet" | "2010s" => Some("totusenogtitalls"),
        "2020-tallet" | "2020s" => Some("tjuetjuetallet"),
        _ => None,
    };
    if let Some(v) = decade {
        out.push((v.into(), 0.0));
    }
    for (suffix, w) in [
        ("f.Kr.", "før Kristus"),
        ("f. Kr.", "før Kristus"),
        ("BC", "før Kristus"),
        ("B.C.", "før Kristus"),
        ("BCE", "før Kristus"),
        ("e.Kr.", "etter Kristus"),
        ("e. Kr.", "etter Kristus"),
        ("AD", "etter Kristus"),
        ("A.D.", "etter Kristus"),
        ("CE", "etter Kristus"),
    ] {
        if let Some(n) = s.strip_suffix(suffix) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(n) = number::cardinal(n, false) {
                out.push((format!("{n} {w}"), 0.0));
            }
        }
    }
    out
}

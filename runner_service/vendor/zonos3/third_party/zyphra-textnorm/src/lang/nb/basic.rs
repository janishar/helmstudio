use super::number;

pub fn digits(s: &str) -> Option<String> {
    let words = ["null", "en", "to", "tre", "fire", "fem", "seks", "sju", "åtte", "ni"];
    s.bytes()
        .map(|b| if b.is_ascii_digit() { Some(words[(b - b'0') as usize]) } else { None })
        .collect::<Option<Vec<_>>>()
        .map(|v| v.join(" "))
}
fn split_dash(s: &str) -> impl Iterator<Item = (&str, &str)> {
    s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')).map(move |(i, c)| (&s[..i], &s[i + c.len_utf8()..]))
}
fn opt_space(s: &str) -> &str {
    s.strip_prefix(' ').unwrap_or(s)
}
fn opt_end_space(s: &str) -> &str {
    s.strip_suffix(' ').unwrap_or(s)
}
pub fn cardinal(s: &str) -> Vec<(String, f64)> {
    let mut v = Vec::new();
    v.extend(number::cardinal_variants(s, false));
    if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        for (x, w) in number::cardinal_variants(t, false) {
            v.push((format!("minus {x}"), w));
        }
    }
    if s.len() > 1 && s.starts_with('0') {
        if let Some(x) = digits(s) {
            v.push((x, 0.0));
        }
    }
    for (a, b) in split_dash(s) {
        let a = opt_end_space(a);
        let b = opt_space(b);
        let (negative, a) =
            if let Some(x) = a.strip_prefix('-').or_else(|| a.strip_prefix('−')) { (true, x) } else { (false, a) };
        for (x, w) in number::cardinal_variants(a, false) {
            for (y, z) in number::cardinal_variants(b, false) {
                v.push((format!("{}{x} til {y}", if negative { "minus " } else { "" }), w + z));
            }
        }
    }
    if let Some((a, b)) = s.split_once(':') {
        for (a, w) in number::cardinal_variants(a, false) {
            for (b, z) in number::cardinal_variants(b, false) {
                v.push((format!("{a} til {b}"), w + z));
            }
        }
    }
    v
}
pub fn ordinal(s: &str) -> Vec<(String, f64)> {
    let (prefix, s) = if let Some(x) = s.strip_prefix("den ") {
        ("den ", x)
    } else if let Some(x) = s.strip_prefix("Den ") {
        ("Den ", x)
    } else {
        ("", s)
    };
    let mut out = Vec::new();
    for suffix in [".", "ste", "dre", "dje", "de", "ende", "te"] {
        if let Some(n) = s.strip_suffix(suffix) {
            if !prefix.is_empty() && suffix != "." {
                continue;
            }
            if let Some(w) = number::ordinal(n) {
                out.push((format!("{prefix}{w}"), 0.0));
                if n == "100" || n == "1000" {
                    out.push((format!("{prefix}ett {w}"), 0.0));
                } else if n.len() == 3 && n.starts_with('1') && n != "100" {
                    out.push((format!("{prefix}ett {w}"), 0.1));
                }
            }
        }
    }
    out
}
pub fn decimal_number(s: &str) -> Vec<String> {
    let (sign, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("minus ", x) } else { ("", s) };
    let mut out = Vec::new();
    for (i, c) in s.char_indices().filter(|(_, c)| *c == '.' || *c == ',') {
        if let Some(a) = number::cardinal(&s[..i], false) {
            let b = &s[i + c.len_utf8()..];
            // Both source paths are unweighted. Its optimized graph prefers the
            // digit-by-digit branch when it is available.
            if b.len() > 1 {
                if let Some(x) = digits(b) {
                    out.push(format!("{sign}{a} komma {x}"));
                }
            }
            if let Some(x) = number::cardinal(b, false) {
                out.push(format!("{sign}{a} komma {x}"));
            }
        }
    }
    out
}
pub fn decimal(s: &str) -> Vec<String> {
    let mut out = decimal_number(s);
    for (sym, unit) in [('%', "prosent"), ('‰', "promille")] {
        let Some(body) = s.strip_suffix(sym) else { continue };
        let body = opt_end_space(body);
        let (pm, body) = if let Some(x) = body.strip_prefix('±') { ("pluss minus ", x) } else { ("", body) };
        for x in decimal_number(body).into_iter().chain(number::cardinal(body, false)) {
            out.push(format!("{pm}{x} {unit}"));
        }
        if !pm.is_empty() {
            continue;
        }
        for (a, b) in split_dash(body) {
            let a = opt_end_space(a);
            let b = opt_space(b);
            let left = decimal_number(a).into_iter().chain(number::cardinal(a, false));
            let right: Vec<_> = decimal_number(b).into_iter().chain(number::cardinal(b, false)).collect();
            for x in left {
                for y in &right {
                    out.push(format!("{pm}{x} til {y} {unit}"));
                }
            }
        }
    }
    out
}
fn base_fraction(s: &str) -> Vec<(String, f64)> {
    let special = [
        ("½", "en halv"),
        ("⅓", "en tredjedel"),
        ("⅔", "to tredjedeler"),
        ("¼", "en fjerdedel"),
        ("¾", "tre fjerdedeler"),
        ("⅕", "en femtedel"),
        ("⅖", "to femtedeler"),
        ("⅗", "tre femtedeler"),
        ("⅘", "fire femtedeler"),
        ("⅙", "en sjettedel"),
        ("⅚", "fem sjettedeler"),
        ("⅛", "en åttendedel"),
        ("⅜", "tre åttendedeler"),
        ("⅝", "fem åttendedeler"),
        ("⅞", "sju åttendedeler"),
        ("⅑", "en niendedel"),
        ("⅒", "en tiendedel"),
    ];
    let common = [
        ("1/2", "en halv"),
        ("1/3", "en tredjedel"),
        ("2/3", "to tredjedeler"),
        ("1/4", "en fjerdedel"),
        ("3/4", "tre fjerdedeler"),
        ("1/5", "en femtedel"),
        ("2/5", "to femtedeler"),
        ("3/5", "tre femtedeler"),
        ("4/5", "fire femtedeler"),
        ("1/6", "en sjettedel"),
        ("5/6", "fem sjettedeler"),
        ("1/8", "en åttendedel"),
        ("3/8", "tre åttendedeler"),
        ("5/8", "fem åttendedeler"),
        ("7/8", "sju åttendedeler"),
        ("1/10", "en tiendedel"),
        ("24/12", "tjuefire tolvtedeler"),
    ];
    let mut v = Vec::new();
    for (k, x) in special {
        if s == k {
            v.push((x.into(), 0.0));
        }
    }
    for (k, x) in common {
        if s == k {
            v.push((x.into(), -0.1));
        }
    }
    if let Some((a, b)) = s.split_once('/') {
        let den = match b {
            "2" => "halvdel",
            "3" => "tredjedel",
            "4" => "fjerdedel",
            "5" => "femtedel",
            "6" => "sjettedel",
            "7" => "sjuendedel",
            "8" => "åttendedel",
            "9" => "niendedel",
            "10" => "tiendedel",
            "12" => "tolvtedel",
            "100" => "hundredel",
            "1000" => "tusendel",
            _ => "",
        };
        if !den.is_empty() {
            if let Some(x) = number::cardinal(a, false) {
                v.push((format!("{x} {den}{}", if a == "1" { "" } else { "er" }), 0.0));
            }
        }
    }
    v
}
pub fn fraction(s: &str) -> Vec<(String, f64)> {
    // Both slash fractions and the closed vulgar-fraction alphabet require a
    // marker. Reject ordinary numbers and measurements before splitting or
    // spelling their possible integer prefixes.
    if !s.contains('/')
        && !s.chars().any(|c| {
            matches!(
                c,
                '½' | '⅓'
                    | '⅔'
                    | '¼'
                    | '¾'
                    | '⅕'
                    | '⅖'
                    | '⅗'
                    | '⅘'
                    | '⅙'
                    | '⅚'
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
                    | '⅑'
                    | '⅒'
            )
        })
    {
        return Vec::new();
    }
    let (sign, s) =
        if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("minus ", x) } else { ("", s) };
    let mut out = base_fraction(s).into_iter().map(|(x, w)| (format!("{sign}{x}"), w)).collect::<Vec<_>>();
    for (i, _) in s.char_indices().skip(1) {
        let a = opt_end_space(&s[..i]);
        let b = &s[i..];
        let fractions = base_fraction(b);
        if !fractions.is_empty() {
            if let Some(x) = number::cardinal(a, false) {
                for (y, w) in fractions {
                    out.push((format!("{sign}{x} og {y}"), w));
                }
            }
        }
    }
    out
}
pub fn whitelist(s: &str) -> Vec<(String, f64)> {
    static ENTRIES: std::sync::LazyLock<std::collections::HashMap<&'static str, &'static str>> =
        std::sync::LazyLock::new(|| {
            [
                ("f.eks.", "for eksempel"),
                ("f. eks.", "for eksempel"),
                ("F.eks.", "for eksempel"),
                ("bl.a.", "blant annet"),
                ("bl. a.", "blant annet"),
                ("Bl.a.", "blant annet"),
                ("osv.", "og så videre"),
                ("Osv.", "og så videre"),
                ("ca.", "cirka"),
                ("Ca.", "cirka"),
                ("dvs.", "det vil si"),
                ("Dvs.", "det vil si"),
                ("mfl.", "med flere"),
                ("m.fl.", "med flere"),
                ("pga.", "på grunn av"),
                ("p.g.a.", "på grunn av"),
                ("Pga.", "på grunn av"),
                ("m.m.", "med mer"),
                ("jf.", "jamfør"),
                ("jfr.", "jamfør"),
                ("AS", "aksjeselskap"),
                ("A/S", "aksjeselskap"),
                ("t.o.m.", "til og med"),
                ("f.o.m.", "fra og med"),
                ("e.Kr.", "etter Kristus"),
                ("f.Kr.", "før Kristus"),
                ("dr.", "doktor"),
                ("dr", "doktor"),
                ("Dr.", "doktor"),
                ("Dr", "doktor"),
                ("prof.", "professor"),
                ("Prof.", "professor"),
                ("mr.", "mister"),
                ("Mr.", "mister"),
                ("fru", "fru"),
                ("frk.", "frøken"),
                ("cand.", "kandidat"),
                ("mag.", "magister"),
                ("&", "og"),
                ("©", "copyright"),
                ("§", "paragraf"),
                ("¢", "cent"),
                ("~", "omtrent"),
                ("±", "pluss minus"),
            ]
            .into_iter()
            .collect()
        });
    let mut v = Vec::new();
    if let Some(&x) = ENTRIES.get(s) {
        v.push((x.into(), 0.0));
    }
    for k in ["nr. ", "nr ", "Nr. ", "Nr ", "No. ", "no. "] {
        if let Some(n) = s.strip_prefix(k).and_then(|x| number::cardinal(x, false)) {
            v.push((format!("nummer {n}"), 0.0));
        }
    }
    for (k, w) in [("§", "paragraf"), ("©", "copyright")] {
        if let Some(n) = s.strip_prefix(k).and_then(|x| number::cardinal(opt_space(x), false)) {
            v.push((format!("{w} {n}"), 0.0));
        }
    }
    v
}
pub fn roman(s: &str) -> Vec<String> {
    let names = [
        "Harald",
        "Olav",
        "Haakon",
        "Karl",
        "Gustav",
        "Louis",
        "Elizabeth",
        "Henrik",
        "Fredrik",
        "Christian",
        "Magnus",
        "Sverre",
        "Eirik",
        "Inge",
        "Sigurd",
        "Pave",
        "Kong",
        "Dronning",
    ];
    let labels = [
        "Kapittel",
        "kapittel",
        "Del",
        "del",
        "Bind",
        "bind",
        "Akt",
        "akt",
        "Scene",
        "scene",
        "Bok",
        "bok",
        "Volum",
        "volum",
        "Vol.",
        "vol.",
        "Århundre",
        "århundre",
        "klasse",
        "Klasse",
    ];
    let romans = [
        "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
        "XVIII", "XIX", "XX", "XXI",
    ];
    let mut v = Vec::new();
    if let Some((a, b)) = s.split_once(' ') {
        if let Some(i) = romans.iter().position(|x| *x == b) {
            let n = (i + 1).to_string();
            if names.contains(&a) {
                let ordinal = if i == 19 { "tjuende".to_owned() } else { number::ordinal(&n).unwrap() };
                v.push(format!("{a} den {ordinal}"));
            }
            if labels.contains(&a) {
                v.push(format!(
                    "{} {}",
                    if a == "Vol." || a == "vol." { "volum" } else { a },
                    number::cardinal(&n, false).unwrap()
                ));
            }
        }
    }
    if let Some(body) = s.strip_suffix(" århundre") {
        let (prefix, body) = if let Some(x) = body.strip_prefix("det ") { ("det ", x) } else { ("", body) };
        let body = body.strip_suffix('.').unwrap_or(body);
        if let Some(i) = romans.iter().position(|x| *x == body) {
            let ordinal = if i == 19 { "tjuende".to_owned() } else { number::ordinal(&(i + 1).to_string()).unwrap() };
            v.push(format!("{prefix}den {ordinal} århundre"));
        }
    }
    v
}

fn hour(s: &str) -> Option<String> {
    if s.len() > 2 || s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: u32 = s.parse().ok()?;
    if n > 23 {
        return None;
    }
    number::cardinal(&n.to_string(), false)
}
fn minsec(s: &str, zero_empty: bool) -> Option<String> {
    if s.len() != 2 || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    let n: u32 = s.parse().ok()?;
    if n > 59 {
        return None;
    }
    if n == 0 && zero_empty {
        return Some(String::new());
    }
    if n < 10 { Some(format!("null {}", number::cardinal(&n.to_string(), false)?)) } else { number::cardinal(s, false) }
}
fn base_time(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let mut parts = s.split(|c| c == ':' || c == '.');
    let first = parts.next().unwrap_or("");
    let Some(second) = parts.next() else { return out };
    let third = parts.next();
    if parts.next().is_some() {
        return out;
    }
    // Validate the minute and optional second before spelling the hour.
    if second.len() != 2
        || !second.bytes().all(|b| b.is_ascii_digit())
        || second.as_bytes()[0] > b'5'
        || third.is_some_and(|t| t.len() != 2 || !t.bytes().all(|b| b.is_ascii_digit()) || t.as_bytes()[0] > b'5')
    {
        return out;
    }
    if let (Some(h), Some(m)) = (hour(first), minsec(second, true)) {
        if let Some(third) = third {
            if let Some(sec) = minsec(third, false) {
                out.push(format!("{h} {m} {sec}"));
                if second == "00" {
                    out.push(format!("{h} null null {sec}"));
                }
            }
        } else {
            out.push(if m.is_empty() { h } else { format!("{h} {m}") });
        }
    }
    out
}
const TIME_SUFFIXES: &[(&str, &str)] = &[
    ("am", "formiddag"),
    ("AM", "formiddag"),
    ("a.m.", "formiddag"),
    ("f.m.", "formiddag"),
    ("pm", "ettermiddag"),
    ("PM", "ettermiddag"),
    ("p.m.", "ettermiddag"),
    ("e.m.", "ettermiddag"),
];
const TIME_ZONES: &[(&str, &str)] = &[
    ("UTC", "u t c"),
    ("GMT", "g m t"),
    ("CET", "c e t"),
    ("EST", "e s t"),
    ("PST", "p s t"),
    ("cet", "c e t"),
    ("est", "e s t"),
];

/// A clock ends in a digit, meridiem suffix, or timezone key.
pub fn time_possible_end(s: &str) -> bool {
    s.as_bytes().last().is_some_and(u8::is_ascii_digit)
        || TIME_SUFFIXES.iter().any(|(key, _)| s.ends_with(key))
        || TIME_ZONES.iter().any(|(key, _)| s.ends_with(key))
}

fn single_time(s: &str) -> Vec<String> {
    let mut out = base_time(s);
    for prefix in ["kl. ", "Kl. ", "kl ", "Kl ", "klokken "] {
        if let Some(t) = s.strip_prefix(prefix) {
            for x in base_time(t).into_iter().chain(hour(t)) {
                out.push(format!("klokken {x}"));
            }
        }
    }
    for &(suffix, word) in TIME_SUFFIXES {
        if let Some(t) = s.strip_suffix(suffix) {
            let t = opt_end_space(t);
            for x in base_time(t).into_iter().chain(hour(t)) {
                out.push(format!("{x} {word}"));
            }
        }
    }
    out
}
pub fn time(s: &str) -> Vec<String> {
    let mut out = single_time(s);
    for (a, b) in split_dash(s) {
        for x in base_time(opt_end_space(a)) {
            for y in base_time(opt_space(b)) {
                out.push(format!("{x} til {y}"));
            }
        }
    }
    for &(tz, word) in TIME_ZONES {
        for (i, _) in s.match_indices(tz) {
            let before = opt_end_space(&s[..i]);
            let after = &s[i + tz.len()..];
            let offset = if after.is_empty() {
                Some(String::new())
            } else {
                let (sign, n) = if let Some(x) = after.strip_prefix('+') {
                    ("pluss", x)
                } else if let Some(x) = after.strip_prefix('-').or_else(|| after.strip_prefix('−')) {
                    ("minus", x)
                } else {
                    continue;
                };
                number::cardinal(n, false).map(|x| format!(" {sign} {x}"))
            };
            if let Some(offset) = offset {
                for x in single_time(before) {
                    out.push(format!("{x} {word}{offset}"));
                }
            }
        }
    }
    out
}

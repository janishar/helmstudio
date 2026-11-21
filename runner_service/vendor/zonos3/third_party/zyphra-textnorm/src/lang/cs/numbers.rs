use super::graph_utils::{Gender, positive, under_thousand};

pub fn unsigned(s: &str) -> Option<String> {
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return positive(s);
    }
    let mut groups = s.split(|c| matches!(c, ' ' | '.' | '\u{a0}' | '\u{202f}'));
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut count = 0;
    for g in groups.clone() {
        if g.len() != 3 || !g.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        count += 1;
    }
    if count == 0 {
        return None;
    }
    let mut joined = String::with_capacity(first.len() + count * 3);
    joined.push_str(first);
    for g in groups {
        joined.push_str(g);
    }
    positive(&joined)
}
pub fn cardinal(s: &str) -> Option<String> {
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        Some(format!("mínus {}", unsigned(rest)?))
    } else {
        unsigned(s)
    }
}
pub fn split_range(s: &str) -> impl Iterator<Item = (&str, &str)> {
    s.char_indices().filter_map(move |(i, c)| {
        if i == 0 || !matches!(c, '-' | '–' | '—') {
            return None;
        }
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let tail = &s[i + c.len_utf8()..];
        let b = tail.strip_prefix(' ').unwrap_or(tail);
        Some((a, b))
    })
}
pub fn cardinal_full(s: &str) -> Option<String> {
    const DECADES: &[(&str, &str)] = &[
        ("2020s", "dvacátá léta"),
        ("2010s", "desátá léta"),
        ("2000s", "nultá léta"),
        ("1990s", "devadesátá léta"),
        ("1980s", "osmdesátá léta"),
        ("1970s", "sedmdesátá léta"),
        ("1960s", "šedesátá léta"),
        ("1950s", "padesátá léta"),
        ("1940s", "čtyřicátá léta"),
        ("1930s", "třicátá léta"),
        ("1920s", "dvacátá léta"),
        ("90. léta", "devadesátá léta"),
        ("90.léta", "devadesátá léta"),
        ("80. léta", "osmdesátá léta"),
        ("80.léta", "osmdesátá léta"),
        ("70. léta", "sedmdesátá léta"),
        ("70.léta", "sedmdesátá léta"),
        ("60. léta", "šedesátá léta"),
        ("60.léta", "šedesátá léta"),
        ("1990-léta", "devadesátá léta"),
    ];
    if let Some((_, v)) = DECADES.iter().find(|(k, _)| *k == s) {
        return Some((*v).to_owned());
    }
    if let Some(v) = cardinal(s) {
        return Some(v);
    }
    for (a, b) in split_range(s) {
        if let (Some(a), Some(b)) = (cardinal(a), unsigned(b)) {
            return Some(format!("{a} až {b}"));
        }
    }
    None
}
fn fraction_digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    // In the optimized source union, a two-digit fractional tens reading
    // follows the digit-sequence path; teens still take the cardinal path.
    if s.len() == 2 && s.as_bytes()[0] >= b'2' {
        return Some(s.bytes().map(|b| positive(&(b as char).to_string()).unwrap()).collect::<Vec<_>>().join(" "));
    }
    if s.len() <= 3 && !s.starts_with('0') {
        return positive(s);
    }
    if s.len() == 1 {
        return positive(s);
    }
    if s.len() == 2 && s.starts_with('0') {
        return Some(format!("nula {}", positive(&s[1..])?));
    }
    Some(s.bytes().map(|b| positive(&(b as char).to_string()).unwrap()).collect::<Vec<_>>().join(" "))
}
pub fn decimal(s: &str) -> Option<(String, f64)> {
    let (negative, s) =
        if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { (true, t) } else { (false, s) };
    let (idx, sep) = if let Some(i) = s.find(',') { (i, ',') } else { (s.find('.')?, '.') };
    let a = &s[..idx];
    let b = &s[idx + 1..];
    if sep == '.' && (a.len() > 3 || (a.len() > 1 && a.starts_with('0')) || !a.bytes().all(|b| b.is_ascii_digit())) {
        return None;
    }
    let whole = match a {
        "0" => "nula celých".to_owned(),
        "1" => "jedna celá".to_owned(),
        "2" => "dvě celé".to_owned(),
        "3" => "tři celé".to_owned(),
        "4" => "čtyři celé".to_owned(),
        _ => format!("{} celých", unsigned(a)?),
    };
    Some((
        format!("{}{} {}", if negative { "mínus " } else { "" }, whole, fraction_digits(b)?),
        if sep == '.' { 0.1 } else { 0.0 },
    ))
}
pub fn number_weighted(s: &str) -> Option<(String, f64)> {
    cardinal(s).map(|v| (v, 0.0)).or_else(|| decimal(s))
}
const FRACTIONS: &[(&str, &str)] = &[
    ("½", "jedna polovina"),
    ("⅓", "jedna třetina"),
    ("⅔", "dvě třetiny"),
    ("¼", "jedna čtvrtina"),
    ("¾", "tři čtvrtiny"),
    ("⅕", "jedna pětina"),
    ("⅖", "dvě pětiny"),
    ("⅗", "tři pětiny"),
    ("⅘", "čtyři pětiny"),
    ("⅙", "jedna šestina"),
    ("⅚", "pět šestin"),
    ("⅛", "jedna osmina"),
    ("⅜", "tři osminy"),
    ("⅝", "pět osmin"),
    ("⅞", "sedm osmin"),
    ("⅐", "jedna sedmina"),
    ("⅑", "jedna devítina"),
    ("⅒", "jedna desetina"),
    ("1/2", "jedna polovina"),
    ("1/3", "jedna třetina"),
    ("2/3", "dvě třetiny"),
    ("1/4", "jedna čtvrtina"),
    ("3/4", "tři čtvrtiny"),
    ("1/5", "jedna pětina"),
    ("2/5", "dvě pětiny"),
    ("3/5", "tři pětiny"),
    ("4/5", "čtyři pětiny"),
    ("1/6", "jedna šestina"),
    ("5/6", "pět šestin"),
    ("1/7", "jedna sedmina"),
    ("1/8", "jedna osmina"),
    ("3/8", "tři osminy"),
    ("5/8", "pět osmin"),
    ("7/8", "sedm osmin"),
    ("1/9", "jedna devítina"),
    ("1/10", "jedna desetina"),
    ("11/12", "jedenáct dvanáctin"),
    ("21/100", "dvacet jedna setin"),
    ("22/7", "dvacet dvě sedmin"),
    ("125/1000", "sto dvacet pět tisícin"),
    ("101/250", "sto jedna dvě stě padesátin"),
];
/// Every concatenated or spaced mixed-fraction branch in the source union.
pub fn mixed_fraction_paths(input: &str) -> Vec<String> {
    let (sign, s) = if let Some(t) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        ("mínus ", t)
    } else {
        ("", input)
    };
    let mut paths = Vec::new();
    for &(key, val) in FRACTIONS {
        let Some(prefix) = s.strip_suffix(key) else { continue };
        let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
        if prefix.is_empty() || prefix.len() > 3 || prefix.starts_with('0') {
            continue;
        }
        if let Ok(n) = prefix.parse::<usize>() {
            if (1..=999).contains(&n) {
                let whole = under_thousand(n, Gender::Feminine);
                paths.push(format!("{sign}{whole} a {val}"));
                paths.push(format!("{sign}{whole} celé {val}"));
            }
        }
    }
    paths
}
pub fn decimal_full(s: &str) -> Option<(String, f64)> {
    if let Some(x) = decimal(s) {
        return Some(x);
    }
    let (sign, s) =
        if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("mínus ", t) } else { ("", s) };
    if s.contains('/') || !s.is_ascii() {
        // A direct entry and a concatenated mixed-fraction parse can overlap.
        // Keep the complete numerator/denominator interpretation when present.
        if let Some((_, val)) = FRACTIONS.iter().find(|(key, _)| *key == s) {
            return Some((format!("{sign}{val}"), 0.0));
        }
        for &(key, val) in FRACTIONS {
            if let Some(prefix) = s.strip_suffix(key) {
                let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
                if !prefix.is_empty() && prefix.len() <= 3 && !prefix.starts_with('0') {
                    if let Ok(n) = prefix.parse::<usize>() {
                        if (1..=999).contains(&n) {
                            return Some((format!("{sign}{} a {val}", under_thousand(n, Gender::Feminine)), 0.0));
                        }
                    }
                }
            }
        }
    }
    let (sym, stem) = if let Some(t) = s.strip_suffix('%') {
        ('%', t)
    } else if let Some(t) = s.strip_suffix('‰') {
        ('‰', t)
    } else {
        return None;
    };
    let stem = stem.strip_suffix(' ').unwrap_or(stem);
    if sym == '‰' {
        let (v, w) = number_weighted(stem)?;
        return Some((format!("{sign}{v} promile"), w));
    }
    let (prefix, stem) = if let Some(t) = stem.strip_prefix('±').or_else(|| stem.strip_prefix("+/-")) {
        ("plus mínus ", t.strip_prefix(' ').unwrap_or(t))
    } else {
        ("", stem)
    };
    for (a, b) in split_range(stem) {
        if let (Some((a, wa)), Some((b, wb))) = (number_weighted(a), decimal_full(&format!("{b}%"))) {
            return Some((format!("{sign}{prefix}{a} až {b}"), wa + wb));
        }
    }
    let (v, w) = match stem {
        "1" => ("jedno procento".to_owned(), 0.0),
        "2" => ("dvě procenta".to_owned(), 0.0),
        "3" => ("tři procenta".to_owned(), 0.0),
        "4" => ("čtyři procenta".to_owned(), 0.0),
        _ => {
            let (v, w) = number_weighted(stem)?;
            (format!("{v} procent"), w)
        }
    };
    Some((format!("{sign}{prefix}{v}"), w))
}

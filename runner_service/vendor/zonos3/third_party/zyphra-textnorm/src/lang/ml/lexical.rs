//! Whitelist, Roman, electronic, serial and telephone classifiers.
use super::{number, numeric::Reading};
use crate::text::space_to_nbsp;
crate::tsv!(WHITELIST, "ml/whitelist/abbreviations.tsv");
crate::tsv!(ROMANS, "ml/roman/roman_to_spoken.tsv");
crate::tsv!(ORDS, "ml/ordinal/ordinals.tsv");
crate::tsv!(ORDS_MATHE, "ml/ordinal/ordinals_mathe.tsv");

fn name(s: &str, cost: i64, rank: i32, out: &mut Vec<Reading>) {
    out.push((format!("name: \"{}\"", space_to_nbsp(s)), cost, rank));
}
pub(super) fn roman_key(s: &str) -> bool {
    matches!(
        s,
        "അധ്യായം"
            | "ഭാഗം"
            | "ചന്ദ്രയാൻ"
            | "മഹായുദ്ധം"
            | "ലോക മഹായുദ്ധം"
            | "ക്ലാസ്"
            | "നൂറ്റാണ്ട്"
            | "ഖണ്ഡം"
            | "രംഗം"
            | "വാല്യം"
            | "പതിപ്പ്"
            | "ഘട്ടം"
            | "തലമുറ"
            | "സ്റ്റേജ്"
            | "ഗ്രൂപ്പ്"
    )
}

pub fn whitelist(s: &str, out: &mut Vec<Reading>) {
    for row in WHITELIST.exact(s) {
        name(&row.value, 1_010_000 + row.cost, 0, out);
    }
    if !s.as_bytes().first().is_some_and(u8::is_ascii_uppercase) || !s.contains('.') {
        return;
    }
    for sep in [". ", "."] {
        let p: Vec<&str> = s.strip_suffix('.').unwrap_or(s).split(sep).collect();
        if p.len() >= 3 && p.iter().all(|x| x.len() == 1 && x.as_bytes()[0].is_ascii_uppercase()) {
            name(&p.concat(), 1_010_000, 0, out);
        }
    }
}

pub fn roman(s: &str, out: &mut Vec<Reading>) {
    for row in ROMANS.rows() {
        for sep in ["-", " "] {
            if let Some(key) = s.strip_suffix(row.key.as_str()).and_then(|s| s.strip_suffix(sep)) {
                if roman_key(key) {
                    out.push((
                        format!(
                            "roman {{ preserve_order: true key_cardinal: \"{}\" integer: \"{}\" }}",
                            space_to_nbsp(key),
                            row.key
                        ),
                        1_100_000 + row.cost,
                        11,
                    ));
                }
            }
            if let Some(key) = s.strip_prefix(row.key.as_str()).and_then(|s| s.strip_prefix(sep)) {
                if roman_key(key) {
                    out.push((
                        format!(
                            "roman {{ preserve_order: true integer: \"{}\" key_cardinal: \"{}\" }}",
                            row.key,
                            space_to_nbsp(key)
                        ),
                        1_100_000 + row.cost,
                        11,
                    ));
                }
            }
        }
        for (suffix, table) in [("-ാമത്തെ", &*ORDS_MATHE), ("ാമത്തെ", &*ORDS_MATHE), ("-ാം", &*ORDS), ("ാം", &*ORDS)]
        {
            let Some(rest) = s.strip_prefix(row.key.as_str()).and_then(|s| s.strip_prefix(suffix)) else { continue };
            let n = match row.key.as_str() {
                "I" => 1,
                "II" => 2,
                "III" => 3,
                "IV" => 4,
                "V" => 5,
                "VI" => 6,
                "VII" => 7,
                "VIII" => 8,
                "IX" => 9,
                "X" => 10,
                "XI" => 11,
                "XII" => 12,
                "XX" => 20,
                "XXI" => 21,
                _ => continue,
            };
            if suffix.contains("മത്തെ") && n > 12 {
                continue;
            }
            let number_key = n.to_string();
            let Some(ord) = table.exact(&number_key).next() else { continue };
            let word = if suffix.contains("മത്തെ") && n == 10 {
                "പത്താാമത്തെ"
            } else {
                &ord.value
            };
            let key = if rest.is_empty() {
                String::new()
            } else if let Some(k) = rest.strip_prefix(' ') {
                if !roman_key(k) {
                    continue;
                }
                format!(" key_cardinal: \"{}\"", space_to_nbsp(k))
            } else {
                continue;
            };
            out.push((format!("roman {{ preserve_order: true default_ordinal: \"{word}\"{key} }}"), 1_100_000, 11));
        }
    }
}

fn domain(s: &str) -> bool {
    let Some((prefix, suffix)) = s.rsplit_once('.') else { return false };
    suffix.len() >= 2
        && suffix.chars().all(|c| c.is_ascii_alphabetic())
        && prefix.split('.').all(|x| {
            !x.is_empty()
                && x.chars().all(|c| c.is_ascii_alphanumeric() || number::digit(c).is_some() || matches!(c, '-' | '_'))
        })
}
fn urlchar(c: char) -> bool {
    c.is_ascii_alphanumeric()
        || number::digit(c).is_some()
        || matches!(c, '.' | '/' | ':' | '-' | '_' | '~' | '?' | '=' | '&' | '%' | '#')
}
pub fn electronic(s: &str, out: &mut Vec<Reading>) {
    if let Some((user, dom)) = s.split_once('@') {
        if !user.is_empty()
            && user
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || number::digit(c).is_some() || matches!(c, '.' | '-' | '_' | '+'))
            && domain(dom)
        {
            out.push((format!("electronic {{ username: \"{user}\" domain: \"{dom}\" }}"), 1_100_000, 9));
        }
    }
    for (p, prefix) in [("https", "https://"), ("http", "http://")] {
        if let Some(d) = s.strip_prefix(prefix) {
            if !d.is_empty() && d.chars().all(urlchar) {
                out.push((format!("electronic {{ protocol: \"{p}\" domain: \"{d}\" }}"), 1_100_000, 9));
            }
        }
    }
    if s.strip_prefix("www.").is_some_and(|d| !d.is_empty() && d.chars().all(urlchar)) || domain(s) || {
        let mut p = s.split('.');
        (0..4).all(|_| p.next().is_some_and(|x| !x.is_empty() && x.len() <= 3 && x.bytes().all(|b| b.is_ascii_digit())))
            && p.next().is_none()
    } {
        out.push((format!("electronic {{ domain: \"{s}\" }}"), 1_100_000, 9));
    }
}

fn read_phone(s: &str) -> Option<String> {
    let d: String = s.chars().filter(|&c| number::digit(c).is_some()).collect();
    number::digitwise(&d)
}
fn grouped(s: &str) -> bool {
    let mut prev = false;
    let mut first = true;
    for c in s.chars() {
        if number::digit(c).is_some() {
            prev = false;
            first = false;
        } else if matches!(c, ' ' | '-' | '.') && !first && !prev {
            prev = true;
        } else {
            return false;
        }
    }
    !first && !prev
}
fn landline(s: &str) -> bool {
    let s = s.strip_prefix('(').unwrap_or(s);
    if !s.starts_with('0') && !s.starts_with('൦') {
        return false;
    }
    for count in 3..=5 {
        let Some((end, _)) = s.char_indices().nth(count) else { continue };
        let std = &s[..end];
        if !std.chars().all(|c| number::digit(c).is_some()) {
            continue;
        }
        let rest = &s[end..];
        let rest = rest.strip_prefix(')').unwrap_or(rest);
        let rest = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')).unwrap_or(rest);
        let digits = rest.chars().filter(|c| number::digit(*c).is_some()).count();
        if (5..=8).contains(&digits) && grouped(rest) {
            return true;
        }
    }
    false
}
pub fn telephone(s: &str, out: &mut Vec<Reading>) {
    if s.chars().filter(|&c| number::digit(c).is_some()).take(7).count() < 7 {
        return;
    }
    if let Some(without_space) = s.strip_suffix(' ') {
        if !without_space.ends_with(' ') {
            let mut extra = Vec::new();
            telephone(without_space, &mut extra);
            out.extend(extra.into_iter().filter(|(tag, _, _)| tag.starts_with("telephone {")));
        }
    }
    if let Some(body) = s.strip_prefix('+') {
        if body.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
            let d: String = body.chars().filter(char::is_ascii_digit).collect();
            if (7..=15).contains(&d.len()) {
                if let Some(w) = read_phone(&d) {
                    name(&format!("പ്ലസ് {w}"), 1_100_000, 10, out);
                }
            }
        }
        for (byte, _) in body.char_indices().skip(1).take(3) {
            let cc = &body[..byte];
            let rest = &body[byte..];
            if !cc.chars().all(|c| number::digit(c).is_some()) {
                continue;
            }
            let rest = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')).unwrap_or(rest);
            let count = rest.chars().filter(|c| number::digit(*c).is_some()).count();
            if (6..=12).contains(&count) && grouped(rest) {
                if let (Some(a), Some(b)) = (read_phone(cc), read_phone(rest)) {
                    out.push((
                        format!("telephone {{ country_code: \"പ്ലസ് {a} \" number_part: \"{b} \" }}"),
                        1_100_000,
                        10,
                    ));
                }
            }
        }
    }
    let d: String = s.chars().filter(|c| number::digit(*c).is_some()).collect();
    let starts_mobile = d.chars().next().and_then(number::digit).is_some_and(|d| d >= 6);
    let groups: Vec<&str> = s.split([' ', '-', '.']).collect();
    let mobile = starts_mobile
        && d.chars().count() == 10
        && grouped(s)
        && (groups.len() == 1
            || groups.len() == 2 && groups[0].chars().count() == 5 && groups[1].chars().count() == 5
            || groups.len() == 3
                && groups[0].chars().count() == 3
                && groups[1].chars().count() == 3
                && groups[2].chars().count() == 4);
    let landline = landline(s);
    if mobile || landline {
        // A complete national-phone pattern is more specific than an equal-cost cardinal.
        if let Some(w) = read_phone(s) {
            out.push((format!("telephone {{ number_part: \"{w} \" }}"), 1_100_000, -1));
        }
    }
}

const LETTERS: [&str; 26] = [
    "എ",
    "ബി",
    "സി",
    "ഡി",
    "ഇ",
    "എഫ്",
    "ജി",
    "എച്ച്",
    "ഐ",
    "ജെ",
    "കെ",
    "എൽ",
    "എം",
    "എൻ",
    "ഒ",
    "പി",
    "ക്യു",
    "ആർ",
    "എസ്",
    "ടി",
    "യു",
    "വി",
    "ഡബ്ല്യു",
    "എക്സ്",
    "വൈ",
    "സെഡ്",
];
fn letters(s: &str) -> Option<String> {
    if s.chars().all(|c| c.is_ascii_alphabetic()) && !s.is_empty() {
        Some(s.bytes().map(|b| LETTERS[(b.to_ascii_uppercase() - b'A') as usize]).collect::<Vec<_>>().join(" "))
    } else if !s.is_empty()
        && s.chars().all(|c| {
            ('\u{d00}'..='\u{d7f}').contains(&c) && number::digit(c).is_none() || c == '\u{200c}' || c == '\u{200d}'
        })
    {
        Some(s.to_owned())
    } else {
        None
    }
}
pub fn serial(s: &str, out: &mut Vec<Reading>) {
    if let Some((base, exp)) = s.split_once('^') {
        if exp.chars().count() <= 3 && base.chars().count() <= 4 {
            if let Some((e, ec)) = number::final_cardinal(exp) {
                let b = number::final_cardinal(base).map(|x| x.0).or_else(|| {
                    (base.len() == 1 && base.chars().all(|c| c.is_ascii_alphabetic())).then(|| base.to_owned())
                });
                if let Some(b) = b {
                    name(&format!("{b} ഘാതം {e}"), 1_110_000 + ec, 12, out);
                }
            }
        }
    }
    for pre in ["കോവിഡ്-", "COVID-", "Covid-", "covid-"] {
        if let Some(n) = s.strip_prefix(pre) {
            if n.chars().count() <= 3 {
                if let Some((w, c)) = number::final_cardinal(n) {
                    name(&format!("കോവിഡ്-{w}"), 1_110_000 + c, 12, out);
                }
            }
        }
    }
    if s == "H2O" || s == "h2o" {
        name("എച്ച് രണ്ട് ഒ", 1_110_000, 12, out)
    }
    let mut groups: Vec<(bool, String)> = Vec::new();
    for c in s.chars() {
        if c == '-' {
            groups.push((false, "-".into()));
            continue;
        }
        let is_num = number::digit(c).is_some();
        if let Some(last) = groups.last_mut() {
            if last.0 == is_num && last.1 != "-" {
                last.1.push(c);
                continue;
            }
        }
        groups.push((is_num, c.to_string()));
    }
    if groups.len() >= 2 && groups.iter().any(|x| x.0) && groups.iter().any(|x| !x.0 && x.1 != "-") {
        let mut words = String::new();
        let mut prev_hyphen = false;
        let mut valid = true;
        let mut weight = 0;
        for (is_num, g) in &groups {
            if g == "-" {
                if words.is_empty() || prev_hyphen {
                    valid = false;
                    break;
                }
                words.push('-');
                prev_hyphen = true;
                continue;
            }
            let w = if *is_num {
                match number::final_cardinal(g) {
                    Some((w, c)) => {
                        weight += c;
                        w
                    }
                    None => {
                        valid = false;
                        break;
                    }
                }
            } else {
                match letters(g) {
                    Some(w) => w,
                    None => {
                        valid = false;
                        break;
                    }
                }
            };
            if !words.is_empty() && !prev_hyphen {
                words.push(' ')
            }
            words.push_str(&w);
            prev_hyphen = false;
        }
        if valid && !prev_hyphen {
            name(&words, 1_110_000 + weight, 12, out)
        }
    }
    let (prefix, version) = if let Some((p, v)) = s.split_once(' ') {
        if p.chars().all(|c| c.is_ascii_alphabetic()) { (format!("{p} "), v) } else { (String::new(), s) }
    } else if s.starts_with('v') || s.starts_with('V') {
        (format!("{} ", &s[..1]), &s[1..])
    } else {
        (String::new(), s)
    };
    let parts: Vec<&str> = version.split('.').collect();
    if parts.len() >= 2 {
        let mut w = Vec::new();
        let mut c = 0;
        for p in parts {
            if let Some((v, x)) = number::final_cardinal(p) {
                w.push(v);
                c += x
            } else {
                return;
            }
        }
        name(&format!("{prefix}{}", w.join(" ഡോട്ട് ")), 1_110_000 + c, 12, out);
    }
}

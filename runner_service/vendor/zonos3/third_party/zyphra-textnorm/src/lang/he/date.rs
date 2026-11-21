//! Date readings, including the separately tagged ISO branch.
use super::cardinal::base_num;
use std::collections::HashMap;
use std::sync::LazyLock;

const MONTHS: [&str; 12] = [
    "בינואר",
    "בפברואר",
    "במרץ",
    "באפריל",
    "במאי",
    "ביוני",
    "ביולי",
    "באוגוסט",
    "בספטמבר",
    "באוקטובר",
    "בנובמבר",
    "בדצמבר",
];
const NAMES: [&[&str]; 12] = [
    &["ינואר", "בינואר", "January", "Jan"],
    &["פברואר", "בפברואר", "February", "Feb"],
    &["מרץ", "במרץ", "March", "Mar"],
    &["אפריל", "באפריל", "April", "Apr"],
    &["מאי", "במאי", "May"],
    &["יוני", "ביוני", "June", "Jun"],
    &["יולי", "ביולי", "July", "Jul"],
    &["אוגוסט", "באוגוסט", "August", "Aug"],
    &["ספטמבר", "בספטמבר", "September", "Sep", "Sept"],
    &["אוקטובר", "באוקטובר", "October", "Oct"],
    &["נובמבר", "בנובמבר", "November", "Nov"],
    &["דצמבר", "בדצמבר", "December", "Dec"],
];

const ERAS: &[&str] = &[
    "לפנה\"ס",
    "לפנה״ס",
    "לפנהס",
    "לפנה\"צ",
    "לפני הספירה",
    "BC",
    "B.C.",
    "BCE",
    "B.C.E.",
    "לספירה",
    "לסה\"נ",
    "לסה״נ",
    "AD",
    "A.D.",
    "CE",
    "C.E.",
];

/// Source month and era spellings grouped by their first character.
static DATE_STARTS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for &word in NAMES.iter().flat_map(|names| names.iter()).chain(ERAS.iter()) {
        if let Some(first) = word.chars().next() {
            index.entry(first).or_default().push(word);
        }
    }
    index
});

fn matching_words(tail: &str) -> Option<&'static Vec<&'static str>> {
    DATE_STARTS.get(&tail.chars().next()?)
}

/// A digit-led date can only continue into a numeric delimiter, a month, or an era.
pub(crate) fn may_continue(s: &str) -> bool {
    if !s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        return true;
    }
    let Some(i) = s.bytes().position(|b| !b.is_ascii_digit()) else { return true };
    let rest = &s[i..];
    if rest.chars().next().is_some_and(|c| matches!(c, '/' | '.' | '-' | '–' | '—' | '־' | ',' | 's')) {
        return true;
    }
    let tail = rest.trim_start_matches(' ');
    if tail.is_empty() {
        return true;
    }
    matching_words(tail).is_some_and(|words| words.iter().any(|word| word.starts_with(tail) || tail.starts_with(word)))
}

/// Necessary lexical shape for a date, without allocating split fields.
pub(crate) fn may_match(s: &str) -> bool {
    if !s.bytes().any(|b| b.is_ascii_digit()) {
        return false;
    }
    if !s.as_bytes().first().is_some_and(u8::is_ascii_digit) {
        return true;
    }
    let Some(i) = s.bytes().position(|b| !b.is_ascii_digit()) else { return false };
    let rest = &s[i..];
    if rest.chars().next().is_some_and(|c| matches!(c, '/' | '.' | '-' | '–' | '—' | '־' | ',' | 's')) {
        return true;
    }
    let tail = rest.trim_start_matches(' ');
    matching_words(tail).is_some_and(|words| words.iter().any(|word| tail.starts_with(word)))
}
fn month(s: &str) -> Option<&'static str> {
    if s.len() <= 2 && s.bytes().all(|b| b.is_ascii_digit()) {
        let n: usize = s.parse().ok()?;
        return MONTHS.get(n.checked_sub(1)?).copied();
    }
    NAMES.iter().position(|names| names.contains(&s)).map(|i| MONTHS[i])
}
fn day(s: &str) -> Option<String> {
    if s.is_empty() || s.len() > 2 || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n: u8 = s.parse().ok()?;
    if !(1..=31).contains(&n) {
        return None;
    }
    if n == 2 { Some("שני".into()) } else { base_num(&n.to_string(), true) }
}
/// `(tag, weight)`, where names are encoded as `name:` fields here.
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    let mut add = |name: String, weight: i64| {
        out.push((format!("name: \"{}\"", name.replace(' ', "\u{a0}")), weight));
    };
    for sep in ['/', '.'] {
        let p: Vec<_> = s.split(sep).collect();
        if p.len() == 3 {
            if let (Some(d), Some(m), Some(y)) = (day(p[0]), month(p[1]), base_num(p[2], false)) {
                add(format!("{d} {m} {y}"), 2_700_000);
                if matches!(p[0], "1" | "01") {
                    add(format!("ראשון {m} {y}"), 2_750_000);
                }
            }
        } else if sep == '/' && p.len() == 2 && p[0].parse::<u8>().map_or(false, |n| n >= 13) {
            if let (Some(d), Some(m)) = (day(p[0]), month(p[1])) {
                add(format!("{d} {m}"), 1_600_000);
            }
        }
    }
    let words: Vec<_> = s.split(' ').filter(|p| !p.is_empty()).collect();
    if !s.starts_with(' ') && !s.ends_with(' ') && (words.len() == 2 || words.len() == 3) {
        if let (Some(d), Some(m)) =
            (day(words[0]), if words[1].chars().all(|c| c.is_ascii_digit()) { None } else { month(words[1]) })
        {
            if words.len() == 2 {
                add(format!("{d} {m}"), 2_730_000);
                if matches!(words[0], "1" | "01") {
                    add(format!("ראשון {m}"), 2_780_000);
                }
            } else if let Some(y) = base_num(words[2], false) {
                add(format!("{d} {m} {y}"), 2_680_000);
                if matches!(words[0], "1" | "01") {
                    add(format!("ראשון {m} {y}"), 2_730_000);
                }
            }
        }
        if words.len() == 3 && words[0].bytes().all(|b| b.is_ascii_alphabetic()) {
            if let (Some(m), Some(d), Some(y)) =
                (month(words[0]), day(words[1].strip_suffix(',').unwrap_or(words[1])), base_num(words[2], false))
            {
                add(format!("{d} {m} {y}"), 2_700_000);
                if matches!(words[1], "1" | "01" | "1," | "01,") {
                    add(format!("ראשון {m} {y}"), 2_750_000);
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '־') {
            let (a, b) = (&s[..i], &s[i + c.len_utf8()..]);
            if a.len() == 4 && b.len() == 4 {
                if let (Some(a), Some(b)) = (base_num(a, false), base_num(b, false)) {
                    add(format!("{a} עד {b}"), 1_820_000);
                }
            }
        }
    }
    for (era, spoken) in [
        ("לפנה\"ס", "לפני הספירה"),
        ("לפנה״ס", "לפני הספירה"),
        ("לפנהס", "לפני הספירה"),
        ("לפנה\"צ", "לפני הספירה"),
        ("לפני הספירה", "לפני הספירה"),
        ("BC", "לפני הספירה"),
        ("B.C.", "לפני הספירה"),
        ("BCE", "לפני הספירה"),
        ("B.C.E.", "לפני הספירה"),
        ("לספירה", "לספירה"),
        ("לסה\"נ", "לספירה"),
        ("לסה״נ", "לספירה"),
        ("לספירה הנוצרית", "לספירה"),
        ("AD", "לספירה"),
        ("A.D.", "לספירה"),
        ("CE", "לספירה"),
        ("C.E.", "לספירה"),
    ] {
        if let Some(prefix) = s.strip_suffix(era) {
            if let Some(n) = base_num(prefix.strip_suffix(' ').unwrap_or(prefix), false) {
                add(format!("{n} {spoken}"), 1_770_000);
            }
        }
    }
    let decade = s.strip_prefix("שנות ה-").map(|x| (x, true)).or_else(|| s.strip_suffix('s').map(|x| (x, false)));
    if let Some((n, he)) = decade {
        let val = match n {
            "20" | "1920" => Some("שנות העשרים"),
            "30" | "1930" => Some("שנות השלושים"),
            "40" | "1940" => Some("שנות הארבעים"),
            "50" | "1950" => Some("שנות החמישים"),
            "60" | "1960" => Some("שנות השישים"),
            "70" | "1970" => Some("שנות השבעים"),
            "80" | "1980" => Some("שנות השמונים"),
            "90" | "1990" => Some("שנות התשעים"),
            "2000" => Some("שנות האלפיים"),
            "2010" => Some("שנות האלפיים ועשר"),
            "2020" => Some("שנות האלפיים עשרים"),
            "2030" if !he => Some("שנות האלפיים שלושים"),
            _ => None,
        };
        if let Some(v) = val {
            add(v.into(), 1_760_000);
        }
    }
    drop(add);
    let p: Vec<_> = s.split(|c| matches!(c, '-' | '–' | '—' | '־')).collect();
    if p.len() == 3 && p[0].len() == 4 && p[1].len() == 2 && p[2].len() == 2 {
        if let (Some(y), Some(m), Some(d)) = (base_num(p[0], false), month(p[1]), day(p[2])) {
            out.push((format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"), 880_000));
            if p[2] == "01" {
                out.push((format!("date {{ year: \"{y}\" month: \"{m}\" day: \"ראשון\" }}"), 930_000));
            }
        }
    }
    out
}

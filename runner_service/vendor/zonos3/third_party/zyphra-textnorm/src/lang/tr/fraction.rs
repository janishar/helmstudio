//! Fraction readings from `tr/taggers/fraction.py`.
use super::graph_utils::{is_positive, positive};

const COMMON: [(&str, &str); 31] = [
    ("½", "yarım"),
    ("¼", "çeyrek"),
    ("¾", "üç bölü dört"),
    ("⅓", "üçte bir"),
    ("⅔", "üçte iki"),
    ("⅕", "beşte bir"),
    ("⅖", "beşte iki"),
    ("⅗", "beşte üç"),
    ("⅘", "beşte dört"),
    ("⅙", "altıda bir"),
    ("⅚", "altıda beş"),
    ("⅛", "sekizde bir"),
    ("⅜", "sekizde üç"),
    ("⅝", "sekizde beş"),
    ("⅞", "sekizde yedi"),
    ("⅐", "yedide bir"),
    ("⅑", "dokuzda bir"),
    ("⅒", "onda bir"),
    ("1/2", "yarım"),
    ("1/4", "çeyrek"),
    ("3/4", "üç bölü dört"),
    ("1/3", "üçte bir"),
    ("2/3", "üçte iki"),
    ("1/5", "beşte bir"),
    ("2/5", "beşte iki"),
    ("3/5", "beşte üç"),
    ("4/5", "beşte dört"),
    ("1/8", "sekizde bir"),
    ("3/8", "sekizde üç"),
    ("5/8", "sekizde beş"),
    ("7/8", "sekizde yedi"),
];

pub fn count(input: &str) -> usize {
    let s = input.strip_prefix('-').or_else(|| input.strip_prefix('−')).unwrap_or(input);
    let mut total = 0;
    for (written, _) in COMMON {
        total += usize::from(s == written);
        if let Some(prefix) = s.strip_suffix(written) {
            total += usize::from(is_positive(prefix.strip_suffix(' ').unwrap_or(prefix)));
        }
    }
    if let Some((a, b)) = s.split_once('/') {
        total += usize::from(is_positive(a) && is_positive(b));
    }
    total
}

pub fn readings(input: &str) -> Vec<String> {
    let (sign, s) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(s) => ("eksi ", s),
        None => ("", input),
    };
    let mut out = Vec::new();
    for (written, spoken) in COMMON {
        if s == written {
            out.push(format!("{sign}{spoken}"));
        }
        if let Some(prefix) = s.strip_suffix(written) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if let Some(n) = positive(prefix) {
                let tail = match written {
                    "½" | "1/2" => "buçuk".to_owned(),
                    "¼" | "1/4" => "tam çeyrek".to_owned(),
                    _ => format!("tam {spoken}"),
                };
                out.push(format!("{sign}{n} {tail}"));
            }
        }
    }
    if let Some((a, b)) = s.split_once('/') {
        if let (Some(a), Some(b)) = (positive(a), positive(b)) {
            out.push(format!("{sign}{a} bölü {b}"));
        }
    }
    out
}

//! Whitelist tagger for Urdu text normalization.

use super::Sink;
use crate::text::cost;

pub const ABBREVIATIONS: &[(&str, &str)] = &[
    ("Dr.", "ڈاکٹر"),
    ("Prof.", "پروفیسر"),
    ("Mr.", "مسٹر"),
    ("Mrs.", "مسز"),
    ("Ms.", "مس"),
    ("Engr.", "انجینئر"),
    ("Lt.", "لیفٹیننٹ"),
    ("Col.", "کرنل"),
    ("Gen.", "جنرل"),
    ("Capt.", "کیپٹن"),
    ("Maj.", "میجر"),
    ("Sr.", "سینئر"),
    ("Jr.", "جونیئر"),
    ("Govt.", "گورنمنٹ"),
    ("Ltd.", "لمیٹڈ"),
    ("No.", "نمبر"),
    ("ﷺ", "صلی اللہ علیہ وآلہ وسلم"),
    ("صلعم", "صلی اللہ علیہ وسلم"),
    ("رض", "رضی اللہ عنہ"),
    ("رح", "رحمۃ اللہ علیہ"),
    ("الخ", "الیٰ آخرہ"),
    ("Pvt.", "پرائیویٹ"),
    ("Co.", "کمپنی"),
    ("etc.", "وغیرہ"),
    ("vs.", "بمقابلہ"),
    ("&", "اور"),
    ("©", "کاپی رائٹ"),
    ("®", "رجسٹرڈ"),
    ("™", "ٹریڈ مارک"),
];

pub(crate) fn possible_start(c: char) -> bool {
    c.is_ascii_uppercase() || ABBREVIATIONS.iter().any(|(key, _)| key.starts_with(c))
}

pub fn classify(input: &str, end: usize, out: &mut Sink) {
    for &(abbr, expansion) in ABBREVIATIONS {
        if input == abbr {
            out.push(end, cost(1.01), 0, || {
                let norm = expansion.replace(' ', "\u{a0}");
                format!("name: \"{}\"", norm)
            });
            return;
        }
    }

    // Acronyms: 3 or more uppercase ASCII letters separated by "." or ". ",
    // optionally ending with a "."
    // e.g. "U.S.A." -> "USA", "U.S.A" -> "USA", "U. S. A." -> "USA"
    if parse_dotted_acronym(input) {
        out.push(end, cost(1.01), 0, || {
            let acronym: String = input.chars().filter(char::is_ascii_uppercase).collect();
            format!("name: \"{}\"", acronym)
        });
    }
}

fn parse_dotted_acronym(s: &str) -> bool {
    let bytes = s.as_bytes();
    if bytes.len() < 5 {
        return false;
    }
    let mut i = 0;

    // First uppercase letter
    if !bytes[i].is_ascii_uppercase() {
        return false;
    }
    i += 1;

    let mut count = 1;
    let separator = if bytes[i..].starts_with(b". ") { b". ".as_slice() } else { b".".as_slice() };
    while i < bytes.len() {
        if !bytes[i..].starts_with(separator) {
            // The optional final period is independent of the chosen separator.
            if bytes[i..] == *b"." && count >= 3 {
                return true;
            }
            return false;
        }
        let sep_len = separator.len();
        i += sep_len;
        if i >= bytes.len() {
            return separator == b"." && count >= 3;
        }
        if !bytes[i].is_ascii_uppercase() {
            return false;
        }
        count += 1;
        i += 1;
    }

    count >= 3
}

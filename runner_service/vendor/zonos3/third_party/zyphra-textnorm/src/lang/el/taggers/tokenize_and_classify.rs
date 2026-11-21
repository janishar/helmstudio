use super::super::{RestoreWriting, WRITE_TAGS};
use super::*;
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;

#[inline]
fn boundary(c: char) -> bool {
    crate::text::is_nemo_space(c) || super::punctuation::is_mark(c)
}

/// Greek-letter starts can cross a space only through these source prefixes.
fn greek_crosses_space(first: &str) -> bool {
    STARTS.cross_space.contains(first)
}

/// A Greek word after a numeric expression can only begin one of the
/// grammar's finite unit, month, era, time-marker, quantity, or range keys.
fn numeric_greek_continuation(text: &[char]) -> bool {
    let len = text.iter().position(|c| crate::text::is_nemo_space(*c)).unwrap_or(text.len());
    let word = &text[..len];
    word.first()
        .and_then(|first| STARTS.continuation.get(first))
        .is_some_and(|keys| keys.iter().any(|key| word.starts_with(key) || key.starts_with(word)))
}

struct Starts {
    whitelist: std::collections::HashSet<char>,
    roman: std::collections::HashSet<char>,
    unit_end: std::collections::HashSet<char>,
    cross_space: std::collections::HashSet<&'static str>,
    continuation: std::collections::HashMap<char, Vec<Box<[char]>>>,
}
static STARTS: std::sync::LazyLock<Starts> = std::sync::LazyLock::new(|| {
    let mut whitelist = std::collections::HashSet::new();
    let mut roman = std::collections::HashSet::new();
    let mut unit_end = std::collections::HashSet::new();
    let mut cross_space = std::collections::HashSet::new();
    let mut continuation: std::collections::HashMap<char, Vec<Box<[char]>>> = std::collections::HashMap::new();
    for (keys, _, _) in tables::UNITS {
        for &key in *keys {
            if let Some(first) = key.chars().next() {
                continuation.entry(first).or_default().push(key.chars().collect());
            }
        }
    }
    for key in MONTHS.into_iter().chain([
        "με",
        "προς",
        "π.Χ.",
        "πΧ",
        "π.Κ.Χ.",
        "μ.Χ.",
        "μΧ",
        "Κ.Χ.",
        "π.μ.",
        "πμ",
        "μ.μ.",
        "μμ",
        "εκατομμύριο",
        "εκατομμύρια",
        "εκατ.",
        "δισεκατομμύριο",
        "δισεκατομμύρια",
        "δισ.",
        "τρισεκατομμύριο",
        "τρισεκατομμύρια",
    ]) {
        if let Some(first) = key.chars().next() {
            continuation.entry(first).or_default().push(key.chars().collect());
        }
    }
    cross_space.insert("δεκαετία");
    for table in
        [tables::TITLES, tables::NO_PREFIX, tables::MONARCH_NAMES, tables::CHAPTER_PREFIX, tables::CENTURY_WORD]
    {
        cross_space.extend(table.iter().filter_map(|(key, _)| key.split(' ').next()));
    }
    unit_end.extend(tables::UNITS.iter().flat_map(|(keys, _, _)| keys.iter().filter_map(|key| key.chars().last())));
    unit_end.extend("%‰".chars());
    for table in [tables::ABBREVS, tables::TITLES, tables::NO_PREFIX, tables::VER_PREFIX] {
        whitelist.extend(table.iter().filter_map(|(key, _)| key.chars().next()));
    }
    whitelist.extend("&©§".chars());
    for table in [tables::MONARCH_NAMES, tables::CHAPTER_PREFIX, tables::CENTURY_WORD, tables::ROMAN_TO_ORD_M] {
        roman.extend(table.iter().filter_map(|(key, _)| key.chars().next()));
    }
    Starts { whitelist, roman, unit_end, cross_space, continuation }
});

pub(in crate::lang::el) struct Union;
fn push(out: &mut Vec<Candidate>, end: usize, weight: f64, rank: i32, spoken: &str) {
    let tag = if writing_tags() { format!("name: \"{}\"", spoken.replace(' ', "\u{a0}")) } else { String::new() };
    out.push(Candidate { end, cost: cost(weight), rank, tag });
}
impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len()
            || crate::text::is_nemo_space(text[start])
            || !crate::lang::common::possible_token_start(text, start)
            || (start != 0 && !boundary(text[start - 1]))
        {
            return;
        }
        let first = text[start];
        let digit = first.is_ascii_digit();
        let numeric =
            digit || matches!(first, '-' | '−' | '~' | '±') || tables::VULGAR.iter().any(|(k, _)| k.starts_with(first));
        let monetary = numeric || first.is_ascii_alphabetic() || "€$£¥₹₽₩₺₪₴₫৳₦₱฿֏₿".contains(first);
        let phone = matches!(first, '+' | '(') || digit;
        let electronic_start = first.is_ascii_alphanumeric();
        let whitelist_start = digit || first.is_ascii_alphabetic() || STARTS.whitelist.contains(&first);
        let roman_start = STARTS.roman.contains(&first);
        let greek_word = !first.is_ascii() && first.is_alphabetic() && !numeric && !monetary;
        if !whitelist_start && !roman_start && !numeric && !monetary && !phone && !electronic_start && first != 'δ' {
            for end in start + 1..=text.len() {
                if crate::text::is_nemo_space(text[end - 1]) {
                    break;
                }
                if end == text.len() || boundary(text[end]) {
                    out.push(Candidate { end, cost: cost(100.0), rank: 13, tag: String::new() });
                }
            }
            return;
        }
        let mut first_space = true;
        let mut currency_seen = false;
        let mut non_digit_seen = false;
        let mut colon_seen = false;
        let mut slash_seen = false;
        let mut vulgar_seen = false;
        let mut hyphen_seen = false;
        let mut electronic_separator = false;
        let mut math_marker = false;
        let mut word_open = true;
        let mut cardinal_chars = true;
        let mut s = String::new();
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if greek_word && first_space && crate::text::is_nemo_space(c) {
                first_space = false;
                if c != ' ' || !greek_crosses_space(&s) {
                    break;
                }
            }
            if numeric
                && crate::text::is_nemo_space(c)
                && end < text.len()
                && !text[end].is_ascii()
                && text[end].is_alphabetic()
                && !numeric_greek_continuation(&text[end..])
            {
                break;
            }
            word_open &= !crate::text::is_nemo_space(c);
            s.push(c);
            currency_seen |= c.is_ascii_alphabetic() || "€$£¥₹₽₩₺₪₴₫৳₦₱฿֏₿¢".contains(c);
            non_digit_seen |= !c.is_ascii_digit();
            colon_seen |= c == ':';
            slash_seen |= c == '/';
            vulgar_seen |= matches!(
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
                    | '⅐'
                    | '⅑'
                    | '⅒'
            );
            hyphen_seen |= c == '-';
            electronic_separator |= matches!(c, '.' | '@' | '/');
            math_marker |= matches!(c, '+' | '=' | '^');
            cardinal_chars &=
                c.is_ascii_digit() || matches!(c, '-' | '−' | '~' | '±' | '–' | '—' | ',' | ' ' | 'μ' | 'ε');
            if !crate::lang::common::possible_token_end(text, end) || (end < text.len() && !boundary(text[end])) {
                continue;
            }
            if digit && !non_digit_seen {
                if let Some(x) = all_cardinal(&s) {
                    push(out, end, 1.10, 10, &x);
                }
                word(end, out);
                continue;
            }
            if whitelist_start && (!digit || math_marker || matches!(c, 'G' | 'K')) {
                if let Some(x) = whitelist(&s) {
                    push(out, end, 1.01, 0, &x);
                }
            }
            for sep in if digit { &[":", " προς "][..] } else { &[][..] } {
                if let Some((a, b)) = s.split_once(*sep) {
                    if let (Some(a), Some(b)) = (number::cardinal(a), number::cardinal(b)) {
                        push(out, end, 2.51, 0, &spoken!("{a} προς {b}"));
                    }
                }
            }
            if roman_start && s.contains(' ') {
                if let Some(x) = roman(&s) {
                    push(out, end, 1.02, 1, &x);
                }
                if let Some(x) = roman_chapter_ordinal(&s) {
                    push(out, end, 1.52, 18, &x);
                }
            }
            if monetary && currency_seen {
                if let Some(x) = money(&s) {
                    push(out, end, 1.03, 2, &x);
                }
                if let Some(x) = money_alternate(&s) {
                    push(out, end, 1.03, 15, &x);
                }
            }
            if digit && colon_seen {
                if let Some(x) = time(&s) {
                    push(out, end, 1.04, 3, &x);
                }
            }
            if s == "0:00" || s == "00:00" {
                push(out, end, 1.04, 14, "μηδέν");
            }
            if (digit && non_digit_seen) || first == 'δ' {
                if let Some(x) = date(&s) {
                    push(out, end, 1.05, 4, &x);
                }
            }
            if digit && hyphen_seen {
                if let Some((y, m, d)) = iso_date(&s) {
                    let tag = if writing_tags() {
                        format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}")
                    } else {
                        String::new()
                    };
                    out.push(Candidate { end, cost: cost(1.05), rank: 5, tag });
                }
            }
            if numeric {
                if STARTS.unit_end.contains(&c) {
                    if let Some(x) = measure(&s) {
                        push(out, end, 1.06, 6, &x);
                    }
                }
                if slash_seen || vulgar_seen {
                    if let Some((x, w)) = fraction(&s) {
                        push(out, end, 1.07 + w, 7, &x);
                    }
                    let unsigned = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(&s);
                    for (split, _) in unsigned.char_indices().skip(1).take_while(|(i, _)| *i <= 16) {
                        if let Some((x, w)) = mixed_fraction(&s, split, false) {
                            push(out, end, 1.07 + w, 100 + split as i32, &x);
                        }
                        if let Some((x, w)) = mixed_fraction(&s, split, true) {
                            push(out, end, 1.07 + w, 1000 + split as i32, &x);
                        }
                    }
                    if let Some(x) = fraction_fallback(&s) {
                        push(out, end, 1.57, 19, &x);
                    }
                }
                if non_digit_seen {
                    if let Some(x) = decimal(&s) {
                        push(out, end, 1.08, 8, &x);
                    }
                    if grouped(&s).is_some() {
                        if let Some(x) = decimal_core(&s) {
                            push(out, end, 1.08, 9, &x);
                        }
                    }
                }
                if matches!(c, 'ς' | 'η' | 'ο' | 'υ' | 'ι' | 'α' | 'ν') {
                    if let Some(x) = parse_ordinal(&s) {
                        push(out, end, 1.09, 9, &x);
                    }
                }
                if cardinal_chars {
                    if let Some(x) = all_cardinal(&s) {
                        push(out, end, 1.10, 10, &x);
                    }
                }
            }
            if phone && (first == '+' || first == '(' || hyphen_seen) {
                if let Some((x, w)) = telephone(&s) {
                    push(out, end, 1.11 + w, 11, &x);
                }
                if let Some(x) = telephone_local_country(&s, true) {
                    push(out, end, 1.11, 16, &x);
                }
                if let Some(x) = telephone_local_country(&s, false) {
                    push(out, end, 1.11, 17, &x);
                }
            }
            if electronic_start && electronic_separator {
                if let Some(x) = electronic(&s) {
                    push(out, end, 1.12, 12, &x);
                }
            }
            if word_open {
                word(end, out);
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.rank == 13 {
            let mut tag = String::with_capacity((candidate.end - start) * 3 + 8);
            tag.push_str("name: \"");
            tag.extend(&text[start..candidate.end]);
            tag.push('"');
            candidate.tag = tag;
            return;
        }
        let input: String = text[start..candidate.end].iter().collect();
        WRITE_TAGS.with(|flag| {
            let _restore = RestoreWriting(flag.replace(true));
            let result = (|| -> Option<String> {
                if candidate.rank == 5 {
                    let (y, m, d) = iso_date(&input)?;
                    return Some(format!("date {{ year: \"{y}\" month: \"{m}\" day: \"{d}\" }}"));
                }
                let value = match candidate.rank {
                    0 if candidate.cost == cost(2.51) => {
                        let (a, b) = input.split_once(" προς ").or_else(|| input.split_once(':'))?;
                        format!("{} προς {}", number::cardinal(a)?, number::cardinal(b)?)
                    }
                    0 => whitelist(&input)?,
                    1 => roman(&input)?,
                    2 => money(&input)?,
                    3 => time(&input)?,
                    4 => date(&input)?,
                    6 => measure(&input)?,
                    7 => fraction(&input)?.0,
                    8 => decimal(&input)?,
                    9 if candidate.cost == cost(1.08) => decimal_core(&input)?,
                    9 => parse_ordinal(&input)?,
                    10 => all_cardinal(&input)?,
                    11 => telephone(&input)?.0,
                    12 => electronic(&input)?,
                    14 => "μηδέν".to_string(),
                    15 => money_alternate(&input)?,
                    16 => telephone_local_country(&input, true)?,
                    17 => telephone_local_country(&input, false)?,
                    18 => roman_chapter_ordinal(&input)?,
                    19 => fraction_fallback(&input)?,
                    rank @ 101..=116 => mixed_fraction(&input, (rank - 100) as usize, false)?.0,
                    rank @ 1001..=1016 => mixed_fraction(&input, (rank - 1000) as usize, true)?.0,
                    _ => return None,
                };
                let mut tag = String::with_capacity(value.len() + 8);
                tag.push_str("name: \"");
                for c in value.chars() {
                    tag.push(if c == ' ' { '\u{a0}' } else { c });
                }
                tag.push('"');
                Some(tag)
            })();
            if let Some(tag) = result {
                candidate.tag = tag;
            }
        });
    }
}

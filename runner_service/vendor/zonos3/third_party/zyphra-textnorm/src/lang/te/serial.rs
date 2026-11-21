//! Telugu serial classification grammar (mixed codes, versions, powers).

use super::lazy;
use super::numbers::{self, is_digit};
use crate::lattice::Candidate;
use crate::text::cost;
use crate::tsv;

tsv!(MEASURE_UNITS_OTHER, "te/measure/unit_other.tsv");
tsv!(MEASURE_PREFIXED_OTHER, "te/measure/unit_prefixed_other.tsv");
tsv!(CURRENCY_SYMS, "te/money/currency.tsv");

type RowIndex = std::collections::HashMap<char, Vec<&'static crate::text::Row>>;
static EXCLUSIONS: std::sync::LazyLock<(RowIndex, RowIndex, RowIndex)> = std::sync::LazyLock::new(|| {
    let mut currencies_first: RowIndex = RowIndex::new();
    let mut currencies_last: RowIndex = RowIndex::new();
    let mut units_last: RowIndex = RowIndex::new();
    for row in CURRENCY_SYMS.rows() {
        if let (Some(first), Some(last)) = (row.key.chars().next(), row.key.chars().next_back()) {
            currencies_first.entry(first).or_default().push(row);
            currencies_last.entry(last).or_default().push(row);
        }
    }
    for row in MEASURE_UNITS_OTHER.rows().iter().chain(MEASURE_PREFIXED_OTHER.rows().iter()) {
        if let Some(last) = row.key.chars().next_back() {
            units_last.entry(last).or_default().push(row);
        }
    }
    (currencies_first, currencies_last, units_last)
});

pub fn letter_to_te(c: char) -> Option<&'static str> {
    match c.to_ascii_lowercase() {
        'a' => Some("ఎ"),
        'b' => Some("బి"),
        'c' => Some("సి"),
        'd' => Some("డి"),
        'e' => Some("ఇ"),
        'f' => Some("ఎఫ్"),
        'g' => Some("జి"),
        'h' => Some("హెచ్"),
        'i' => Some("ఐ"),
        'j' => Some("జె"),
        'k' => Some("కె"),
        'l' => Some("ఎల్"),
        'm' => Some("ఎం"),
        'n' => Some("ఎన్"),
        'o' => Some("ఓ"),
        'p' => Some("పి"),
        'q' => Some("క్యూ"),
        'r' => Some("ఆర్"),
        's' => Some("ఎస్"),
        't' => Some("టి"),
        'u' => Some("యు"),
        'v' => Some("వి"),
        'w' => Some("డబ్ల్యూ"),
        'x' => Some("ఎక్స్"),
        'y' => Some("వై"),
        'z' => Some("జెడ్"),
        _ => None,
    }
}

static TELUGU_CHARS: std::sync::LazyLock<[bool; 128]> = std::sync::LazyLock::new(|| {
    let mut chars = [false; 128];
    for c in "ఀఁంఃఄఅఆఇఈఉఊఋఌఎఏఐఒఓఔకఖగఘఙచఛజఝఞటఠడఢణతథదధనపఫబభమయరఱలళఴవశషసహ఼ఽాిీుూృౄెేైొోౌ్ౕౖౘౙౚౝౠౡౢౣ".chars()
    {
        chars[(c as usize) - 0x0c00] = true;
    }
    chars
});

pub fn is_telugu_char(c: char) -> bool {
    let n = c as usize;
    (0x0c00..0x0c80).contains(&n) && TELUGU_CHARS[n - 0x0c00]
}

pub fn is_symbol(c: char) -> Option<&'static str> {
    match c {
        '#' => Some("హ్యాష్"),
        '%' => Some("శాతం"),
        '&' => Some("అండ్"),
        '@' => Some("ఎట్"),
        _ => None,
    }
}

fn spell_latin_letters(s: &str) -> Option<String> {
    let mut out = String::new();
    for (i, c) in s.chars().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        out.push_str(letter_to_te(c)?);
    }
    if out.is_empty() { None } else { Some(out) }
}

fn is_excluded(s: &str) -> bool {
    // 1. Pure Latin word with optional - or /
    if s.chars().all(|c| c.is_ascii_alphabetic() || matches!(c, '-' | '/')) {
        return true;
    }

    // 2. Dimension pattern: digits x digits
    if let Some((d1, d2)) = s.split_once(['x', 'X']) {
        if !d1.is_empty() && !d2.is_empty() && d1.chars().all(is_digit) && d2.chars().all(is_digit) {
            return true;
        }
    }

    // 3. Ordinal pattern: digits + వ
    if let Some(rest) = s.strip_suffix('వ') {
        if !rest.is_empty() && rest.chars().all(is_digit) {
            return true;
        }
    }

    // 4. Date suffix: e.g. "1947 క్రీ.శ."
    for suf in ["క్రీ.శ.", "క్రీ.శ", "క్రీశ", "క్రీ.పూ.", "క్రీ.పూ", "క్రీపూ", "CE", "BCE", "AD", "BC"]
    {
        if let Some(rest) = s.strip_suffix(suf) {
            let trimmed = rest.trim_end();
            if !trimmed.is_empty() && trimmed.chars().all(is_digit) {
                return true;
            }
        }
    }

    // 5. Decade pattern: 1990s, 1990ల, 1990లలో
    for suf in ["s", "ల", "లలో"] {
        if let Some(rest) = s.strip_suffix(suf) {
            if rest.chars().count() == 4 && rest.chars().all(is_digit) {
                let first = numbers::digit_val(rest.chars().next().unwrap()).unwrap_or(0);
                let last = numbers::digit_val(rest.chars().last().unwrap()).unwrap_or(1);
                if (first == 1 || first == 2) && last == 0 {
                    return true;
                }
            }
        }
    }

    // 6. Money glued: currency sym + digits or digits + currency sym
    for row in s
        .chars()
        .next()
        .and_then(|c| EXCLUSIONS.0.get(&c))
        .into_iter()
        .flatten()
        .chain(s.chars().next_back().and_then(|c| EXCLUSIONS.1.get(&c)).into_iter().flatten())
    {
        let sym = row.key.as_str();
        if let Some(rest) = s.strip_prefix(sym) {
            let trimmed = rest.strip_prefix(' ').unwrap_or(rest);
            if !trimmed.is_empty() && trimmed.chars().all(is_digit) {
                return true;
            }
        }
        if let Some(rest) = s.strip_suffix(sym) {
            let trimmed = rest.strip_suffix(' ').unwrap_or(rest);
            if !trimmed.is_empty() && trimmed.chars().all(is_digit) {
                return true;
            }
        }
    }

    // IPv4 uses ASCII digits; date-dot accepts either supported digit set.
    let mut parts = s.split('.');
    if let (Some(a), Some(b), Some(c)) = (parts.next(), parts.next(), parts.next()) {
        let short = |p: &str| (1..=2).contains(&p.chars().count()) && p.chars().all(is_digit);
        if let Some(d) = parts.next() {
            if parts.next().is_none()
                && [a, b, c, d].iter().all(|p| (1..=3).contains(&p.len()) && p.bytes().all(|b| b.is_ascii_digit()))
            {
                return true;
            }
        } else if short(a) && short(b) && c.chars().count() == 4 && c.chars().all(is_digit) {
            return true;
        }
    }

    // 9. Measure pattern: digits + unit
    for row in s.chars().next_back().and_then(|c| EXCLUSIONS.2.get(&c)).into_iter().flatten() {
        let unit_sym = row.key.as_str();
        if let Some(rest) = s.strip_suffix(unit_sym) {
            let trimmed = rest.strip_suffix(' ').unwrap_or(rest);
            let mut range = trimmed.split(['-', '–']);
            let first = range.next().unwrap_or("");
            let valid_first = !first.is_empty() && first.chars().all(is_digit);
            let valid = match range.next() {
                Some(last) => !last.is_empty() && last.chars().all(is_digit) && range.next().is_none(),
                None => true,
            };
            if valid_first && valid {
                return true;
            }
        }
    }

    false
}

fn valid_code_num(s: &str) -> bool {
    let mut chars = s.chars();
    let Some(first) = chars.next() else { return false };
    if !is_digit(first) || !chars.all(is_digit) {
        return false;
    }
    let len = s.chars().count();
    len == 1 || len >= 4 || !numbers::is_zero(first)
}

fn valid_cardinal(s: &str) -> bool {
    let mut chars = s.chars();
    let Some(first) = chars.next() else { return false };
    is_digit(first)
        && chars.all(is_digit)
        && s.chars().count() <= 18
        && (s.chars().count() == 1 || !numbers::is_zero(first))
}

fn valid_segment(s: &str) -> bool {
    valid_code_num(s)
        || (!s.is_empty() && s.chars().all(|c| c.is_ascii_alphabetic()))
        || (s.chars().count() >= 2 && s.chars().all(is_telugu_char))
        || (s.chars().count() == 1 && s.chars().next().and_then(is_symbol).is_some())
}

// Iterate input segments without allocating or rendering during lattice recognition.
fn segments(s: &str, mut visit: impl FnMut(&str, Option<char>)) -> bool {
    let mut start = 0;
    let mut previous: Option<char> = None;
    let mut count = 0;
    for (pos, c) in s.char_indices() {
        if matches!(c, '-' | '/') {
            let segment = &s[start..pos];
            if !valid_segment(segment) {
                return false;
            }
            visit(segment, Some(c));
            count += 1;
            start = pos + c.len_utf8();
            previous = None;
        } else {
            if let Some(prev) = previous {
                let prev_alpha = prev.is_ascii_alphabetic() || is_telugu_char(prev);
                let next_alpha = c.is_ascii_alphabetic() || is_telugu_char(c);
                if (prev_alpha && is_digit(c)) || (is_digit(prev) && next_alpha) {
                    let segment = &s[start..pos];
                    if !valid_segment(segment) {
                        return false;
                    }
                    visit(segment, Some(' '));
                    count += 1;
                    start = pos;
                }
            }
            previous = Some(c);
        }
    }
    let segment = &s[start..];
    if !valid_segment(segment) {
        return false;
    }
    visit(segment, None);
    count >= 1
}

fn spoken_segment(s: &str) -> String {
    if s.chars().all(is_digit) {
        numbers::code_num(s).unwrap()
    } else if s.chars().all(|c| c.is_ascii_alphabetic()) {
        spell_latin_letters(s).unwrap()
    } else if s.chars().count() >= 2 && s.chars().all(is_telugu_char) {
        s.to_owned()
    } else {
        is_symbol(s.chars().next().unwrap()).unwrap().to_owned()
    }
}

fn tag_spoken(spoken: &str) -> String {
    format!("name: \"{}\"", spoken.replace(' ', "\u{a0}"))
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.chars().count() < 2 || s.chars().any(crate::text::is_nemo_space) {
        return;
    }

    let (is_v, ver_s) =
        if let Some(rest) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) { (true, rest) } else { (false, s) };
    let parts = ver_s.split('.');
    let min_parts = if is_v { 2 } else { 3 };
    if parts.clone().count() >= min_parts && parts.clone().all(valid_cardinal) && !is_excluded(s) {
        out.push(Candidate {
            end,
            cost: cost(1.11) - cost(0.05),
            rank: 11,
            tag: lazy::tag(|| {
                let mut spoken = String::new();
                if is_v {
                    spoken.push_str("వి ");
                }
                for (i, part) in parts.enumerate() {
                    if i != 0 {
                        spoken.push_str(" డాట్ ");
                    }
                    spoken.push_str(&numbers::cardinal(part).unwrap());
                }
                tag_spoken(&spoken)
            }),
        });
        return;
    }

    if let Some((base, exp)) = s.split_once('^') {
        let (negative, exp) = if let Some(rest) = exp.strip_prefix('-') { (true, rest) } else { (false, exp) };
        let numeric_base = valid_code_num(base);
        let letter_base = !base.is_empty() && base.chars().all(|c| c.is_ascii_alphabetic());
        let valid_exp = valid_code_num(exp) || (exp.len() == 1 && exp.chars().all(|c| c.is_ascii_alphabetic()));
        if (numeric_base || letter_base) && valid_exp && !is_excluded(s) {
            let square = letter_base && !negative && exp == "2";
            let cube = letter_base && !negative && exp == "3";
            let weight = if numeric_base {
                cost(1.0)
            } else if square || cube {
                -cost(0.01)
            } else {
                0
            };
            out.push(Candidate {
                end,
                cost: cost(1.11) + weight,
                rank: 11,
                tag: lazy::tag(|| {
                    let base_spoken = if numeric_base {
                        numbers::code_num(base).unwrap()
                    } else {
                        spell_latin_letters(base).unwrap()
                    };
                    let spoken = if square {
                        format!("{base_spoken} వర్గం")
                    } else if cube {
                        format!("{base_spoken} ఘనం")
                    } else {
                        let exp_spoken = if valid_code_num(exp) {
                            numbers::code_num(exp).unwrap()
                        } else {
                            spell_latin_letters(exp).unwrap()
                        };
                        let neg = if negative { "ఋణ " } else { "" };
                        if numeric_base {
                            format!("{base_spoken} టు ది పవర్ {neg}{exp_spoken}")
                        } else {
                            format!("{base_spoken} యొక్క {neg}{exp_spoken} ఘాతం")
                        }
                    };
                    tag_spoken(&spoken)
                }),
            });
            return;
        }
    }

    if segments(s, |_, _| {}) && !is_excluded(s) {
        out.push(Candidate {
            end,
            cost: cost(1.11),
            rank: 11,
            tag: lazy::tag(|| {
                let mut spoken = String::new();
                segments(s, |segment, delimiter| {
                    spoken.push_str(&spoken_segment(segment));
                    if let Some(delimiter) = delimiter {
                        spoken.push(delimiter);
                    }
                });
                tag_spoken(&spoken)
            }),
        });
    }
}

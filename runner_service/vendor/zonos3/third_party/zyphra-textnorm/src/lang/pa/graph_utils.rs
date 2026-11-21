//! Punjabi number primitives from `pa/taggers/cardinal.py`.
// Number names are from Unicode CLDR release 49 (Unicode-3.0 license), as in
// the source Punjabi cardinal grammar.

use crate::text::{Cost, cost};
use std::cell::RefCell;
use std::collections::HashMap;
use std::sync::LazyLock;

const DIGITS: [&str; 10] = ["ਸਿਫ਼ਰ", "ਇੱਕ", "ਦੋ", "ਤਿੰਨ", "ਚਾਰ", "ਪੰਜ", "ਛੇ", "ਸੱਤ", "ਅੱਠ", "ਨੌਂ"];
const PAIRS: &str = include_str!("../../../data/pa/numbers/teens_and_ties_en.tsv");

pub(crate) fn digit(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '੦'..='੯' => Some((c as u32 - '੦' as u32) as u8),
        _ => None,
    }
}

pub(crate) fn digitwise(s: &str) -> Option<String> {
    let mut result = String::new();
    for c in s.chars() {
        let n = digit(c)? as usize;
        if !result.is_empty() {
            result.push(' ');
        }
        result.push_str(DIGITS[n]);
    }
    (!result.is_empty()).then_some(result)
}

/// Number graph excluding leading-zero and grouped-number branches. Returns the
/// source graph's accumulated internal weight as well as the spoken value.
pub(crate) fn cardinal(s: &str) -> Option<(String, Cost)> {
    let mut chars = ['\0'; 13];
    let mut len = 0;
    for c in s.chars() {
        if len == chars.len() {
            return None;
        }
        chars[len] = c;
        len += 1;
    }
    cardinal_chars(&chars[..len])
}

fn cardinal_chars(chars: &[char]) -> Option<(String, Cost)> {
    let mut result = String::new();
    let weight = cardinal_into(chars, &mut result)?;
    Some((result, weight))
}

fn cardinal_into(chars: &[char], result: &mut String) -> Option<Cost> {
    let first = digit(*chars.first()?)?;
    if chars.len() == 1 {
        result.push_str(DIGITS[first as usize]);
        return Some(0);
    }
    if first == 0 || chars.len() > 13 {
        return None;
    }
    if chars.len() == 2 {
        // The source's two-digit tables accept either two ASCII or two Punjabi
        // digits, not a mixture.
        if chars[0].is_ascii() != chars[1].is_ascii() {
            return None;
        }
        let n = first * 10 + digit(chars[1])?;
        let word = PAIR_WORDS.get(n as usize).copied().flatten()?;
        result.push_str(word);
        return Some(cost(-0.1));
    }
    let (scale, suffix) = match chars.len() {
        3 => (2, "ਸੌ"),
        4 | 5 => (3, "ਹਜ਼ਾਰ"),
        6 | 7 => (5, "ਲੱਖ"),
        8 | 9 => (7, "ਕਰੋੜ"),
        10 | 11 => (9, "ਅਰਬ"),
        12 | 13 => (11, "ਖਰਬ"),
        _ => return None,
    };
    let split = chars.len() - scale;
    let mut weight = cardinal_into(&chars[..split], result)?;
    let rest = &chars[split..];
    let zeros = rest.iter().take_while(|&&c| digit(c) == Some(0)).count();
    // Every deleted zero before the residual graph carries -0.1; when
    // the entire remainder is zero, all of them are deleted.
    weight += cost(-0.1) * zeros as Cost;
    result.push(' ');
    result.push_str(suffix);
    if zeros < rest.len() {
        result.push(' ');
        let tail_weight = cardinal_into(&rest[zeros..], result)?;
        // Residual two-digit graphs use unweighted teens_ties, unlike the
        // top-level and the two-digit scale prefixes.
        weight += tail_weight;
        if rest.len() - zeros == 2 {
            weight -= cost(-0.1);
        }
    }
    Some(weight)
}

/// Full cardinal number graph, including the two grouped-number conventions
/// and digitwise leading-zero readings.
pub(crate) fn number(s: &str) -> Option<(String, Cost)> {
    if !s.contains(',') {
        if s.chars().count() > 1 && s.chars().next().and_then(digit) == Some(0) {
            return Some((digitwise(s)?, cost(0.5)));
        }
        return cardinal(s);
    }
    number_weight(s)?;
    let mut chars = ['\0'; 13];
    let mut len = 0;
    for c in s.chars().filter(|&c| c != ',') {
        chars[len] = c;
        len += 1;
    }
    cardinal_chars(&chars[..len])
}

// The numeric graph's tropical weight without producing its spoken string.
// At most thirteen non-leading-zero digits enter the cardinal graph.
fn cardinal_weight(chars: &[char]) -> Option<Cost> {
    let first = digit(*chars.first()?)?;
    if chars.len() == 1 {
        return Some(0);
    }
    if first == 0 || chars.len() > 13 {
        return None;
    }
    if chars.len() == 2 {
        if chars[0].is_ascii() != chars[1].is_ascii() {
            return None;
        }
        digit(chars[1])?;
        return Some(cost(-0.1));
    }
    let scale = match chars.len() {
        3 => 2,
        4 | 5 => 3,
        6 | 7 => 5,
        8 | 9 => 7,
        10 | 11 => 9,
        12 | 13 => 11,
        _ => return None,
    };
    let split = chars.len() - scale;
    let mut weight = cardinal_weight(&chars[..split])?;
    let rest = &chars[split..];
    let zeros = rest.iter().take_while(|&&c| digit(c) == Some(0)).count();
    weight += cost(-0.1) * zeros as Cost;
    if zeros < rest.len() {
        let tail = &rest[zeros..];
        weight += cardinal_weight(tail)?;
        if tail.len() == 2 {
            weight -= cost(-0.1);
        }
    }
    Some(weight)
}
pub(crate) fn cardinal_input_weight(s: &str) -> Option<Cost> {
    let mut chars = ['\0'; 13];
    let mut len = 0;
    for c in s.chars() {
        digit(c)?;
        if len == chars.len() {
            return None;
        }
        chars[len] = c;
        len += 1;
    }
    cardinal_weight(&chars[..len])
}

pub(crate) fn number_weight(s: &str) -> Option<Cost> {
    if !s.contains(',') && s.chars().next().and_then(digit) == Some(0) {
        let mut chars = s.chars();
        chars.next()?;
        let mut more = false;
        for c in chars {
            digit(c)?;
            more = true;
        }
        return Some(if more { cost(0.5) } else { 0 });
    }
    let mut digits = ['\0'; 13];
    let mut len = 0;
    let mut groups = 0;
    let mut group_len = 0;
    let mut indian = true;
    let mut western = true;
    for c in s.chars() {
        if c == ',' {
            if group_len == 0 {
                return None;
            }
            if groups == 0 {
                indian &= group_len <= 2;
                western &= group_len <= 3;
            } else {
                indian &= group_len == 2;
                western &= group_len == 3;
            }
            groups += 1;
            group_len = 0;
        } else {
            digit(c)?;
            if len == digits.len() {
                return None;
            }
            digits[len] = c;
            len += 1;
            group_len += 1;
        }
    }
    if groups > 0 && (group_len != 3 || !(indian || western)) {
        return None;
    }
    cardinal_weight(&digits[..len])
}

/// Static lookup indexes preserve the first row for duplicate keys, as the
/// previous linear scan did. All tables are embedded and never mutated.
static TABLES: LazyLock<HashMap<(usize, usize), HashMap<&'static str, &'static str>>> = LazyLock::new(|| {
    let tables = [
        include_str!("../../../data/pa/whitelist/abbreviations.tsv"),
        include_str!("../../../data/pa/roman/roman_to_spoken.tsv"),
        include_str!("../../../data/pa/roman/roman_ordinal_exceptions.tsv"),
        include_str!("../../../data/pa/ordinal/suffixes.tsv"),
        include_str!("../../../data/pa/ordinal/exceptions.tsv"),
        include_str!("../../../data/pa/address/letters.tsv"),
        include_str!("../../../data/pa/serial/power_special.tsv"),
        include_str!("../../../data/pa/serial/special_symbols.tsv"),
        include_str!("../../../data/pa/serial/chars.tsv"),
        include_str!("../../../data/pa/electronic/domain.tsv"),
        include_str!("../../../data/pa/electronic/file_extensions.tsv"),
        include_str!("../../../data/pa/electronic/symbols.tsv"),
        include_str!("../../../data/pa/electronic/subscript_digit.tsv"),
        include_str!("../../../data/pa/electronic/protocols.tsv"),
        include_str!("../../../data/pa/date/days.tsv"),
        include_str!("../../../data/pa/date/day_ordinal.tsv"),
        include_str!("../../../data/pa/date/months.tsv"),
        include_str!("../../../data/pa/date/month_names.tsv"),
        include_str!("../../../data/pa/date/month_abbr.tsv"),
        include_str!("../../../data/pa/time/hours.tsv"),
        include_str!("../../../data/pa/time/minutes.tsv"),
        include_str!("../../../data/pa/time/seconds.tsv"),
        include_str!("../../../data/pa/money/major_minor_currencies.tsv"),
        include_str!("../../../data/pa/fraction/common_fractions.tsv"),
        include_str!("../../../data/pa/fraction/next_number.tsv"),
    ];
    let mut result = HashMap::new();
    for table in tables {
        let mut index = HashMap::new();
        for row in table.lines() {
            let row = row.trim_end_matches('\r');
            let (key, value) = row.split_once('\t').unwrap_or((row, row));
            index.entry(key).or_insert_with(|| value.split('\t').next().unwrap_or(value));
        }
        result.insert((table.as_ptr() as usize, table.len()), index);
    }
    result
});

static PAIR_WORDS: LazyLock<[Option<&'static str>; 100]> = LazyLock::new(|| {
    let mut words = [None; 100];
    for row in PAIRS.lines() {
        if let Some((key, value)) = row.split_once('\t') {
            if let Ok(n) = key.parse::<usize>() {
                if n < words.len() {
                    words[n] = Some(value.trim_end_matches('\r'));
                }
            }
        }
    }
    words
});

thread_local! {
    // Separate include_str! expansions need not have the same address. Cache
    // any such static table under its own address rather than rescanning it.
    static EXTRA_TABLES: RefCell<HashMap<(usize, usize), HashMap<&'static str, &'static str>>> =
        RefCell::new(HashMap::new());
}

pub(crate) fn lookup(table: &'static str, key: &str) -> Option<&'static str> {
    let id = (table.as_ptr() as usize, table.len());
    if let Some(index) = TABLES.get(&id) {
        return index.get(key).copied();
    }
    EXTRA_TABLES.with_borrow_mut(|tables| {
        let index = tables.entry(id).or_insert_with(|| {
            let mut rows = HashMap::new();
            for row in table.lines() {
                let row = row.trim_end_matches('\r');
                let (k, v) = row.split_once('\t').unwrap_or((row, row));
                rows.entry(k).or_insert_with(|| v.split('\t').next().unwrap_or(v));
            }
            rows
        });
        index.get(key).copied()
    })
}

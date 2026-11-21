//! Currency major, minor, magnitude, rate and range branches.
use super::{
    decimal,
    graph_utils::{append_positive, is_positive, positive},
};
use std::sync::LazyLock;
const PRE: &str = include_str!("../../../data/tr/currencies_prefix.tsv");
const POST: &str = include_str!("../../../data/tr/currencies_suffix.tsv");
const EXTRA: &str = include_str!("../../../data/tr/extra_currencies.tsv");
type Currency = (&'static str, &'static str, &'static str);
static PREFIX: LazyLock<Vec<Currency>> = LazyLock::new(|| load_currencies(PRE));
static SUFFIX: LazyLock<Vec<Currency>> = LazyLock::new(|| load_currencies(POST));
static PREFIX_INDEX: LazyLock<[Vec<Currency>; 256]> = LazyLock::new(|| make_index(&PREFIX, true));
static SUFFIX_INDEX: LazyLock<[Vec<Currency>; 256]> = LazyLock::new(|| make_index(&SUFFIX, false));
static SUFFIX_WORDS: LazyLock<[Vec<&'static str>; 256]> = LazyLock::new(|| {
    let mut words: [Vec<&'static str>; 256] = std::array::from_fn(|_| Vec::new());
    for &(symbol, _, _) in SUFFIX.iter() {
        let end = symbol.char_indices().find(|(_, c)| !c.is_alphabetic()).map_or(symbol.len(), |(i, _)| i);
        if end != 0 {
            let word = &symbol[..end];
            words[word.as_bytes()[0] as usize].push(word);
        }
    }
    words
});
/// Alphabetic runs in a digit-led amount, before an optional rate slash,
/// can only be the alphabetic part of a trailing currency symbol.
pub fn numeric_word(word: &str, complete: bool) -> bool {
    let Some(&first) = word.as_bytes().first() else { return false };
    SUFFIX_WORDS[first as usize].iter().any(|item| if complete { *item == word } else { item.starts_with(word) })
}
fn make_index(rows: &[Currency], first: bool) -> [Vec<Currency>; 256] {
    let mut index: [Vec<Currency>; 256] = std::array::from_fn(|_| Vec::new());
    for &row in rows {
        let bytes = row.0.as_bytes();
        let key = if first { bytes.first() } else { bytes.last() };
        if let Some(&key) = key {
            index[key as usize].push(row);
        }
    }
    index
}
fn prefixes(s: &str) -> &'static [Currency] {
    s.as_bytes().first().map_or(&[], |&key| PREFIX_INDEX[key as usize].as_slice())
}
fn suffixes(s: &str) -> &'static [Currency] {
    s.as_bytes().last().map_or(&[], |&key| SUFFIX_INDEX[key as usize].as_slice())
}
/// Every currency branch has a leading symbol, a trailing symbol, or a rate
/// whose numerator has a trailing symbol. Tables determine the symbols.
pub fn possible(s: &str) -> bool {
    let unsigned = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(s);
    if prefixes(unsigned).iter().any(|(symbol, _, _)| unsigned.starts_with(*symbol))
        || ["US $", "USD $"].iter().any(|prefix| unsigned.starts_with(*prefix))
        || suffixes(unsigned).iter().any(|(symbol, _, _)| unsigned.ends_with(*symbol))
    {
        return true;
    }
    if let Some((amount, _)) = unsigned.split_once('/') {
        let amount = amount.strip_suffix(' ').unwrap_or(amount);
        return suffixes(amount).iter().any(|(symbol, _, _)| amount.ends_with(*symbol));
    }
    false
}
static PREFIX_INITIALS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars = Vec::new();
    for (symbol, _, _) in currencies(true) {
        if let Some(c) = symbol.chars().next() {
            if !chars.contains(&c) {
                chars.push(c);
            }
        }
    }
    chars
});
pub fn can_start(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '-' | '−') || PREFIX_INITIALS.contains(&c)
}
fn currencies(prefix: bool) -> &'static [Currency] {
    if prefix { &PREFIX } else { &SUFFIX }
}
fn load_currencies(base: &'static str) -> Vec<Currency> {
    let mut rows = Vec::new();
    for line in base.lines().chain(EXTRA.lines()) {
        let mut p = line.split('\t');
        if let (Some(a), Some(b), Some(c)) = (p.next(), p.next(), p.next()) {
            if !rows.iter().any(|(key, _, _)| *key == a) {
                rows.push((a, b, c));
            }
        }
    }
    rows
}
fn amount_count(s: &str) -> usize {
    let mut total = usize::from(is_positive(s));
    for (i, c) in s.char_indices() {
        if !matches!(c, ',' | '.') {
            continue;
        }
        let a = &s[..i];
        let b = &s[i + 1..];
        if b.len() != 2 || !b.bytes().all(|v| v.is_ascii_digit()) {
            continue;
        }
        if b == "00" {
            total += usize::from(is_positive(a));
        } else {
            let minor = b.strip_prefix('0').unwrap_or(b);
            if is_positive(minor) && (a == "0" || a == "00" || is_positive(a)) {
                total += 1;
            }
        }
    }
    total
}
fn basic_count(input: &str) -> usize {
    let negative = input.starts_with('-') || input.starts_with('−');
    let s = input.strip_prefix('-').or_else(|| input.strip_prefix('−')).unwrap_or(input);
    let mut total = 0;
    for &(sym, _, _) in prefixes(s) {
        if let Some(rest) = s.strip_prefix(sym) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            total += amount_count(rest);
            if !negative {
                for mag in ["k", "K", " bin", "M", "m", " milyon", "B", "b", " milyar"] {
                    if let Some(n) = rest.strip_suffix(mag) {
                        for n in [Some(n), n.strip_suffix(' ')].into_iter().flatten() {
                            total += usize::from(is_positive(n)) + decimal::count(n);
                        }
                    }
                }
            }
        }
    }
    for &(sym, _, _) in suffixes(s) {
        if let Some(rest) = s.strip_suffix(sym) {
            total += amount_count(rest.strip_suffix(' ').unwrap_or(rest));
        }
    }
    for prefix in ["US $", "US$", "USD$", "USD $"] {
        if let Some(n) = input.strip_prefix(prefix) {
            total += decimal::count(n);
        }
    }
    total
}
/// Count each currency reading and its repeated/range alternatives.
pub fn count(s: &str) -> usize {
    let mut total = basic_count(s);
    if let Some((money, unit)) = s.split_once('/') {
        let money = money.strip_suffix(' ').unwrap_or(money);
        let unit = unit.strip_prefix(' ').unwrap_or(unit);
        if ["h", "saat", "kg", "kilo", "gün", "hafta", "ay", "yıl", "adet", "tane", "kişi"].contains(&unit) {
            total += basic_count(money);
        }
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–' | '—') {
            continue;
        }
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let b = &s[i + c.len_utf8()..];
        let b = b.strip_prefix(' ').unwrap_or(b);
        total += basic_count(a) * basic_count(b);
        for &(sym, _, _) in suffixes(b) {
            if let Some(n) = b.strip_suffix(sym) {
                let n = n.strip_suffix(' ').unwrap_or(n);
                if is_positive(a) && is_positive(n) {
                    total += 1;
                }
            }
        }
    }
    total
}
/// Write the first plain major-unit path directly into its final tag.
pub fn simple_tag(input: &str) -> Option<String> {
    let (sign, s) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(rest) => (true, rest),
        None => (false, input),
    };
    let mut found = None;
    for &(symbol, major, _) in prefixes(s) {
        if let Some(rest) = s.strip_prefix(symbol) {
            found = Some((rest.strip_prefix(' ').unwrap_or(rest), major));
            break;
        }
    }
    if found.is_none() {
        for &(symbol, major, _) in suffixes(s) {
            if let Some(rest) = s.strip_suffix(symbol) {
                found = Some((rest.strip_suffix(' ').unwrap_or(rest), major));
                break;
            }
        }
    }
    let (number, major) = found?;
    let mut tag = String::with_capacity(input.len() * 4 + major.len() + 16);
    tag.push_str("name: \"");
    if sign {
        tag.push_str("eksi\u{a0}");
    }
    append_positive(&mut tag, number, '\u{a0}')?;
    tag.push('\u{a0}');
    for c in major.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    Some(tag)
}
fn amount(s: &str, major: &str, minor: &str, sign: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(n) = positive(s) {
        out.push(format!("{sign}{n} {major}"));
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, ',' | '.') {
            continue;
        }
        let a = &s[..i];
        let b = &s[i + 1..];
        if b.len() != 2 || !b.bytes().all(|v| v.is_ascii_digit()) {
            continue;
        }
        if b == "00" {
            if let Some(n) = positive(a) {
                out.push(format!("{sign}{n} {major}"));
            }
            continue;
        }
        let b = b.strip_prefix('0').unwrap_or(b);
        let Some(m) = positive(b) else { continue };
        if a == "0" || a == "00" {
            out.push(format!("{sign}{m} {minor}"));
        } else if let Some(n) = positive(a) {
            out.push(format!("{sign}{n} {major} {m} {minor}"));
        }
    }
    out
}
fn basic(input: &str) -> Vec<String> {
    let (sign, s) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(s) => ("eksi ", s),
        None => ("", input),
    };
    let mut out = Vec::new();
    for &(sym, maj, min) in prefixes(s) {
        if let Some(rest) = s.strip_prefix(sym) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            out.extend(amount(rest, maj, min, sign));
            if sign.is_empty() {
                for (mag, spoken) in [
                    ("k", "bin"),
                    ("K", "bin"),
                    (" bin", "bin"),
                    ("M", "milyon"),
                    ("m", "milyon"),
                    (" milyon", "milyon"),
                    ("B", "milyar"),
                    ("b", "milyar"),
                    (" milyar", "milyar"),
                ] {
                    if let Some(n) = rest.strip_suffix(mag) {
                        for n in [Some(n), n.strip_suffix(' ')].into_iter().flatten() {
                            if let Some(v) = positive(n) {
                                out.push(format!("{v} {spoken} {maj}"));
                            }
                            for v in decimal::readings(n) {
                                out.push(format!("{v} {spoken} {maj}"));
                            }
                        }
                    }
                }
            }
        }
    }
    for &(sym, maj, min) in suffixes(s) {
        if let Some(rest) = s.strip_suffix(sym) {
            let rest = rest.strip_suffix(' ').unwrap_or(rest);
            out.extend(amount(rest, maj, min, sign));
        }
    }
    for (prefix, spoken) in [("US $", "US "), ("US$", "US "), ("USD$", ""), ("USD $", "")] {
        if let Some(n) = input.strip_prefix(prefix) {
            for v in decimal::readings(n) {
                out.push(format!("{spoken}{v} dolar"));
            }
        }
    }
    out
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = basic(s);
    if let Some((money, unit)) = s.split_once('/') {
        let money = money.strip_suffix(' ').unwrap_or(money);
        let unit = unit.strip_prefix(' ').unwrap_or(unit);
        for (u, v) in [
            ("h", "saat"),
            ("saat", "saat"),
            ("kg", "kilogram"),
            ("kilo", "kilogram"),
            ("gün", "gün"),
            ("hafta", "hafta"),
            ("ay", "ay"),
            ("yıl", "yıl"),
            ("adet", "adet"),
            ("tane", "tane"),
            ("kişi", "kişi"),
        ] {
            if unit == u {
                for m in basic(money) {
                    out.push(format!("{m} bölü {v}"));
                }
            }
        }
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–' | '—') {
            continue;
        }
        let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
        let b = &s[i + c.len_utf8()..];
        let b = b.strip_prefix(' ').unwrap_or(b);
        for x in basic(a) {
            for y in basic(b) {
                out.push(format!("{x} ila {y}"));
            }
        }
        for &(sym, maj, _) in suffixes(b) {
            if let Some(n) = b.strip_suffix(sym) {
                let n = n.strip_suffix(' ').unwrap_or(n);
                if let (Some(x), Some(y)) = (positive(a), positive(n)) {
                    out.push(format!("{x} ila {y} {maj}"));
                }
            }
        }
    }
    out
}

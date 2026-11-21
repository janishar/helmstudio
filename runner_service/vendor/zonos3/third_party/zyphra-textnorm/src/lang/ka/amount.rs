use super::{
    graph_utils::{all_number, cardinal, glue_suffix, number_into},
    lookup,
    numeric::{
        all_number_cost, decimal_body, decimal_body_weight, decimal_cost, range_cost, range_weight, split_suffix,
    },
};
use std::collections::HashSet;
use std::sync::LazyLock;
const UNITS: &str = include_str!("../../../data/ka/measurements.tsv");
const LATIN: &str = include_str!("../../../data/ka/measurements_latin.tsv");
const DATES: &str = include_str!("../../../data/ka/measurement_dates.tsv");
const CURRENCY: &str = include_str!("../../../data/ka/currency.tsv");
const MINOR: &str = include_str!("../../../data/ka/currency_minor.tsv");
type Labels = Vec<(&'static str, &'static str)>;
static CURRENCY_ROWS: LazyLock<Labels> =
    LazyLock::new(|| CURRENCY.lines().filter_map(|line| line.split_once('\t')).collect());
static MINOR_ROWS: LazyLock<Labels> =
    LazyLock::new(|| MINOR.lines().filter_map(|line| line.split_once('\t')).collect());
struct CurrencyIndex {
    major: HashSet<&'static str>,
    minor: HashSet<&'static str>,
    starts: HashSet<char>,
    ends: HashSet<char>,
    max_bytes: usize,
}
static CURRENCY_INDEX: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let major: HashSet<_> = CURRENCY_ROWS.iter().map(|(key, _)| *key).collect();
    let minor: HashSet<_> = MINOR_ROWS.iter().map(|(key, _)| *key).collect();
    let max_bytes = major.iter().chain(&minor).map(|key| key.len()).max().unwrap_or(0);
    let starts = major.iter().filter_map(|key| key.chars().next()).collect();
    let ends = major.iter().chain(&minor).filter_map(|key| key.chars().last()).collect();
    CurrencyIndex { major, minor, starts, ends, max_bytes }
});
static MAX_UNIT_BYTES: LazyLock<usize> = LazyLock::new(|| {
    [UNITS, LATIN, DATES]
        .into_iter()
        .flat_map(|table| table.lines().filter_map(|line| line.split_once('\t').map(|(key, _)| key.len())))
        .max()
        .unwrap_or(0)
});

fn units(s: &str, dates: bool) -> Vec<String> {
    let (base, suf) = split_suffix(s);
    let folded;
    let lower = if base.bytes().any(|b| b.is_ascii_uppercase()) {
        folded = base.to_ascii_lowercase();
        folded.as_str()
    } else {
        base
    };
    // The date and measurement graphs are a union, not a priority lookup:
    // for example W is both a case-folded week and the Latin watt symbol.
    let mut out = Vec::new();
    for v in [if dates { lookup(DATES, lower) } else { None }, lookup(UNITS, lower), lookup(LATIN, base)]
        .into_iter()
        .flatten()
    {
        let value = suf.map_or(v.to_owned(), |x| glue_suffix(v, x));
        if !out.contains(&value) {
            out.push(value);
        }
    }
    out
}

fn accepted_units(s: &str) -> (bool, bool) {
    let (base, _) = split_suffix(s);
    if base.len() > *MAX_UNIT_BYTES {
        return (false, false);
    }
    let folded;
    let lower = if base.bytes().any(|b| b.is_ascii_uppercase()) {
        folded = base.to_ascii_lowercase();
        folded.as_str()
    } else {
        base
    };
    let ordinary = lookup(UNITS, lower).is_some() || lookup(LATIN, base).is_some();
    let date = ordinary || lookup(DATES, lower).is_some();
    (date, ordinary)
}

/// Recognize the measure branches and their weights without spelling numbers.
pub fn measure_costs(s: &str, mut push: impl FnMut(usize, bool, f64)) {
    for (i, _) in s.char_indices().skip(1) {
        let n = s[..i].trim_end_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
        let (_, unsigned) = sign(n);
        let cardinal = all_number_cost(unsigned).or_else(|| range_cost(unsigned));
        let decimal = decimal_cost(n);
        if cardinal.is_none() && decimal.is_none() {
            continue;
        }
        let (date_unit, ordinary_unit) = accepted_units(&s[i..]);
        if date_unit {
            if let Some(w) = cardinal {
                push(i, false, w);
            }
        }
        if ordinary_unit {
            if let Some(w) = decimal {
                push(i, true, w);
            }
        }
    }
}
fn sign(s: &str) -> (&str, &str) {
    if let Some(s) = s.strip_prefix(['-', '−']) { ("negative: \"true\" ", s) } else { ("", s) }
}
fn num_body(s: &str) -> Option<(String, f64)> {
    let (neg, rest) = sign(s);
    let (w, cost) = all_number(rest).or_else(|| range_weight(rest))?;
    Some((format!("{neg}integer: \"{w}\""), cost))
}

/// Render the exact split recorded during recognition, without rescanning
/// the input or constructing readings for competing units.
pub fn measure_at(s: &str, split: usize, decimal: bool) -> Option<String> {
    let n = s.get(..split)?.trim_end_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
    let (base, suffix) = split_suffix(s.get(split..)?);
    let folded;
    let lower = if base.bytes().any(|b| b.is_ascii_uppercase()) {
        folded = base.to_ascii_lowercase();
        folded.as_str()
    } else {
        base
    };
    let unit = if decimal { None } else { lookup(DATES, lower) }
        .or_else(|| lookup(UNITS, lower))
        .or_else(|| lookup(LATIN, base))?;
    let mut tag = String::with_capacity(96 + n.len() * 3 + unit.len());
    if decimal {
        let (number, _) = decimal_body_weight(n)?;
        tag.push_str("measure { decimal { ");
        tag.push_str(&number);
    } else {
        let (negative, unsigned) = sign(n);
        tag.push_str("measure { cardinal { ");
        tag.push_str(negative);
        tag.push_str("integer: \"");
        if all_number_cost(unsigned).is_some() {
            number_into(unsigned, &mut tag)?;
        } else {
            tag.push_str(&range_weight(unsigned)?.0);
        }
        tag.push('"');
    }
    tag.push_str(" } units: \"");
    if let Some(suffix) = suffix {
        tag.push_str(&glue_suffix(unit, suffix));
    } else {
        tag.push_str(unit);
    }
    tag.push_str("\" }");
    Some(tag)
}

pub fn measures(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (i, _) in s.char_indices().skip(1) {
        let n = s[..i].trim_end_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
        let u = &s[i..];
        let ordinary = units(u, false);
        let date = units(u, true);
        if !date.is_empty() {
            if let Some((n, w)) = num_body(n) {
                for u in date {
                    out.push((format!("measure {{ cardinal {{ {n} }} units: \"{u}\" }}"), w));
                }
            }
        }
        if !ordinary.is_empty() {
            if let Some((n, w)) = decimal_body_weight(n) {
                for u in ordinary {
                    out.push((format!("measure {{ decimal {{ {n} }} units: \"{u}\" }}"), w));
                }
            }
        }
    }
    out
}
fn amount(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let trimmed = s.strip_suffix(",00").or_else(|| s.strip_suffix(".00")).unwrap_or(s);
    if let Some((n, w)) = all_number(trimmed) {
        out.push((format!("integer_part: \"{n}\""), w));
    }
    if let Some((n, w)) = range_weight(trimmed) {
        out.push((format!("integer_part: \"{n}\""), w));
    }
    if let Some((body, w)) = decimal_body_weight(s) {
        out.push((body, w));
    }
    for (suffix, qty) in [("k", "ათასი"), ("K", "ათასი"), ("M", "მილიონი"), ("B", "მილიარდი")]
    {
        if let Some(raw) = s.strip_suffix(suffix) {
            let raw = raw.strip_suffix(' ').unwrap_or(raw);
            if suffix == "k" || suffix == "K" {
                if raw.len() <= 6 {
                    if let Some(n) = cardinal(raw) {
                        let w = if raw == "1" { qty.to_owned() } else { format!("{n} {qty}") };
                        out.push((format!("integer_part: \"{w}\""), 0.0));
                    }
                }
            } else if let Some((a, b)) = raw.split_once(['.', ',']) {
                if a.len() <= 6 {
                    if let Some(body) = decimal_body(&format!("{a}.{b}")) {
                        out.push((format!("{body} quantity: \"{qty}\""), 0.0));
                    }
                }
            } else if raw.len() <= 6 {
                if let Some(n) = cardinal(raw) {
                    out.push((format!("integer_part: \"{n}\" quantity: \"{qty}\""), 0.0));
                }
            }
        }
    }
    out
}
fn per(s: &str) -> (&str, Option<&str>) {
    if !s.contains('/') {
        return (s, None);
    }
    for (k, v) in [
        ("h", "საათში"),
        ("hr", "საათში"),
        ("საათი", "საათში"),
        ("სთ", "საათში"),
        ("kg", "კილოგრამზე"),
        ("კგ", "კილოგრამზე"),
        ("m", "მეტრზე"),
        ("მ", "მეტრზე"),
        ("km", "კილომეტრზე"),
        ("კმ", "კილომეტრზე"),
        ("month", "თვეში"),
        ("თვე", "თვეში"),
        ("year", "წელიწადში"),
        ("წელი", "წელიწადში"),
        ("d", "დღეში"),
        ("დღე", "დღეში"),
    ] {
        if let Some(raw) = s.strip_suffix(k).and_then(|x| x.strip_suffix('/')) {
            return (raw.strip_suffix(' ').unwrap_or(raw), Some(v));
        }
    }
    (s, None)
}

pub fn may_be_money(s: &str) -> bool {
    let (_, raw) = sign(s);
    let (raw, _) = per(raw);
    let (base, _) = split_suffix(raw);
    let index = &*CURRENCY_INDEX;
    if raw.chars().next().is_some_and(|c| index.starts.contains(&c)) {
        for (i, c) in raw.char_indices() {
            let end = i + c.len_utf8();
            if end > index.max_bytes {
                break;
            }
            if index.major.contains(&raw[..end]) {
                return true;
            }
        }
    }
    if base.chars().last().is_some_and(|c| index.ends.contains(&c)) {
        for (i, _) in base.char_indices().rev() {
            if base.len() - i > index.max_bytes {
                break;
            }
            let key = &base[i..];
            if index.major.contains(key) || index.minor.contains(key) {
                return true;
            }
        }
    }
    false
}

pub fn money(s: &str) -> Vec<(String, f64)> {
    let (negative, raw) = sign(s);
    let (raw, per) = per(raw);
    let per = per.map(|p| format!(" morphosyntactic_features: \"{p}\"")).unwrap_or_default();
    let mut out = Vec::new();
    if per.is_empty() {
        let (base, suffix) = split_suffix(raw);
        for &(sym, min) in MINOR_ROWS.iter() {
            if let Some(a) = base.strip_suffix(sym) {
                if a.len() <= 2 {
                    if let Some(v) = cardinal(a) {
                        let unit = suffix.map_or(min.to_owned(), |s| glue_suffix(min, s));
                        out.push((
                            format!("money {{ {negative}fractional_part: \"{v}\" currency_min: \"{unit}\" }}"),
                            0.0,
                        ));
                    }
                }
            }
        }
    }
    let (base, suffix) = split_suffix(raw);
    for &(sym, curr) in CURRENCY_ROWS.iter() {
        for pre in [true, false] {
            let (raw_amt, curr_word) = if pre {
                if let Some(a) = raw.strip_prefix(sym) {
                    (a.strip_prefix(' ').unwrap_or(a), curr.to_owned())
                } else {
                    continue;
                }
            } else {
                if let Some(a) = base.strip_suffix(sym) {
                    (a.strip_suffix(' ').unwrap_or(a), suffix.map_or(curr.to_owned(), |s| glue_suffix(curr, s)))
                } else {
                    continue;
                }
            };
            for (body, w) in amount(raw_amt) {
                let fields = if pre {
                    format!("currency: \"{curr_word}\" {body}")
                } else {
                    format!("{body} currency: \"{curr_word}\"")
                };
                out.push((format!("money {{ {negative}{fields}{per} }}"), w));
            }
            if let Some(min) = lookup(MINOR, sym) {
                if let Some((a, b)) = raw_amt.rsplit_once(['.', ',']) {
                    if b.len() == 2 {
                        if let (Some((major, w)), Some(minor)) = (all_number(a), cardinal(b)) {
                            if a == "0" {
                                out.push((format!("money {{ {negative}fractional_part: \"{minor}\" currency_min: \"{min}\"{per} }}"),-0.05));
                            }
                            let body = format!(
                                "integer_part: \"{major}\" currency_maj: \"{curr}\" fractional_part: \"{minor}\" currency_min: \"{min}\""
                            );
                            out.push((format!("money {{ {negative}{body}{per} }}"), -0.05 + w));
                        }
                    }
                }
            }
        }
    }
    out
}

/// The top-level union's range of two complete money expressions.
pub fn money_range(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—' | '֊')) {
        let left = s[..i].trim_end_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
        let right =
            s[i + c.len_utf8()..].trim_start_matches(|c: char| matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}'));
        for (a, wa) in money(left) {
            for (b, wb) in money(right) {
                fn attach(tag: &str, suffix: &str) -> String {
                    let Some(i) = tag.find("currency: \"") else { return tag.to_owned() };
                    let start = i + "currency: \"".len();
                    let Some(end) = tag[start..].find('"').map(|x| x + start) else { return tag.to_owned() };
                    format!("{}{}{}", &tag[..start], glue_suffix(&tag[start..end], suffix), &tag[end..])
                }
                out.push((format!("{} }} tokens {{ {}", attach(&a, "დან"), attach(&b, "მდე")), wa + wb));
            }
        }
    }
    out
}

//! Money classifier for Hindi.
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

const CURRENCY_DATA: &str = include_str!("../../../data/hi/money/currency.tsv");
const CURRENCY_SG_DATA: &str = include_str!("../../../data/hi/money/currency_singular.tsv");
const MAJOR_MINOR_DATA: &str = include_str!("../../../data/hi/money/major_minor_currencies.tsv");
const QUANTITIES_DATA: &str = include_str!("../../../data/hi/numbers/thousands.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static CURRENCY_SG_MAP: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(CURRENCY_SG_DATA).collect());
static MAJOR_MINOR_MAP: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(MAJOR_MINOR_DATA).collect());
static QUANTITIES_SET: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    let mut v: Vec<&'static str> = rows(QUANTITIES_DATA).map(|(_, val)| val).collect();
    v.sort_unstable();
    v.dedup();
    v
});

const ISO_PLURAL: &[(&str, &str)] = &[
    ("INR", "रुपए"),
    ("Rs", "रुपए"),
    ("Rs.", "रुपए"),
    ("USD", "अमेरिकी डॉलर"),
    ("EUR", "यूरो"),
    ("GBP", "ब्रिटिश पाउंड"),
    ("JPY", "जापानी येन"),
    ("CNY", "चीनी युआन"),
    ("AUD", "ऑस्ट्रेलियाई डॉलर"),
    ("CAD", "कनाडाई डॉलर"),
    ("SGD", "सिंगापुर डॉलर"),
    ("CHF", "स्विस फ़्रैंक"),
    ("RUB", "रूसी रूबल"),
    ("AED", "दिरहम"),
    ("SAR", "रियाल"),
    ("KRW", "वॉन"),
    ("TRY", "लीरा"),
    ("BDT", "टका"),
    ("NGN", "नाइरा"),
    ("PKR", "पाकिस्तानी रुपए"),
    ("NZD", "न्यूज़ीलैंड डॉलर"),
    ("HKD", "हांगकांग डॉलर"),
    ("TWD", "नया ताइवान डॉलर"),
    ("BRL", "ब्राज़ीलियाई रियाल"),
    ("MXN", "मेक्सिकन पेसो"),
    ("ILS", "शेकेल"),
    ("UAH", "ह्रिव्निया"),
    ("VND", "डोंग"),
    ("PHP", "पेसो"),
    ("THB", "बात"),
    ("AMD", "द्राम"),
    ("GHS", "सेडी"),
];

const ISO_SINGULAR: &[(&str, &str)] = &[
    ("INR", "रुपया"),
    ("Rs", "रुपया"),
    ("Rs.", "रुपया"),
    ("USD", "अमेरिकी डॉलर"),
    ("EUR", "यूरो"),
    ("GBP", "ब्रिटिश पाउंड"),
    ("JPY", "जापानी येन"),
    ("CNY", "चीनी युआन"),
    ("AUD", "ऑस्ट्रेलियाई डॉलर"),
    ("CAD", "कनाडाई डॉलर"),
    ("SGD", "सिंगापुर डॉलर"),
    ("CHF", "स्विस फ़्रैंक"),
    ("RUB", "रूसी रूबल"),
    ("AED", "दिरहम"),
    ("SAR", "रियाल"),
    ("KRW", "वॉन"),
    ("TRY", "लीरा"),
    ("BDT", "टका"),
    ("NGN", "नाइरा"),
    ("PKR", "पाकिस्तानी रुपया"),
    ("NZD", "न्यूज़ीलैंड डॉलर"),
    ("HKD", "हांगकांग डॉलर"),
    ("TWD", "नया ताइवान डॉलर"),
    ("BRL", "ब्राज़ीलियाई रियाल"),
    ("MXN", "मेक्सिकन पेसो"),
    ("ILS", "शेकेल"),
    ("UAH", "ह्रिव्निया"),
    ("VND", "डोंग"),
    ("PHP", "पेसो"),
    ("THB", "बात"),
    ("AMD", "द्राम"),
    ("GHS", "सेडी"),
];

const PER_UNIT: &[(&str, &str)] = &[
    ("/h", "प्रति घंटा"),
    ("/hr", "प्रति घंटा"),
    ("/kg", "प्रति किलोग्राम"),
    ("/g", "प्रति ग्राम"),
    ("/km", "प्रति किलोमीटर"),
    ("/m", "प्रति मीटर"),
    ("/L", "प्रति लीटर"),
    ("/l", "प्रति लीटर"),
    ("/min", "प्रति मिनट"),
];

const SCALES: &[(&str, &str)] =
    &[("k", "हज़ार"), ("K", "हज़ार"), ("m", "मिलियन"), ("M", "मिलियन"), ("b", "अरब"), ("B", "अरब")];
static CURRENCY_TRIE: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    super::trie::Trie::from_keys(
        rows(CURRENCY_DATA).map(|(symbol, _)| symbol).chain(ISO_PLURAL.iter().map(|&(symbol, _)| symbol)),
    )
});
static CURRENCY_ENDS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for symbol in rows(CURRENCY_DATA).map(|(symbol, _)| symbol).chain(ISO_PLURAL.iter().map(|&(symbol, _)| symbol)) {
        if let Some(last) = symbol.chars().next_back() {
            index.entry(last).or_default().push(symbol);
        }
    }
    index
});
fn currency_at_edge(s: &str) -> bool {
    let mut prefix = false;
    CURRENCY_TRIE.matching_prefixes(s, |_| prefix = true);
    prefix
        || s.chars()
            .next_back()
            .and_then(|c| CURRENCY_ENDS.get(&c))
            .is_some_and(|keys| keys.iter().any(|key| s.ends_with(*key)))
}
fn currency_prefix_or_complete(s: &str) -> bool {
    let mut valid = CURRENCY_TRIE.has_prefix(s);
    CURRENCY_TRIE.matching_prefixes(s, |len| {
        let tail = &s[len..];
        let range = tail.strip_prefix(' ').unwrap_or(tail);
        if tail.is_empty()
            || tail == " "
            || PER_UNIT.iter().any(|(suffix, _)| suffix.starts_with(tail))
            || range.starts_with(['-', '–', '—'])
        {
            valid = true;
        }
    });
    valid
}
pub(crate) fn numeric_prefix(s: &str) -> bool {
    let Some((i, _)) = s
        .char_indices()
        .find(|&(_, c)| !super::numbers::is_digit(c) && !matches!(c, ' ' | ',' | '.' | '-' | '–' | '—'))
    else {
        return true;
    };
    let rest = &s[i..];
    if currency_prefix_or_complete(rest) {
        return true;
    }
    if let Some(after_scale) = rest.strip_prefix(['k', 'K', 'm', 'M', 'b', 'B']) {
        return currency_prefix_or_complete(after_scale.strip_prefix(' ').unwrap_or(after_scale));
    }
    false
}

pub(crate) fn possible_start(s: &str) -> bool {
    if s.contains(['-', '–', '—']) && CURRENCY_TRIE.contains_in(s) {
        return true;
    }
    let (core, _) = signed(s);
    currency_at_edge(core) || PER_UNIT.iter().any(|(suffix, _)| core.strip_suffix(suffix).is_some_and(currency_at_edge))
}
fn signed(s: &str) -> (&str, &'static str) {
    if let Some(v) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (v, "negative: \"true\" ")
    } else if let Some(v) = s.strip_prefix('~').or_else(|| s.strip_prefix('≈')) {
        (v.strip_prefix(' ').unwrap_or(v), "negative: \"approx\" ")
    } else if let Some(v) = s.strip_prefix('±') {
        (v.strip_prefix(' ').unwrap_or(v), "negative: \"plus_minus\" ")
    } else {
        (s, "")
    }
}
fn optional_space(s: &str) -> impl Iterator<Item = &str> {
    std::iter::once(s).chain(s.strip_prefix(' '))
}
fn quantity(s: &str) -> Vec<(String, Cost)> {
    let mut values = super::numeric::cardinal(s);
    if let Some((integer, fraction)) = s.split_once('.') {
        if let Some(fraction) = super::numbers::digitwise(fraction) {
            for (integer, weight) in super::numeric::cardinal(integer) {
                values.push((super::lazy::tag(|| format!("{integer} दशमलव {fraction}")), weight));
            }
        }
    }
    values
}
fn emit(out: &mut Vec<Candidate>, end: usize, weight: Cost, fields: impl FnOnce() -> String) {
    out.push(Candidate {
        end,
        cost: cost(1.1) + weight,
        rank: 7,
        tag: super::lazy::tag(|| format!("money {{ {} }}", fields())),
    });
}
fn fractional(s: &str) -> Vec<(String, Cost)> {
    if s.is_empty() || !s.chars().all(super::numbers::is_digit) {
        return Vec::new();
    }
    let mut leading = [0u8; 2];
    let mut count = 0;
    let mut significant = 0;
    for c in s.chars() {
        let value = b'0' + super::numbers::digit_val(c).unwrap() as u8;
        if count < 2 {
            leading[count] = value;
        }
        count += 1;
        if value != b'0' {
            significant = count;
        }
    }
    let (canonical, len) = match significant {
        1 if leading[0] != b'0' => ([leading[0], b'0'], 2),
        2 if leading[0] == b'0' => ([leading[1], 0], 1),
        2 => (leading, 2),
        _ => return Vec::new(),
    };
    super::numeric::cardinal(std::str::from_utf8(&canonical[..len]).unwrap())
}
fn integers(s: &str) -> Vec<(String, Cost)> {
    let mut result = super::numeric::cardinal(s);
    if let Some((integer, fraction)) = s.split_once('.') {
        if !fraction.is_empty() && fraction.chars().all(super::numbers::zero) {
            result.extend(super::numeric::cardinal(integer));
        }
    }
    result
}
fn currency_minor(major: &str) -> Option<&'static str> {
    MAJOR_MINOR_MAP.get(major).copied()
}
fn currency_names() -> impl Iterator<Item = (&'static str, &'static str, &'static str, bool)> {
    rows(CURRENCY_DATA)
        .map(|(symbol, plural)| (symbol, plural, CURRENCY_SG_MAP.get(symbol).copied().unwrap_or(plural), false))
        .chain(ISO_PLURAL.iter().map(|&(symbol, plural)| {
            let singular = ISO_SINGULAR.iter().find_map(|&(key, v)| (key == symbol).then_some(v)).unwrap_or(plural);
            (symbol, plural, singular, true)
        }))
}
fn major_paths(
    s: &str,
    end: usize,
    out: &mut Vec<Candidate>,
    sign: &str,
    per_unit: &str,
    plural: &str,
    singular: &str,
    iso: bool,
    trailing: bool,
) {
    let base = if trailing {
        cost(0.01)
    } else if iso {
        cost(-0.01)
    } else {
        0
    };
    for (value, weight) in integers(s) {
        let one = s == "1"
            || s == "१"
            || s.strip_prefix("1.")
                .or_else(|| s.strip_prefix("१."))
                .is_some_and(|v| !v.is_empty() && v.chars().all(super::numbers::zero));
        if trailing {
            emit(out, end, base + weight, || {
                format!("{sign}integer_part: \"{value}\" currency_maj: \"{plural}\" preserve_order: true{per_unit}")
            });
            if one {
                emit(out, end, base + weight + cost(-0.001), || {
                    format!(
                        "{sign}integer_part: \"{value}\" currency_maj: \"{singular}\" preserve_order: true{per_unit}"
                    )
                });
            }
        } else {
            emit(out, end, base + weight, || {
                format!("{sign}currency_maj: \"{plural}\" integer_part: \"{value}\"{per_unit}")
            });
            if one {
                emit(out, end, base + weight + cost(-0.001), || {
                    format!("{sign}currency_maj: \"{singular}\" integer_part: \"{value}\"{per_unit}")
                });
            }
        }
    }
    if let Some((integer, frac)) = s.split_once('.') {
        if !trailing
            && !iso
            && frac.chars().count() >= 3
            && frac.chars().nth(2).is_some_and(super::numbers::non_zero_digit)
        {
            if let Some(fraction) = super::numbers::digitwise(frac) {
                for (value, weight) in super::numeric::cardinal(integer) {
                    emit(out, end, weight + cost(-0.0005), || {
                        format!(
                            "{sign}currency_maj: \"{plural}\" integer_part: \"{value}\" fractional_part: \"{fraction}\"{per_unit}"
                        )
                    });
                }
            }
        }
        if integer.chars().count() <= 13 && !(iso && plural == "पाकिस्तानी रुपए") {
            let minor_major = if iso && plural == "रूसी रूबल" { "रूबल" } else { plural };
            if let Some(minor) = currency_minor(minor_major) {
                for (value, w1) in super::numeric::cardinal(integer) {
                    for (fraction, w2) in fractional(frac) {
                        if trailing {
                            emit(out, end, cost(0.01) + w1 + w2, || {
                                format!(
                                    "{sign}integer_part: \"{value}\" fractional_part: \"{fraction}\" currency_maj: \"{minor_major}\" currency_min: \"{minor}\" preserve_order: true{per_unit}"
                                )
                            });
                        } else {
                            emit(out, end, w1 + w2, || {
                                format!(
                                    "{sign}currency_maj: \"{minor_major}\" integer_part: \"{value}\" fractional_part: \"{fraction}\" currency_min: \"{minor}\"{per_unit}"
                                )
                            });
                        }
                    }
                }
            }
        }
        if (integer == "1" || integer == "१") && !(iso && singular == "पाकिस्तानी रुपया")
        {
            let minor_major = if iso && singular == "रूसी रूबल" { "रूबल" } else { singular };
            if let Some(minor) = currency_minor(minor_major) {
                for (value, w1) in super::numeric::cardinal(integer) {
                    for (fraction, w2) in fractional(frac) {
                        if trailing {
                            emit(out, end, cost(0.009) + w1 + w2, || {
                                format!(
                                    "{sign}integer_part: \"{value}\" fractional_part: \"{fraction}\" currency_maj: \"{minor_major}\" currency_min: \"{minor}\" preserve_order: true{per_unit}"
                                )
                            });
                        } else {
                            emit(out, end, cost(-0.001) + w1 + w2, || {
                                format!(
                                    "{sign}currency_maj: \"{minor_major}\" integer_part: \"{value}\" fractional_part: \"{fraction}\" currency_min: \"{minor}\"{per_unit}"
                                )
                            });
                        }
                    }
                }
            }
        }
    }
    for &(suffix, scale) in SCALES {
        if let Some(number) = s.strip_suffix(suffix) {
            for (value, weight) in quantity(number) {
                if trailing {
                    emit(out, end, weight + cost(-0.02), || {
                        format!(
                            "{sign}integer_part: \"{value} {scale}\" currency_maj: \"{plural}\" preserve_order: true{per_unit}"
                        )
                    });
                } else {
                    emit(out, end, weight + cost(-0.02), || {
                        format!("{sign}currency_maj: \"{plural}\" integer_part: \"{value} {scale}\"{per_unit}")
                    });
                }
            }
        }
    }
    if !trailing {
        if let Some((number, scale)) = s.rsplit_once(' ') {
            if QUANTITIES_SET.contains(&scale) {
                for (value, weight) in quantity(number) {
                    emit(out, end, weight + cost(-0.01), || {
                        format!("{sign}currency_maj: \"{plural}\" integer_part: \"{value} {scale}\"{per_unit}")
                    });
                }
            }
        }
    }
    for dash in ['-', '–', '—'] {
        if let Some((left, right)) = s.split_once(dash) {
            let left = left.strip_suffix(' ').unwrap_or(left);
            let right = right.strip_prefix(' ').unwrap_or(right);
            for (l, w1) in quantity(left) {
                for (r, w2) in quantity(right) {
                    let range = super::lazy::tag(|| format!("{l} से {r}").replace(' ', "\u{a0}"));
                    if trailing {
                        emit(out, end, cost(-0.01) + w1 + w2, || {
                            format!(
                                "{sign}integer_part: \"{range}\" currency_maj: \"{plural}\" preserve_order: true{per_unit}"
                            )
                        });
                    } else {
                        emit(out, end, cost(-0.01) + w1 + w2, || {
                            format!("{sign}currency_maj: \"{plural}\" integer_part: \"{range}\"{per_unit}")
                        });
                    }
                }
            }
        }
    }
}
fn spoken_prices(s: &str) -> Vec<(String, Cost)> {
    let mut values = Vec::new();
    for (symbol, plural, _, iso) in currency_names() {
        if iso && symbol == "PKR" {
            continue;
        }
        let plural = if iso && symbol == "RUB" { "रूबल" } else { plural };
        if let Some(rest) = s.strip_prefix(symbol) {
            for rest in optional_space(rest) {
                for (v, w) in quantity(rest) {
                    values.push((super::lazy::tag(|| format!("{v} {plural}")), w));
                }
            }
        }
        if let Some(rest) = s.strip_suffix(symbol) {
            for value in std::iter::once(rest).chain(rest.strip_suffix(' ')) {
                for (v, w) in quantity(value) {
                    values.push((super::lazy::tag(|| format!("{v} {plural}")), w));
                }
            }
        }
    }
    values
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    for dash in ['-', '–', '—'] {
        if let Some((left, right)) = s.split_once(dash) {
            let left = left.strip_suffix(' ').unwrap_or(left);
            let right = right.strip_prefix(' ').unwrap_or(right);
            for (l, w1) in spoken_prices(left) {
                for (r, w2) in spoken_prices(right) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1) + w1 + w2,
                        rank: 7,
                        tag: super::lazy::tag(|| {
                            let mut tag = String::with_capacity(l.len() + r.len() + 16);
                            tag.push_str("name: \"");
                            tag.extend(
                                l.chars()
                                    .chain(" से ".chars())
                                    .chain(r.chars())
                                    .map(|c| if c == ' ' { '\u{a0}' } else { c }),
                            );
                            tag.push('"');
                            tag
                        }),
                    });
                }
            }
        }
    }
    let (s, sign) = signed(s);
    let variants = std::iter::once((s, ""))
        .chain(PER_UNIT.iter().filter_map(|&(suffix, value)| s.strip_suffix(suffix).map(|core| (core, value))));
    for (core, unit) in variants {
        let unit = super::lazy::tag(|| if unit.is_empty() { String::new() } else { format!(" per_unit: \"{unit}\"") });
        for (symbol, plural, singular, iso) in currency_names() {
            if let Some(rest) = core.strip_prefix(symbol) {
                for number in optional_space(rest) {
                    major_paths(number, end, out, sign, &unit, plural, singular, iso, false);
                }
                if iso {
                    if let Some((number, scale)) = rest.strip_prefix("  ").and_then(|v| v.rsplit_once(' ')) {
                        if QUANTITIES_SET.contains(&scale) {
                            for (value, weight) in quantity(number) {
                                emit(out, end, weight + cost(-0.01), || {
                                    format!("{sign}currency_maj: \"{plural}\" integer_part: \"{value} {scale}\"{unit}")
                                });
                            }
                        }
                    }
                }
            }
            if let Some(rest) = core.strip_suffix(symbol) {
                for number in std::iter::once(rest).chain(rest.strip_suffix(' ')) {
                    major_paths(number, end, out, sign, &unit, plural, singular, iso, true);
                }
            }
        }
    }
}

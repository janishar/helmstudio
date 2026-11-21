//! Serial classifier for Urdu text normalization.

use super::Sink;
use crate::lang::ur::taggers::cardinal;
use crate::lang::ur::taggers::telephone::digit_to_spoken;
use crate::text::cost;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

struct ExclusionIndex {
    units: HashMap<char, Vec<&'static str>>,
    currency_start: HashMap<char, Vec<&'static str>>,
    currency_end: HashMap<char, Vec<&'static str>>,
    suffixes: HashMap<char, Vec<&'static str>>,
}

static EXCLUSIONS: LazyLock<ExclusionIndex> = LazyLock::new(|| {
    fn keys(data: &'static str) -> impl Iterator<Item = &'static str> {
        data.lines().filter_map(|line| line.split('\t').next()).filter(|s| !s.is_empty())
    }
    let mut index = ExclusionIndex {
        units: HashMap::new(),
        currency_start: HashMap::new(),
        currency_end: HashMap::new(),
        suffixes: HashMap::new(),
    };
    for key in keys(include_str!("../../../../data/ur/measure/unit.tsv")) {
        index.units.entry(key.chars().last().unwrap()).or_default().push(key);
    }
    for key in keys(include_str!("../../../../data/ur/money/currency.tsv")) {
        index.currency_start.entry(key.chars().next().unwrap()).or_default().push(key);
        index.currency_end.entry(key.chars().last().unwrap()).or_default().push(key);
    }
    for key in keys(include_str!("../../../../data/ur/ordinal/suffixes.tsv")) {
        index.suffixes.entry(key.chars().last().unwrap()).or_default().push(key);
    }
    index
});

static SERIAL_CHARS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    include_str!("../../../../data/ur/serial/chars.tsv").lines().filter_map(|line| line.chars().next()).collect()
});

pub const LETTERS: &[(&str, &str)] = &[
    ("a", "اے"),
    ("b", "بی"),
    ("c", "سی"),
    ("d", "ڈی"),
    ("e", "ای"),
    ("f", "ایف"),
    ("g", "جی"),
    ("h", "ایچ"),
    ("i", "آئی"),
    ("j", "جے"),
    ("k", "کے"),
    ("l", "ایل"),
    ("m", "ایم"),
    ("n", "این"),
    ("o", "او"),
    ("p", "پی"),
    ("q", "کیو"),
    ("r", "آر"),
    ("s", "ایس"),
    ("t", "ٹی"),
    ("u", "یو"),
    ("v", "وی"),
    ("w", "ڈبلیو"),
    ("x", "ایکس"),
    ("y", "وائی"),
    ("z", "زیڈ"),
];

pub fn letter_to_spoken(c: char) -> Option<&'static str> {
    let lower = c.to_ascii_lowercase();
    LETTERS.iter().find(|&&(l, _)| l.chars().next() == Some(lower)).map(|&(_, s)| s)
}

pub fn format_code_num(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    // If all digits:
    // 1-3 digits -> cardinal
    // 4+ digits -> digit by digit
    if s.chars().all(|c| c.is_ascii_digit()) {
        if s.len() <= 3 {
            cardinal::code_num(s).map(|(spoken, _)| spoken)
        } else if !crate::lang::ur::lazy::building() {
            Some(String::new())
        } else {
            let mut out = String::new();
            for c in s.chars() {
                let sp = digit_to_spoken(c)?;
                if !out.is_empty() {
                    out.push(' ');
                }
                out.push_str(sp);
            }
            Some(out)
        }
    } else {
        None
    }
}

fn version_num(s: &str) -> Option<String> {
    format_code_num(s).or_else(|| if s.len() >= 2 && s.starts_with('0') { cardinal::digitwise(s) } else { None })
}

pub fn classify(input: &str, end: usize, out: &mut Sink) {
    if input.is_empty() || input.chars().any(crate::text::is_nemo_space) {
        return;
    }

    // Exclusions:
    let numeric_date = ['/', '.', '-'].iter().any(|&sep| {
        let mut parts = input.split(sep);
        match (parts.next(), parts.next(), parts.next(), parts.next()) {
            (Some(a), Some(b), Some(c), None) => {
                (1..=2).contains(&a.len())
                    && (1..=2).contains(&b.len())
                    && c.len() == 4
                    && [a, b, c].iter().all(|p| p.bytes().all(|c| c.is_ascii_digit()))
            }
            _ => false,
        }
    });
    if numeric_date {
        return;
    }
    if excluded_serial(input) {
        return;
    }
    // 2. Pure latin word (foo-bar or abc/def)
    if input.chars().all(|c| c.is_ascii_alphabetic() || matches!(c, '-' | '/'))
        && !input.chars().any(|c| c.is_ascii_digit())
    {
        return;
    }
    // 3. Dimension pattern: e.g. 2x4
    if let Some((left, right)) = input.split_once('x').or_else(|| input.split_once('X')) {
        if left.chars().all(|c| c.is_ascii_digit()) && right.chars().all(|c| c.is_ascii_digit()) {
            return;
        }
    }

    // A. Powers: e.g. 2^2, 2^4, x^2
    if let Some((base, pow)) = input.split_once('^') {
        if !base.is_empty() && !pow.is_empty() {
            let base_spoken = if let Some(sp) = format_code_num(base) {
                sp
            } else if base.len() == 1 {
                if let Some(l) = letter_to_spoken(base.chars().next().unwrap()) {
                    crate::lang::ur::lazy::tag(|| l.to_owned())
                } else {
                    return;
                }
            } else {
                return;
            };

            let pow_spoken = if pow == "2" {
                crate::lang::ur::lazy::tag(|| "کا مربع".to_owned())
            } else if pow == "3" {
                crate::lang::ur::lazy::tag(|| "کا مکعب".to_owned())
            } else {
                let p_num = if let Some(neg) = pow.strip_prefix('-') {
                    if let Some(sp) = format_code_num(neg) {
                        crate::lang::ur::lazy::tag(|| format!("منفی {}", sp))
                    } else {
                        return;
                    }
                } else if let Some(sp) = format_code_num(pow) {
                    sp
                } else {
                    return;
                };
                crate::lang::ur::lazy::tag(|| format!("کی طاقت {}", p_num))
            };

            let spoken = crate::lang::ur::lazy::tag(|| format!("{} {}", base_spoken, pow_spoken));
            out.push(
                end,
                cost(if pow == "2" || pow == "3" { 0.11 } else { 2.11 })
                    + cardinal::code_num(base).map_or(0, |(_, w)| w)
                    + if pow == "2" || pow == "3" {
                        0
                    } else {
                        cardinal::code_num(pow.strip_prefix('-').unwrap_or(pow)).map_or(0, |(_, w)| w)
                    },
                11,
                || {
                    let norm = spoken.replace(' ', "\u{a0}");
                    format!("name: \"{}\"", norm)
                },
            );
            return;
        }
    }

    // B. Dotted versions: v2.0, 17.4.1, v1.2.3
    let (has_v, ver_str) = if let Some(r) = input.strip_prefix('v').or_else(|| input.strip_prefix('V')) {
        (true, r)
    } else {
        (false, input)
    };

    let segment_count = ver_str.split('.').count();
    if (has_v && (2..=5).contains(&segment_count)) || (!has_v && (3..=5).contains(&segment_count)) {
        if ver_str.split('.').all(|s| !s.is_empty() && s.bytes().all(|c| c.is_ascii_digit())) {
            if ver_str.split('.').any(|seg| version_num(seg).is_none()) {
                return;
            }
            out.push(
                end,
                cost(1.11)
                    + ver_str
                        .split('.')
                        .map(|seg| cardinal::code_num(seg).map_or(0, |(_, w)| w))
                        .sum::<crate::text::Cost>(),
                11,
                || {
                    let mut res = Vec::new();
                    if has_v {
                        res.push("وی".to_string());
                    }
                    for (idx, seg) in ver_str.split('.').enumerate() {
                        if idx > 0 {
                            res.push("اعشاریہ".to_string());
                        }
                        if let Some(sp) = version_num(seg) {
                            res.push(sp);
                        }
                    }
                    let joined = res.join(" ").replace(' ', "\u{a0}");
                    format!("name: \"{}\"", joined)
                },
            );
            return;
        }
    }

    if let Some((spoken, weight)) = serial_core(input) {
        out.push(end, cost(1.11) + weight, 11, || format!("name: \"{}\"", spoken.replace(' ', "\u{a0}")));
    }
}

pub(crate) fn serial_core(s: &str) -> Option<(String, crate::text::Cost)> {
    const SYMBOLS: &str = include_str!("../../../../data/ur/serial/special_symbols.tsv");
    let mut out = String::new();
    let building = crate::lang::ur::lazy::building();
    let mut weight = 0;
    let mut i = 0;
    let mut segments = 0;
    let mut previous_mode = 0;
    while i < s.len() {
        let c = s[i..].chars().next().unwrap();
        if matches!(c, '-' | '/') {
            if segments == 0 || previous_mode == 0 || i + c.len_utf8() == s.len() {
                return None;
            }
            if building {
                out.push(c);
            }
            previous_mode = 0;
            i += 1;
            continue;
        }
        let mode = if c.is_ascii_digit() {
            1
        } else if c.is_ascii_alphabetic() {
            2
        } else if SERIAL_CHARS.contains(&c) {
            3
        } else {
            4
        };
        if previous_mode != 0 {
            if (previous_mode == 1 && matches!(mode, 2 | 3)) || (mode == 1 && matches!(previous_mode, 2 | 3)) {
                if building {
                    out.push(' ');
                }
            } else {
                return None;
            }
        }
        let start = i;
        if mode == 4 {
            let sym = SYMBOLS
                .lines()
                .filter_map(|line| line.split_once('\t'))
                .find(|(key, _)| key.chars().next() == Some(c))?
                .1;
            if building {
                out.push_str(sym);
            }
            i += c.len_utf8();
        } else {
            let mut count = 0;
            while i < s.len() {
                let ch = s[i..].chars().next().unwrap();
                let same = match mode {
                    1 => ch.is_ascii_digit(),
                    2 => ch.is_ascii_alphabetic(),
                    _ => SERIAL_CHARS.contains(&ch),
                };
                if !same {
                    break;
                }
                i += ch.len_utf8();
                count += 1;
            }
            let part = &s[start..i];
            match mode {
                1 => {
                    let (spoken, w) = cardinal::code_num(part)?;
                    if building {
                        out.push_str(&spoken);
                    }
                    weight += w;
                }
                2 => {
                    if building {
                        for (n, ch) in part.chars().enumerate() {
                            if n != 0 {
                                out.push(' ');
                            }
                            out.push_str(letter_to_spoken(ch)?);
                        }
                    }
                }
                _ => {
                    if count < 2 {
                        return None;
                    }
                    if building {
                        out.push_str(part);
                    }
                }
            }
        }
        previous_mode = mode;
        segments += 1;
    }
    (segments >= 2).then_some((out, weight))
}

fn serial_number(s: &str) -> bool {
    let integer = match s.split_once('.') {
        Some((a, b)) if !b.is_empty() && b.bytes().all(|c| c.is_ascii_digit()) => a,
        Some(_) => return false,
        None => s,
    };
    let mut groups = integer.split(',');
    let first = groups.next().unwrap_or("");
    !first.is_empty()
        && first.bytes().all(|c| c.is_ascii_digit())
        && groups.all(|g| (2..=3).contains(&g.len()) && g.bytes().all(|c| c.is_ascii_digit()))
}

fn excluded_serial(s: &str) -> bool {
    let index = &*EXCLUSIONS;
    let last = s.chars().last();
    if last
        .and_then(|c| index.suffixes.get(&c))
        .into_iter()
        .flatten()
        .any(|&suffix| s.strip_suffix(suffix).is_some_and(|n| !n.is_empty() && n.bytes().all(|c| c.is_ascii_digit())))
    {
        return true;
    }
    let decade = s.strip_suffix("'s").or_else(|| s.strip_suffix("’s")).or_else(|| s.strip_suffix('s'));
    if let Some(year) = decade {
        let b = year.as_bytes();
        if b.len() == 4 && matches!(b[0], b'1' | b'2') && b[1].is_ascii_digit() && b[2].is_ascii_digit() && b[3] == b'0'
        {
            return true;
        }
    }
    if let Some((a, b)) = s.split_once('-') {
        if a.len() == 4
            && a.starts_with('0')
            && a.bytes().all(|c| c.is_ascii_digit())
            && (7..=8).contains(&b.len())
            && b.bytes().all(|c| c.is_ascii_digit())
        {
            return true;
        }
    }
    let mut octets = s.split('.');
    let valid_octet = |p: &str| (1..=3).contains(&p.len()) && p.bytes().all(|c| c.is_ascii_digit());
    if matches!((octets.next(), octets.next(), octets.next(), octets.next(), octets.next()),
        (Some(a), Some(b), Some(c), Some(d), None) if [a,b,c,d].iter().all(|p| valid_octet(p)))
    {
        return true;
    }
    for &unit in last.and_then(|c| index.units.get(&c)).into_iter().flatten() {
        if let Some(prefix) = s.strip_suffix(unit) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if serial_number(prefix)
                || prefix
                    .split_once(|c| c == '-' || c == '–')
                    .is_some_and(|(a, b)| serial_number(a) && serial_number(b))
            {
                return true;
            }
        }
    }
    let unsigned = s.strip_prefix('-').unwrap_or(s);
    let (unsigned, per) = match unsigned.split_once('/') {
        Some((n, p)) => (n, Some(p)),
        None => (unsigned, None),
    };
    let valid_per =
        per.is_none_or(|p| !p.is_empty() && p.chars().all(|c| c.is_ascii_alphabetic() || matches!(c, '²' | '³')));
    let money_number = |number: &str| {
        serial_number(number)
            || number.strip_suffix(|c| matches!(c, 'k' | 'K' | 'M' | 'm' | 'B' | 'b' | 'T')).is_some_and(serial_number)
    };
    for &currency in unsigned.chars().next().and_then(|c| index.currency_start.get(&c)).into_iter().flatten() {
        if valid_per
            && unsigned.strip_prefix(currency).is_some_and(|rest| money_number(rest.strip_prefix(' ').unwrap_or(rest)))
        {
            return true;
        }
    }
    for &currency in unsigned.chars().last().and_then(|c| index.currency_end.get(&c)).into_iter().flatten() {
        if valid_per
            && unsigned.strip_suffix(currency).is_some_and(|rest| money_number(rest.strip_suffix(' ').unwrap_or(rest)))
        {
            return true;
        }
    }
    false
}

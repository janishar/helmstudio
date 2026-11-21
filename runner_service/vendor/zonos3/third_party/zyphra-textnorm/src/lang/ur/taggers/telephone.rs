//! Urdu telephone classifier.

use super::Sink;
use crate::text::cost;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

struct ContextData {
    prefixes: HashMap<char, Vec<&'static str>>,
    suffixes: HashSet<&'static str>,
}

impl ContextData {
    fn new(data: &'static str) -> Self {
        let mut prefixes: HashMap<char, Vec<&'static str>> = HashMap::new();
        let mut suffixes = HashSet::new();
        for key in data.lines().map(|s| s.trim_end_matches('\r')) {
            if let Some(c) = key.chars().next() {
                prefixes.entry(c).or_default().push(key);
            }
            suffixes.insert(key);
        }
        Self { prefixes, suffixes }
    }
}

static MOBILE: LazyLock<ContextData> =
    LazyLock::new(|| ContextData::new(include_str!("../../../../data/ur/telephone/mobile_context.tsv")));
static LANDLINE: LazyLock<ContextData> =
    LazyLock::new(|| ContextData::new(include_str!("../../../../data/ur/telephone/landline_context.tsv")));
static CREDIT: LazyLock<ContextData> =
    LazyLock::new(|| ContextData::new(include_str!("../../../../data/ur/telephone/credit_context.tsv")));
static PIN: LazyLock<ContextData> =
    LazyLock::new(|| ContextData::new(include_str!("../../../../data/ur/telephone/pincode_context.tsv")));

static CONTEXT_ENDS: LazyLock<HashMap<char, Vec<Vec<char>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Vec<char>>> = HashMap::new();
    for data in [
        include_str!("../../../../data/ur/telephone/mobile_context.tsv"),
        include_str!("../../../../data/ur/telephone/landline_context.tsv"),
        include_str!("../../../../data/ur/telephone/credit_context.tsv"),
        include_str!("../../../../data/ur/telephone/pincode_context.tsv"),
    ] {
        for key in data.lines().map(|line| line.trim_end_matches('\r')) {
            let chars: Vec<char> = key.chars().collect();
            if let Some(&last) = chars.last() {
                index.entry(last).or_default().push(chars);
            }
        }
    }
    index
});

static ASCII_CONTEXT_ENDS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut ends = [false; 128];
    for &c in CONTEXT_ENDS.keys() {
        if c.is_ascii() {
            ends[c as usize] = true;
        }
    }
    ends
});

pub(crate) fn context_ends_at(span: &[char]) -> bool {
    let Some(&last) = span.last() else { return false };
    if last.is_ascii() && !ASCII_CONTEXT_ENDS[last as usize] {
        return false;
    }
    CONTEXT_ENDS.get(&last).is_some_and(|keys| keys.iter().any(|key| span.ends_with(key)))
}

pub(crate) fn possible_start(text: &[char]) -> bool {
    let Some(&first) = text.first() else { return false };
    if first.is_ascii_digit() || matches!(first, '+' | '(') || crate::text::is_nemo_space(first) {
        return true;
    }
    [&*MOBILE, &*LANDLINE, &*CREDIT, &*PIN].iter().any(|data| {
        data.prefixes.get(&first).is_some_and(|keys| {
            keys.iter().any(|key| {
                let len = key.chars().count();
                text.len() > len && text[..len].iter().copied().eq(key.chars()) && text[len] == ' '
            })
        })
    })
}

pub fn digit_to_spoken(c: char) -> Option<&'static str> {
    match c {
        '0' => Some("صفر"),
        '1' => Some("ایک"),
        '2' => Some("دو"),
        '3' => Some("تین"),
        '4' => Some("چار"),
        '5' => Some("پانچ"),
        '6' => Some("چھ"),
        '7' => Some("سات"),
        '8' => Some("آٹھ"),
        '9' => Some("نو"),
        _ => None,
    }
}

pub fn digits_to_spoken(s: &str) -> Option<String> {
    let mut out = String::new();
    for c in s.chars() {
        if matches!(c, '-' | ' ' | '.' | '(' | ')') {
            continue;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(digit_to_spoken(c)?);
    }
    Some(out)
}

fn spoken_digits(s: &str) -> String {
    let mut out = String::new();
    for c in s.chars() {
        if let Some(word) = digit_to_spoken(c) {
            out.push_str(word);
            out.push(' ');
        }
    }
    out
}

fn all_digits(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_digit())
}

fn contexts<'a>(input: &'a str, data: &'static ContextData) -> impl Iterator<Item = (&'a str, &'a str, &'a str)> {
    let last = input.rsplit(' ').next().unwrap_or("");
    let suffix_possible = data.suffixes.contains(last);
    let mut keys =
        input.chars().next().and_then(|c| data.prefixes.get(&c)).map_or(&[][..], Vec::as_slice).iter().copied();
    let mut remaining = input;
    let mut prefix_end = 0;
    let mut extra = 0;
    let mut base = true;
    let mut suffix_pos = 0;
    let mut initial = true;
    std::iter::from_fn(move || {
        loop {
            if base {
                base = false;
                return Some((&input[..prefix_end], remaining, ""));
            }
            if suffix_possible {
                while let Some(relative) = remaining[suffix_pos..].find(' ') {
                    let idx = suffix_pos + relative;
                    suffix_pos = idx + 1;
                    let suffix = &remaining[idx + 1..];
                    let mut words = suffix.rsplit(' ');
                    let _last = words.next();
                    let mut count = 1;
                    let valid = words.all(|word| {
                        count += 1;
                        count <= 6
                            && !word.is_empty()
                            && !word.chars().any(|c| c.is_ascii_digit() || crate::text::is_nemo_space(c))
                    });
                    if valid {
                        return Some((&input[..prefix_end], &remaining[..idx], suffix));
                    }
                }
            }
            if !initial && extra < 5 {
                if let Some((word, next)) = remaining.split_once(' ') {
                    if !word.is_empty() && !word.chars().any(|c| c.is_ascii_digit() || crate::text::is_nemo_space(c)) {
                        prefix_end += word.len() + 1;
                        remaining = next;
                        extra += 1;
                        base = true;
                        suffix_pos = 0;
                        continue;
                    }
                }
            }
            initial = false;
            let mut found = None;
            for key in keys.by_ref() {
                if let Some(rest) = input.strip_prefix(key).and_then(|s| s.strip_prefix(' ')) {
                    found = Some(rest);
                    break;
                }
            }
            remaining = found?;
            prefix_end = input.len() - remaining.len();
            extra = 0;
            base = true;
            suffix_pos = 0;
        }
    })
}

fn emit(out: &mut Sink, end: usize, weight: f64, prefix: &str, digits: &str, rest: &str, suffix: &str, trunk: bool) {
    out.push(end, cost(weight), 8, || {
        let mut number = String::new();
        number.push_str(prefix);
        if trunk {
            number.push_str("صفر ");
        }
        number.push_str(&spoken_digits(digits));
        number.push_str(&spoken_digits(rest));
        number.push_str(suffix);
        format!("number_part: \"{number}\"")
    });
}

pub fn classify(input: &str, end: usize, out: &mut Sink) {
    // Every telephone branch requires at least four ASCII digits in its body.
    let digit_count = input.bytes().filter(u8::is_ascii_digit).count();
    if digit_count < 4 {
        return;
    }

    if digit_count >= 10 {
        for (prefix, body, suffix) in contexts(input, &MOBILE) {
            let body = body.strip_prefix('0').unwrap_or(body);
            if body.len() < 10 || !body.starts_with('3') {
                continue;
            }
            let Some(first) = body.get(..3) else { continue };
            if !all_digits(first) {
                continue;
            }
            let mut rest = &body[3..];
            if rest.chars().next().is_some_and(|c| matches!(c, '-' | ' ' | '.')) {
                rest = &rest[1..];
            }
            if rest.len() >= 7 && all_digits(rest) {
                emit(out, end, 1.8, prefix, first, rest, suffix, true);
                for extension_len in 1..=3 {
                    if rest.len() < 7 + extension_len {
                        continue;
                    }
                    let (number, extension) = rest.split_at(rest.len() - extension_len);
                    out.push(end, cost(1.8), 8, || {
                        format!(
                            "number_part: \"{}صفر {}{}\" extension: \"{}{}\"",
                            prefix,
                            spoken_digits(first),
                            spoken_digits(number),
                            spoken_digits(extension),
                            suffix
                        )
                    });
                }
            }
        }
    }

    if digit_count >= 11 {
        for (prefix, body, suffix) in contexts(input, &MOBILE) {
            let Some(body) = body.strip_prefix('+') else { continue };
            for cc_len in 1..=3 {
                let Some(cc) = body.get(..cc_len) else { continue };
                if !all_digits(cc) {
                    continue;
                }
                let number = body[cc_len..].trim_start_matches(crate::text::is_nemo_space);
                let Some(first) = number.get(..3) else { continue };
                if !first.starts_with('3') || !all_digits(first) {
                    continue;
                }
                let mut rest = &number[3..];
                if rest.chars().next().is_some_and(|c| matches!(c, '-' | ' ' | '.')) {
                    rest = &rest[1..];
                }
                if rest.len() < 7 || !all_digits(rest) {
                    continue;
                }
                out.push(end, cost(1.8), 8, || {
                    format!(
                        "country_code: \"{}پلس {}\" number_part: \"{}{}{}\"",
                        prefix,
                        spoken_digits(cc),
                        spoken_digits(first),
                        spoken_digits(rest),
                        suffix
                    )
                });
                for extension_len in 1..=3 {
                    if rest.len() < 7 + extension_len {
                        continue;
                    }
                    let (number, extension) = rest.split_at(rest.len() - extension_len);
                    out.push(end, cost(1.8), 8, || {
                        format!(
                            "country_code: \"{}پلس {}\" number_part: \"{}{}\" extension: \"{}{}\"",
                            prefix,
                            spoken_digits(cc),
                            spoken_digits(first),
                            spoken_digits(number),
                            spoken_digits(extension),
                            suffix
                        )
                    });
                }
            }
        }
    }

    // A mobile extension may follow a number-side context or deleted whitespace.
    if digit_count >= 11 {
        for (split, ch) in input.char_indices() {
            if !ch.is_ascii_digit() || split == 0 {
                continue;
            }
            let before = input[..split].trim_end_matches(crate::text::is_nemo_space);
            if before.len() == split && !before.ends_with(|c: char| !c.is_ascii_digit()) {
                continue;
            }
            for (prefix, body, number_context) in contexts(before, &MOBILE) {
                for (extension_prefix, extension, extension_context) in contexts(&input[split..], &MOBILE) {
                    if !extension_prefix.is_empty() || !(1..=3).contains(&extension.len()) || !all_digits(extension) {
                        continue;
                    }
                    let emit_extension = |out: &mut Sink, country: Option<&str>, number: &str| {
                        let number = number.strip_prefix('0').filter(|_| country.is_none()).unwrap_or(number);
                        let Some(first) = number.get(..3) else { return };
                        if !first.starts_with('3') || !all_digits(first) {
                            return;
                        }
                        let mut rest = &number[3..];
                        if rest.chars().next().is_some_and(|c| matches!(c, '-' | ' ' | '.')) {
                            rest = &rest[1..];
                        }
                        if rest.len() < 7 || !all_digits(rest) {
                            return;
                        }
                        out.push(end, cost(1.8), 8, || {
                            let country_field = country
                                .map(|cc| format!("country_code: \"{}پلس {}\" ", prefix, spoken_digits(cc)))
                                .unwrap_or_default();
                            let number_prefix =
                                if country.is_none() { format!("{prefix}صفر ") } else { String::new() };
                            format!(
                                "{}number_part: \"{}{}{}{}\" extension: \"{}{}\"",
                                country_field,
                                number_prefix,
                                spoken_digits(first),
                                spoken_digits(rest),
                                number_context,
                                spoken_digits(extension),
                                extension_context
                            )
                        });
                    };
                    if let Some(international) = body.strip_prefix('+') {
                        for len in 1..=3 {
                            let Some(cc) = international.get(..len) else { continue };
                            if all_digits(cc) {
                                emit_extension(
                                    out,
                                    Some(cc),
                                    international[len..].trim_start_matches(crate::text::is_nemo_space),
                                );
                            }
                        }
                    } else {
                        emit_extension(out, None, body);
                    }
                }
            }
        }
    }

    if digit_count >= 10 {
        for (prefix, body, suffix) in contexts(input, &LANDLINE) {
            for length in 2..=7 {
                let stripped = body.strip_prefix('0').unwrap_or(body);
                let (area, rest) = if let Some(after_open) =
                    stripped.trim_start_matches(crate::text::is_nemo_space).strip_prefix('(')
                {
                    let after_open = after_open.trim_start_matches(crate::text::is_nemo_space);
                    let Some((inside, rest)) = after_open.split_once(')') else { continue };
                    let inside = inside.trim_matches(crate::text::is_nemo_space);
                    (inside.strip_prefix('0').unwrap_or(inside), rest)
                } else {
                    let Some(area) = stripped.get(..length) else { continue };
                    (area, &stripped[length..])
                };
                if area.len() != length || !all_digits(area) {
                    continue;
                }
                let rest = rest
                    .strip_prefix('-')
                    .or_else(|| rest.strip_prefix('.'))
                    .unwrap_or(rest)
                    .trim_start_matches(crate::text::is_nemo_space);
                if !all_digits(rest) || !(10 - length..=11 - length).contains(&rest.len()) {
                    continue;
                }
                if !matches!(rest.as_bytes()[0], b'2'..=b'9') {
                    continue;
                }
                emit(out, end, 1.9, prefix, area, rest, suffix, true);
            }
        }
    }
    for (data, min, weight) in [(&*CREDIT, 4, 2.0), (&*PIN, 6, 2.1)] {
        if digit_count < min {
            continue;
        }
        for (prefix, body, suffix) in contexts(input, data) {
            if body.len() >= min && all_digits(body) {
                emit(out, end, weight, prefix, body, "", suffix, false);
            }
        }
    }

    if let Some(rest) = input.strip_prefix('+') {
        for cc_len in 1..=3 {
            let Some(cc) = rest.get(..cc_len) else { continue };
            if !all_digits(cc) {
                continue;
            }
            let rest = &rest[cc_len..];
            let number = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')).unwrap_or(rest);
            let mut count = 0;
            let mut last_sep = false;
            let mut valid = !number.is_empty();
            for c in number.chars() {
                if c.is_ascii_digit() {
                    count += 1;
                    last_sep = false;
                } else if matches!(c, ' ' | '-' | '.') && count > 0 && !last_sep {
                    last_sep = true;
                } else {
                    valid = false;
                    break;
                }
            }
            if valid && !last_sep && (6..=12).contains(&count) {
                out.push(end, cost(2.1), 8, || {
                    let country = spoken_digits(cc);
                    let number = digits_to_spoken(number).unwrap();
                    format!("country_code: \"پلس {}\" number_part: \"{}\"", country.trim_end(), number)
                });
            }
        }
    }
}

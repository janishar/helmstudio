//! Productive Urdu date grammar.

use super::Sink;
use super::cardinal::enumerate_without_leading_zeros;
use crate::text::cost;
use std::collections::HashMap;
use std::collections::HashSet;
use std::sync::LazyLock;

static DATE_ENDS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    let mut ends = HashSet::from(['s', 'ں', 'و']);
    for &name in MONTH_NAMES {
        ends.extend(name.chars().last());
    }
    for &(key, _) in YEAR_SUFFIXES.iter() {
        ends.extend(key.chars().last());
    }
    for &key in SUFFIXES.iter() {
        ends.extend(key.chars().last());
    }
    for data in [
        include_str!("../../../../data/ur/numbers/digit.tsv"),
        include_str!("../../../../data/ur/numbers/teens_and_ties.tsv"),
    ] {
        for (_, word) in data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t')) {
            ends.extend(word.chars().last());
        }
    }
    ends
});

pub(crate) fn possible_end(last: char) -> bool {
    last.is_ascii_digit() || DATE_ENDS.contains(&last)
}

static INPUT_CHARS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    let mut chars: HashSet<char> = "-–/.,، '’sسویں".chars().collect();
    for &name in MONTH_NAMES {
        chars.extend(name.chars());
    }
    for &(key, _) in YEAR_SUFFIXES.iter() {
        chars.extend(key.chars());
    }
    for &key in PREFIXES.iter().chain(SUFFIXES.iter()) {
        chars.extend(key.chars());
    }
    for data in [
        include_str!("../../../../data/ur/numbers/digit.tsv"),
        include_str!("../../../../data/ur/numbers/teens_and_ties.tsv"),
    ] {
        for (_, value) in data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t')) {
            chars.extend(value.chars());
        }
    }
    chars
});

pub(crate) fn continuation(c: char) -> bool {
    c.is_ascii_digit() || INPUT_CHARS.contains(&c)
}

static SPOKEN_DIGITS: LazyLock<HashMap<char, Vec<(Vec<char>, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(Vec<char>, &'static str)>> = HashMap::new();
    for (_, value) in
        include_str!("../../../../data/ur/numbers/digit.tsv").lines().filter_map(|line| line.split_once('\t'))
    {
        let chars: Vec<char> = value.chars().collect();
        if let Some(&first) = chars.first() {
            index.entry(first).or_default().push((chars, value));
        }
    }
    index
});

const MONTH_NAMES: &[&str] =
    &["جنوری", "فروری", "مارچ", "اپریل", "مئی", "جون", "جولائی", "اگست", "ستمبر", "اکتوبر", "نومبر", "دسمبر"];

const YEAR_SUFFIX_DATA: &str = include_str!("../../../../data/ur/date/year_suffix.tsv");
const PREFIXES_DATA: &str = include_str!("../../../../data/ur/date/prefixes.tsv");
const SUFFIXES_DATA: &str = include_str!("../../../../data/ur/date/suffixes.tsv");

static YEAR_SUFFIXES: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| YEAR_SUFFIX_DATA.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t')).collect());

static PREFIXES: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    PREFIXES_DATA.lines().map(|l| l.trim_end_matches('\r').trim()).filter(|l| !l.is_empty()).collect()
});

static SUFFIXES: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    SUFFIXES_DATA.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.trim().is_empty()).collect()
});

static START_KEYS: LazyLock<HashMap<char, Vec<Vec<char>>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<Vec<char>>> = HashMap::new();
    let mut add = |key: &str| {
        let chars: Vec<char> = key.chars().collect();
        if let Some(&c) = chars.first() {
            index.entry(c).or_default().push(chars);
        }
    };
    for &name in MONTH_NAMES {
        add(name);
    }
    for &(key, _) in YEAR_SUFFIXES.iter() {
        add(key);
    }
    for &key in PREFIXES.iter() {
        add(key);
    }
    for data in [
        include_str!("../../../../data/ur/numbers/digit.tsv"),
        include_str!("../../../../data/ur/numbers/teens_and_ties.tsv"),
    ] {
        for (_, spoken) in data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t')) {
            add(spoken);
        }
    }
    index
});

pub(crate) fn possible_start(text: &[char]) -> bool {
    text.first().is_some_and(|c| {
        c.is_ascii_digit() || START_KEYS.get(c).is_some_and(|keys| keys.iter().any(|key| text.starts_with(key)))
    })
}

/// A digit-start date can cross its first input space only into a month,
/// a year suffix, or a year modifier.
pub(crate) fn numeric_space_can_continue(rest: &[char]) -> bool {
    MONTH_NAMES.iter().any(|name| prefix_len(rest, name).is_some())
        || YEAR_SUFFIXES.iter().any(|(key, _)| prefix_len(rest, key).is_some())
        || SUFFIXES.iter().filter_map(|key| key.strip_prefix(' ')).any(|key| prefix_len(rest, key).is_some())
}

fn prefix_len(text: &[char], prefix: &str) -> Option<usize> {
    let mut chars = prefix.chars();
    let mut len = 0;
    while let Some(c) = chars.next() {
        if text.get(len) != Some(&c) {
            return None;
        }
        len += 1;
    }
    Some(len)
}

fn verbalized_year(s: &str) -> bool {
    const TEENS: &str = include_str!("../../../../data/ur/numbers/teens_and_ties.tsv");
    const DIGITS: &str = include_str!("../../../../data/ur/numbers/digit.tsv");
    let Some((hundreds, rest)) = s.split_once(" سو") else { return false };
    let contains = |data: &str, word: &str| {
        data.lines().filter_map(|line| line.trim_end_matches('\r').split_once('\t')).any(|(_, value)| value == word)
    };
    if !contains(TEENS, hundreds) {
        return false;
    }
    if rest.is_empty() {
        return true;
    }
    rest.strip_prefix(' ').is_some_and(|unit| contains(TEENS, unit) || contains(DIGITS, unit))
}

fn parse_day(s: &str) -> Option<&'static str> {
    match s {
        "1" | "01" => Some("ایک"),
        "2" | "02" => Some("دو"),
        "3" | "03" => Some("تین"),
        "4" | "04" => Some("چار"),
        "5" | "05" => Some("پانچ"),
        "6" | "06" => Some("چھ"),
        "7" | "07" => Some("سات"),
        "8" | "08" => Some("آٹھ"),
        "9" | "09" => Some("نو"),
        "10" => Some("دس"),
        "11" => Some("گیارہ"),
        "12" => Some("بارہ"),
        "13" => Some("تیرہ"),
        "14" => Some("چودہ"),
        "15" => Some("پندرہ"),
        "16" => Some("سولہ"),
        "17" => Some("سترہ"),
        "18" => Some("اٹھارہ"),
        "19" => Some("انیس"),
        "20" => Some("بیس"),
        "21" => Some("اکیس"),
        "22" => Some("بائیس"),
        "23" => Some("تئیس"),
        "24" => Some("چوبیس"),
        "25" => Some("پچیس"),
        "26" => Some("چھبیس"),
        "27" => Some("ستائیس"),
        "28" => Some("اٹھائیس"),
        "29" => Some("انتیس"),
        "30" => Some("تیس"),
        "31" => Some("اکتیس"),
        _ => None,
    }
}

fn parse_month_num(s: &str) -> Option<&'static str> {
    match s {
        "1" | "01" => Some("جنوری"),
        "2" | "02" => Some("فروری"),
        "3" | "03" => Some("مارچ"),
        "4" | "04" => Some("اپریل"),
        "5" | "05" => Some("مئی"),
        "6" | "06" => Some("جون"),
        "7" | "07" => Some("جولائی"),
        "8" | "08" => Some("اگست"),
        "9" | "09" => Some("ستمبر"),
        "10" => Some("اکتوبر"),
        "11" => Some("نومبر"),
        "12" => Some("دسمبر"),
        _ => None,
    }
}

fn parse_short(chars: &[char], parse: fn(&str) -> Option<&'static str>) -> Option<&'static str> {
    if chars.is_empty() || chars.len() > 2 || !chars.iter().all(char::is_ascii_digit) {
        return None;
    }
    let bytes = [chars[0] as u8, if chars.len() == 2 { chars[1] as u8 } else { 0 }];
    parse(std::str::from_utf8(&bytes[..chars.len()]).unwrap())
}

fn range_numbers(text: &[char], start: usize, emit: &mut dyn FnMut(usize, String, crate::text::Cost)) {
    let raw = &text[start..];
    let digits = raw.iter().take(4).take_while(|c| c.is_ascii_digit()).count();
    if raw.first().is_some_and(|c| *c != '0') {
        for len in 2..=digits {
            enumerate_without_leading_zeros(&raw[..len], &mut |spoken, weight| emit(start + len, spoken, weight));
        }
    }
    // date.py projects its shared digit FST onto the spoken-word alphabet.
    if let Some(rows) = raw.first().and_then(|c| SPOKEN_DIGITS.get(c)) {
        for (chars, value) in rows {
            if raw.starts_with(chars) {
                emit(start + chars.len(), crate::lang::ur::lazy::tag(|| (*value).to_owned()), 0);
            }
        }
    }
}

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() {
        return;
    }
    let remaining = &text[start..];

    // 1. Check numeric dates: DD-MM-YYYY, DD/MM/YYYY, DD.MM.YYYY, YYYY-MM-DD, DD-MM
    let mut num_prefix_len = 0;
    while num_prefix_len < remaining.len()
        && (remaining[num_prefix_len].is_ascii_digit() || matches!(remaining[num_prefix_len], '-' | '/' | '.'))
    {
        num_prefix_len += 1;
    }

    if num_prefix_len >= 3 && remaining[..num_prefix_len].iter().any(|c| matches!(c, '-' | '/' | '.')) {
        for sep in ['-', '/', '.'] {
            let mut parts = remaining[..num_prefix_len].split(|c| *c == sep);
            let a = parts.next().unwrap();
            let Some(b) = parts.next() else { continue };
            let c = parts.next();
            if parts.next().is_some() {
                continue;
            }
            if let Some(year) = c {
                if let (Some(day), Some(month)) = (parse_short(a, parse_day), parse_short(b, parse_month_num)) {
                    if (sep != '-' || a.len() == b.len()) && year.len() == 4 && year.iter().all(char::is_ascii_digit) {
                        let end = start + a.len() + b.len() + 6;
                        enumerate_without_leading_zeros(year, &mut |spoken, weight| {
                            sink.push(end, cost(1.1) + cost(-0.001) + weight, 0, || {
                                format!("date {{ day: \"{}\" month: \"{}\" year: \"{}\" }}", day, month, spoken)
                            });
                        });
                    }
                }
                if sep == '-' && a.len() == 4 && a.iter().all(char::is_ascii_digit) {
                    if let (Some(month), Some(day)) = (parse_short(b, parse_month_num), parse_short(year, parse_day)) {
                        let end = start + 4 + 1 + b.len() + 1 + year.len();
                        enumerate_without_leading_zeros(a, &mut |spoken, weight| {
                            sink.push(end, cost(1.1) + cost(-0.001) + weight, 0, || {
                                format!("date {{ year: \"{}\" month: \"{}\" day: \"{}\" }}", spoken, month, day)
                            });
                        });
                    }
                }
            } else if sep == '-' && a.len() == b.len() {
                if let (Some(day), Some(month)) = (parse_short(a, parse_day), parse_short(b, parse_month_num)) {
                    let end = start + a.len() + 1 + b.len();
                    sink.push(end, cost(1.1) + cost(-0.001), 0, || {
                        format!("date {{ day: \"{}\" month: \"{}\" }}", day, month)
                    });
                }
            }
        }
    }

    // 2. Dates with written month names:
    // e.g. "6 مارچ، 2010", "14 اگست 1947ء", "مارچ 2024"
    // Case A: Day + MonthName
    let mut day_len = 0;
    while day_len < remaining.len() && remaining[day_len].is_ascii_digit() {
        day_len += 1;
    }
    if day_len > 0 && day_len <= 2 {
        let day_bytes = [remaining[0] as u8, if day_len == 2 { remaining[1] as u8 } else { 0 }];
        let day_str = std::str::from_utf8(&day_bytes[..day_len]).unwrap();
        if let Some(day_spoken) = parse_day(day_str) {
            let after_day = &remaining[day_len..];
            if after_day.starts_with(&[' ']) {
                let m_start = &after_day[1..];
                for &month_name in MONTH_NAMES {
                    if let Some(m_len) = prefix_len(m_start, month_name) {
                        let pos_after_m = day_len + 1 + m_len;
                        // 2a. Just Day + Month
                        let c = cost(1.1) + cost(-0.001);
                        sink.push(start + pos_after_m, c, 0, move || {
                            format!("date {{ day: \"{}\" month: \"{}\" }}", day_spoken, month_name)
                        });

                        // 2b. Day + Month + [comma/sep] + Year [+ Era]
                        let after_m = &remaining[pos_after_m..];
                        let mut sep_len = 0;
                        if after_m.starts_with(&[',', ' ']) || after_m.starts_with(&['،', ' ']) {
                            sep_len = 2;
                        } else if after_m.starts_with(&[','])
                            || after_m.starts_with(&['،'])
                            || after_m.starts_with(&[' '])
                        {
                            sep_len = 1;
                        }
                        if sep_len > 0 {
                            let y_start = &after_m[sep_len..];
                            let mut y_len = 0;
                            while y_len < y_start.len() && y_start[y_len].is_ascii_digit() {
                                y_len += 1;
                            }
                            if y_len == 4 {
                                let y_chars = &y_start[..4];
                                let pos_after_y = pos_after_m + sep_len + 4;

                                // Without era
                                enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                                    let c = cost(1.1) + cost(-0.001) + y_cost;
                                    let y_s = &y_spoken;
                                    sink.push(start + pos_after_y, c, 0, move || {
                                        format!(
                                            "date {{ day: \"{}\" month: \"{}\" year: \"{}\" }}",
                                            day_spoken, month_name, y_s
                                        )
                                    });
                                });

                                // With era
                                let after_y = &remaining[pos_after_y..];
                                let era_offset = if after_y.starts_with(&[' ']) { 1 } else { 0 };
                                let era_rem = &after_y[era_offset..];
                                for &(era_k, era_val) in YEAR_SUFFIXES.iter() {
                                    if let Some(k_len) = prefix_len(era_rem, era_k) {
                                        let end = start + pos_after_y + era_offset + k_len;
                                        enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                                            let c = cost(1.1) + cost(-0.003) + y_cost;
                                            let y_s = &y_spoken;
                                            sink.push(end, c, 0, move || {
                                                format!(
                                                    "date {{ day: \"{}\" month: \"{}\" year: \"{}\" era: \"{}\" }}",
                                                    day_spoken, month_name, y_s, era_val
                                                )
                                            });
                                        });
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    // Case B: MonthName alone + [comma/sep] + Year [+ Era]
    for &month_name in MONTH_NAMES {
        if let Some(m_len) = prefix_len(remaining, month_name) {
            let pos_after_m = m_len;
            let after_m = &remaining[pos_after_m..];
            let mut sep_len = 0;
            if after_m.starts_with(&[',', ' ']) || after_m.starts_with(&['،', ' ']) {
                sep_len = 2;
            } else if after_m.starts_with(&[',']) || after_m.starts_with(&['،']) || after_m.starts_with(&[' ']) {
                sep_len = 1;
            }
            if sep_len > 0 {
                let y_start = &after_m[sep_len..];
                let mut y_len = 0;
                while y_len < y_start.len() && y_start[y_len].is_ascii_digit() {
                    y_len += 1;
                }
                if y_len == 4 {
                    let y_chars = &y_start[..4];
                    let pos_after_y = pos_after_m + sep_len + 4;

                    // Without era (weight -0.2)
                    enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                        let c = cost(1.1) + cost(-0.2) + y_cost;
                        let y_s = &y_spoken;
                        sink.push(start + pos_after_y, c, 0, move || {
                            format!("date {{ month: \"{}\" year: \"{}\" }}", month_name, y_s)
                        });
                    });

                    // With era (weight -0.003)
                    let after_y = &remaining[pos_after_y..];
                    let era_offset = if after_y.starts_with(&[' ']) { 1 } else { 0 };
                    let era_rem = &after_y[era_offset..];
                    for &(era_k, era_val) in YEAR_SUFFIXES.iter() {
                        if let Some(k_len) = prefix_len(era_rem, era_k) {
                            let end = start + pos_after_y + era_offset + k_len;
                            enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                                let c = cost(1.1) + cost(-0.003) + y_cost;
                                let y_s = &y_spoken;
                                sink.push(end, c, 0, move || {
                                    format!(
                                        "date {{ month: \"{}\" year: \"{}\" era: \"{}\" }}",
                                        month_name, y_s, era_val
                                    )
                                });
                            });
                        }
                    }
                }
            }
        }
    }

    // 3. Year + Era Suffix only: "1947ء", "2024 BC" (weight -0.005)
    let mut y_len = 0;
    while y_len < remaining.len() && remaining[y_len].is_ascii_digit() {
        y_len += 1;
    }
    if y_len >= 3 && y_len <= 4 {
        let y_chars = &remaining[..y_len];
        let after_y = &remaining[y_len..];
        let era_offset = if after_y.starts_with(&[' ']) { 1 } else { 0 };
        let era_rem = &after_y[era_offset..];
        for &(era_k, era_val) in YEAR_SUFFIXES.iter() {
            if let Some(k_len) = prefix_len(era_rem, era_k) {
                let end = start + y_len + era_offset + k_len;
                enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                    let c = cost(1.1) + cost(-0.005) + y_cost;
                    let y_s = &y_spoken;
                    sink.push(end, c, 0, move || format!("date {{ era: \"{} {}\" }}", y_s, era_val));
                });
            }
        }
    }

    // 4. International decades: "1990s", "1990's", "2020s" (weight -0.02)
    if y_len == 4 && (remaining[0] == '1' || remaining[0] == '2') && remaining[3] == '0' {
        let after_y = &remaining[4..];
        let mut dec_suf_len = 0;
        if after_y.starts_with(&['s']) {
            dec_suf_len = 1;
        } else if after_y.starts_with(&['\'', 's']) || after_y.starts_with(&['’', 's']) {
            dec_suf_len = 2;
        }
        if dec_suf_len > 0 {
            let end = start + 4 + dec_suf_len;
            let y_chars = &remaining[..4];
            enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                let c = cost(1.1) + cost(-0.02) + y_cost;
                let y_s = &y_spoken;
                sink.push(end, c, 0, move || format!("date {{ era: \"{} کی دہائی\" }}", y_s));
            });
        }
    }

    // 5. Century text: "<cardinal>ویں" (weight -0.001)
    if y_len > 0 && y_len <= 4 && remaining[0] != '0' {
        let after = &remaining[y_len..];
        if after.starts_with(&['و', 'ی', 'ں']) {
            let end = start + y_len + 3;
            let num_chars = &remaining[..y_len];
            enumerate_without_leading_zeros(num_chars, &mut |spoken, num_cost| {
                let c = cost(1.1) + cost(-0.001) + num_cost;
                let s = &spoken;
                sink.push(end, c, 0, move || format!("date {{ era: \"{}ویں\" }}", s));
            });
        }
    }

    // Range endpoints use the source projected-digit date number graph.
    range_numbers(text, start, &mut |first_end, first, first_cost| {
        if !matches!(text.get(first_end), Some('-' | '–')) {
            return;
        }
        range_numbers(text, first_end + 1, &mut |end, second, second_cost| {
            let c = cost(1.1) + cost(-0.005) + first_cost + second_cost;
            sink.push(end, c, 0, || format!("date {{ era: \"{first} سے {second}\" preserve_order: true }}"));
        });
    });

    // 7. Prefix / Suffix with 4-digit years: e.g. "سال 2024", "2024 میں", "سال 2024 میں"
    for &prefix in PREFIXES.iter() {
        if let Some(p_len) = prefix_len(remaining, prefix) {
            let after_p = &remaining[p_len..];
            if after_p.starts_with(&[' ']) {
                let rem_y = &after_p[1..];
                if rem_y.len() >= 4 && rem_y[..4].iter().all(|c| c.is_ascii_digit()) {
                    let y_chars = &rem_y[..4];
                    let pos_after_y = p_len + 1 + 4;
                    // Check optional suffix
                    let after_y = &remaining[pos_after_y..];
                    for &suf in SUFFIXES.iter() {
                        if let Some(s_len) = prefix_len(after_y, suf) {
                            let end = start + pos_after_y + s_len;
                            enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                                let c = cost(1.1) + cost(-0.010) + y_cost;
                                sink.push(end, c, 0, || format!("date {{ era: \"{} {}{}\" }}", prefix, y_spoken, suf));
                            });
                        }
                    }
                    let end = start + pos_after_y;
                    enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                        let c = cost(1.1) + cost(-0.009) + y_cost;
                        sink.push(end, c, 0, || format!("date {{ era: \"{} {}\" }}", prefix, y_spoken));
                    });
                }
            }
        }
    }

    // 8. 4-digit year + Suffix: e.g. "2024 میں"
    if y_len == 4 {
        let y_chars = &remaining[..4];
        let after_y = &remaining[4..];
        for &suf in SUFFIXES.iter() {
            if let Some(s_len) = prefix_len(after_y, suf) {
                let end = start + 4 + s_len;
                enumerate_without_leading_zeros(y_chars, &mut |y_spoken, y_cost| {
                    let c = cost(1.1) + cost(-0.001) + y_cost;
                    sink.push(end, c, 0, || format!("date {{ era: \"{}{}\" }}", y_spoken, suf));
                });
            }
        }
    }

    // Written-out hundred-style years are identity paths in the source graph.
    if remaining.windows(3).any(|w| w == [' ', 'س', 'و']) {
        let written: String = remaining.iter().collect();
        for prefix in std::iter::once(None).chain(PREFIXES.iter().copied().map(Some)) {
            let core = if let Some(prefix) = prefix {
                let Some(rest) = written.strip_prefix(prefix).and_then(|s| s.strip_prefix(' ')) else { continue };
                rest
            } else {
                &written
            };
            for suffix in std::iter::once(None).chain(SUFFIXES.iter().copied().map(Some)) {
                let year = if let Some(suffix) = suffix {
                    let Some(year) = core.strip_suffix(suffix) else { continue };
                    year
                } else {
                    core
                };
                if verbalized_year(year) {
                    let offset = match (prefix.is_some(), suffix.is_some()) {
                        (false, false) => -0.009,
                        (false, true) => -0.010,
                        (true, false) => -0.011,
                        (true, true) => -0.012,
                    };
                    sink.push(text.len(), cost(1.1) + cost(offset), 0, || format!("date {{ era: \"{}\" }}", written));
                }
            }
        }
    }

    // 9. Year suffix alone: "ء", "BC" (weight -0.001)
    for &(era_k, era_val) in YEAR_SUFFIXES.iter() {
        if let Some(k_len) = prefix_len(remaining, era_k) {
            let end = start + k_len;
            let c = cost(1.1) + cost(-0.001);
            sink.push(end, c, 0, move || format!("date {{ era: \"{}\" }}", era_val));
        }
    }
}

//! Swedish date classifier: DMY, YMD, MY, year with era, and decades.

use super::number::{cardinal, ordinal};

crate::tsv!(MONTHS, "sv/dates/months.tsv");
crate::tsv!(MONTH_ABBR, "sv/dates/month_abbr.tsv");
crate::tsv!(ERA_WORDS, "sv/dates/era_words.tsv");
crate::tsv!(ERA_SUFFIX, "sv/dates/era_suffix.tsv");

static ERA_VALUES: std::sync::LazyLock<crate::text::Table> = std::sync::LazyLock::new(|| {
    crate::text::Table::from_pairs(ERA_WORDS.rows().iter().map(|row| (row.value.clone(), row.value.clone())))
});
static MONTH_VALUES: std::sync::LazyLock<crate::text::Table> = std::sync::LazyLock::new(|| {
    crate::text::Table::from_pairs(MONTHS.rows().iter().map(|row| (row.value.clone(), row.value.clone())))
});

/// Format a year string (usually 4 digits or 2 digits) into Swedish spoken year.
pub fn format_year(y: &str) -> Option<String> {
    if y.is_empty() || y.len() > 4 || y.starts_with('0') || !y.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if y.len() >= 3 {
        let first = &y[..y.len() - 2];
        let second = &y[y.len() - 2..];
        let f_card = cardinal(first, false, false)?;
        if second == "00" {
            Some(format!("{f_card}hundra"))
        } else if second.starts_with('0') {
            let rem_d = &second[1..];
            let d_card = cardinal(rem_d, false, false)?;
            Some(format!("{f_card}hundra{d_card}"))
        } else {
            let s_card = cardinal(second, false, false)?;
            Some(format!("{f_card}{s_card}"))
        }
    } else {
        cardinal(y, false, false)
    }
}

/// Match era suffix at `text[start..]`. Returns `(spoken, len)`.
pub fn match_era(text: &[char], start: usize) -> Option<(&str, usize)> {
    let rem = &text[start..];
    let mut best = None;
    for (row, end) in ERA_VALUES.prefix_matches(rem, 0) {
        if best.as_ref().map_or(true, |(_, old_end)| end > *old_end) {
            best = Some((row.value.as_str(), end));
        }
    }
    for (row, end) in ERA_SUFFIX.prefix_matches(rem, 0) {
        if let Some(word) = ERA_WORDS.exact(&row.value).next() {
            if best.as_ref().map_or(true, |(_, old_end)| end > *old_end) {
                best = Some((word.value.as_str(), end));
            }
        }
    }
    best
}

/// Match month name or abbreviation at `text[start..]`. Returns `(spoken, len)`.
pub fn match_month_name(text: &[char], start: usize) -> Option<(&str, usize)> {
    let rem = &text[start..];
    let mut best = None;
    for (row, end) in MONTH_VALUES.prefix_matches(rem, 0) {
        if best.as_ref().map_or(true, |(_, old_end)| end > *old_end) {
            best = Some((row.value.as_str(), end));
        }
    }
    for (row, end) in MONTH_ABBR.prefix_matches(rem, 0) {
        let len = end + if rem.get(end) == Some(&'.') { 1 } else { 0 };
        if best.as_ref().map_or(true, |(_, old_end)| len > *old_end) {
            best = Some((row.value.as_str(), len));
        }
    }
    best
}

/// Match numeric month (1..12 or 01..12). Returns `(spoken, len)`.
pub fn match_numeric_month(text: &[char], start: usize) -> Option<(&'static str, usize)> {
    let rem = &text[start..];
    if rem.is_empty() || !rem[0].is_ascii_digit() {
        return None;
    }
    let len = if rem.len() >= 2 && rem[0].is_ascii_digit() && rem[1].is_ascii_digit() { 2 } else { 1 };
    let n = rem[..len].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
    if (1..=12).contains(&n) {
        let spoken = match n {
            1 => "januari",
            2 => "februari",
            3 => "mars",
            4 => "april",
            5 => "maj",
            6 => "juni",
            7 => "juli",
            8 => "augusti",
            9 => "september",
            10 => "oktober",
            11 => "november",
            12 => "december",
            _ => return None,
        };
        Some((spoken, len))
    } else {
        None
    }
}

#[derive(Clone, Copy)]
enum Day<'a> {
    Written(&'a [char]),
    Number(u32),
    Spoken(&'a str),
}

impl Day<'_> {
    fn render(self) -> String {
        match self {
            Self::Written(chars) => {
                let digits: String = chars.iter().collect();
                ordinal(&digits).expect("validated ordinal")
            }
            Self::Number(n) => ordinal(&n.to_string()).expect("validated day"),
            Self::Spoken(value) => value.to_owned(),
        }
    }
}

fn match_date_day(text: &[char], start: usize) -> Option<(Day<'_>, usize)> {
    let rem = &text[start..];
    if !rem.first().is_some_and(|c| c.is_ascii_digit()) {
        return None;
    }
    let digit_run = rem.iter().take_while(|c| c.is_ascii_digit()).count();
    if rem.get(digit_run) == Some(&':') && digit_run <= 24 {
        if let Some(&suffix) = rem.get(digit_run + 1) {
            let mut bytes = [0u8; 24];
            for (out, c) in bytes.iter_mut().zip(&rem[..digit_run]) {
                *out = *c as u8;
            }
            let written = std::str::from_utf8(&bytes[..digit_run]).expect("ASCII digits");
            if super::ordinal::valid_suffix(written, suffix) {
                let mut end = digit_run;
                let mut valid = true;
                while end > 3 {
                    end -= 3;
                    let begin = end.saturating_sub(3);
                    let group = bytes[begin..end].iter().fold(0u16, |n, b| n * 10 + (b - b'0') as u16);
                    if (2..100).contains(&group) {
                        valid = false;
                        break;
                    }
                }
                if valid {
                    return Some((Day::Written(&rem[..digit_run]), digit_run + 2));
                }
                return None;
            }
        }
    }
    let num_len = digit_run.min(2);
    let n = rem[..num_len].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
    if !(1..=31).contains(&n) {
        return None;
    }
    let total_len = num_len + usize::from(rem.get(num_len) == Some(&'.'));
    Some((Day::Number(n), total_len))
}

fn match_spoken_day(text: &[char]) -> Option<(Day<'_>, usize)> {
    static DAYS: std::sync::LazyLock<crate::text::Table> = std::sync::LazyLock::new(|| {
        crate::text::Table::from_pairs((1..=31).filter_map(|n| {
            let value = ordinal(&n.to_string())?;
            Some((value.clone(), value))
        }))
    });
    DAYS.prefix_matches(text, 0).last().map(|(row, end)| (Day::Spoken(&row.value), end))
}

/// Match date forms at `text[start..]`. Returns `Vec<(end_pos, tag)>`.
pub fn match_date(text: &[char], start: usize) -> Vec<(usize, String)> {
    let mut out = Vec::new();
    let rem = &text[start..];
    if rem.is_empty() {
        return out;
    }

    // 1. Decades: e.g. "1990-talet", "90-talet", "1990-tal", "1990s", "2020s"
    if let Some((d_end, d_tag)) = match_decade(rem) {
        out.push((start + d_end, d_tag));
    }

    // 2. Year + era: e.g. "1990 f.Kr.", "44 f.Kr."
    if let Some((ye_end, ye_tag)) = match_year_era(rem) {
        out.push((start + ye_end, ye_tag));
    }

    // Month-year and day-month-year joins use plain_space: zero or more
    // NEMO whitespace characters on input, one space on output.
    if let Some((month, month_end)) = match_month_name(rem, 0) {
        let mut at = month_end;
        if rem.get(at) == Some(&',') {
            at += 1;
        }
        while at < rem.len() && crate::text::is_nemo_space(rem[at]) {
            at += 1;
        }
        for len in 1..=4 {
            if at + len > rem.len() || !rem[at + len - 1].is_ascii_digit() {
                break;
            }
            let written: String = rem[at..at + len].iter().collect();
            let Some(year) = format_year(&written) else { continue };
            let end = at + len;
            out.push((start + end, format!("date {{ month: \"{month}\" year: \"{year}\" preserve_order: true }}")));
            let mut era_at = end;
            while era_at < rem.len() && crate::text::is_nemo_space(rem[era_at]) {
                era_at += 1;
            }
            if let Some((era, era_len)) = match_era(rem, era_at) {
                out.push((
                    start + era_at + era_len,
                    format!("date {{ month: \"{month}\" year: \"{year}\" era: \"{era}\" preserve_order: true }}"),
                ));
            }
        }
    }

    if let Some((day, day_end)) = match_date_day(rem, 0).or_else(|| match_spoken_day(rem)) {
        let mut month_at = day_end;
        while month_at < rem.len() && crate::text::is_nemo_space(rem[month_at]) {
            month_at += 1;
        }
        if let Some((month, month_len)) = match_month_name(rem, month_at) {
            let day = day.render();
            let month_end = month_at + month_len;
            let tag = format!("date {{ day: \"{day}\" month: \"{month}\" }}");
            out.push((start + month_end, tag.clone()));
            let mut at = month_end;
            if rem.get(at) == Some(&',') {
                at += 1;
                out.push((start + at, tag));
            }
            while at < rem.len() && crate::text::is_nemo_space(rem[at]) {
                at += 1;
            }
            for len in 1..=4 {
                if at + len > rem.len() || !rem[at + len - 1].is_ascii_digit() {
                    break;
                }
                let written: String = rem[at..at + len].iter().collect();
                let Some(year) = format_year(&written) else { continue };
                let end = at + len;
                out.push((
                    start + end,
                    format!("date {{ day: \"{day}\" month: \"{month}\" year: \"{year}\" preserve_order: true }}"),
                ));
                let mut era_at = end;
                while era_at < rem.len() && crate::text::is_nemo_space(rem[era_at]) {
                    era_at += 1;
                }
                if let Some((era, era_len)) = match_era(rem, era_at) {
                    out.push((start + era_at + era_len, format!("date {{ day: \"{day}\" month: \"{month}\" year: \"{year}\" era: \"{era}\" preserve_order: true }}")));
                }
            }
        }
    }

    // 5. Numeric dates: YMD and DMY with separators '-', '.', '/'
    // Numeric year-month has an optional day; month and day may each have
    // one or two digits. The deterministic numeric year has four digits.
    if rem.len() >= 6 && rem[0] != '0' && rem[..4].iter().all(|c| c.is_ascii_digit()) {
        let sep = rem[4];
        if matches!(sep, '-' | '.' | '/') {
            let y_str: String = rem[..4].iter().collect();
            if let Some(y_spoken) = format_year(&y_str) {
                for month_len in 1..=2 {
                    let month_end = 5 + month_len;
                    if month_end > rem.len() {
                        break;
                    }
                    let Some((m_spoken, matched)) = match_numeric_month(&rem[..month_end], 5) else { continue };
                    if matched != month_len {
                        continue;
                    }
                    out.push((start + month_end, format!("date {{ year: \"{y_spoken}\" month: \"{m_spoken}\" }}")));
                    if rem.get(month_end) != Some(&sep) {
                        continue;
                    }
                    let day_start = month_end + 1;
                    for day_len in 1..=2 {
                        let day_end = day_start + day_len;
                        if day_end > rem.len() {
                            break;
                        }
                        let Some((day_spoken, matched)) = match_date_day(&rem[..day_end], day_start) else { continue };
                        if matched != day_len {
                            continue;
                        }
                        let day_spoken = day_spoken.render();
                        let tag =
                            format!("date {{ year: \"{y_spoken}\" month: \"{m_spoken}\" day: \"{day_spoken}\" }}");
                        out.push((start + day_end, tag.clone()));
                        if sep == '-' && rem.get(day_end) == Some(&'.') {
                            out.push((start + day_end + 1, tag));
                        }
                    }
                }
            }
        }
    }

    // Numeric DMY: 2.1.2020, 02.01.2020, 2/1/2020, 2-1-2020
    for sep in ['.', '/', '-'] {
        if let Some(first_sep) = rem.iter().take(3).position(|&c| c == sep) {
            if first_sep >= 1 && first_sep <= 2 && rem[..first_sep].iter().all(|c| c.is_ascii_digit()) {
                let d_num = rem[..first_sep].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
                if (1..=31).contains(&d_num) {
                    let after_first = &rem[first_sep + 1..];
                    if let Some(second_sep) = after_first.iter().take(3).position(|&c| c == sep) {
                        if second_sep >= 1
                            && second_sep <= 2
                            && after_first[..second_sep].iter().all(|c| c.is_ascii_digit())
                        {
                            let m_num =
                                after_first[..second_sep].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
                            if (1..=12).contains(&m_num) {
                                let after_second = &after_first[second_sep + 1..];
                                if after_second.len() >= 4 && after_second[..4].iter().all(|c| c.is_ascii_digit()) {
                                    let y_str: String = after_second[..4].iter().collect();
                                    if let (Some(d_spoken), Some(y_spoken)) =
                                        (ordinal(&d_num.to_string()), format_year(&y_str))
                                    {
                                        let m_spoken = match m_num {
                                            1 => "januari",
                                            2 => "februari",
                                            3 => "mars",
                                            4 => "april",
                                            5 => "maj",
                                            6 => "juni",
                                            7 => "juli",
                                            8 => "augusti",
                                            9 => "september",
                                            10 => "oktober",
                                            11 => "november",
                                            12 => "december",
                                            _ => "",
                                        };
                                        let total_len = first_sep + 1 + second_sep + 1 + 4;
                                        let tag = format!(
                                            "date {{ day: \"{d_spoken}\" month: \"{m_spoken}\" year: \"{y_spoken}\" }}"
                                        );
                                        out.push((start + total_len, tag));
                                        let mut era_at = total_len;
                                        while era_at < rem.len() && crate::text::is_nemo_space(rem[era_at]) {
                                            era_at += 1;
                                        }
                                        if let Some((era, era_len)) = match_era(rem, era_at) {
                                            out.push((start + era_at + era_len, format!("date {{ day: \"{d_spoken}\" month: \"{m_spoken}\" year: \"{y_spoken}\" era: \"{era}\" }}")));
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }

    out
}

fn match_decade(s: &[char]) -> Option<(usize, String)> {
    // 1990s, 2020s
    if s.len() >= 5 && (s.starts_with(&['1', '8']) || s.starts_with(&['1', '9']) || s.starts_with(&['2', '0'])) {
        if s[2].is_ascii_digit() && s[3] == '0' {
            if s[4] == 's' || (s.get(4) == Some(&'\'') && s.get(5) == Some(&'s')) {
                let first_two: String = s[..2].iter().collect();
                let f_card = cardinal(&first_two, false, false)?;
                let third = s[2];
                let tens_str = match third {
                    '1' => "tio",
                    '2' => "tjugo",
                    '3' => "trettio",
                    '4' => "fyrtio",
                    '5' => "femtio",
                    '6' => "sextio",
                    '7' => "sjuttio",
                    '8' => "åttio",
                    '9' => "nittio",
                    '0' => "hundra",
                    _ => return None,
                };
                let spoken = format!("{f_card}{tens_str}talet");
                let end = if s[4] == 's' { 5 } else { 6 };
                return Some((end, format!("date {{ year: \"{spoken}\" }}")));
            }
        }
    }

    // 1990-talet, 90-talet, 1990-tal
    let digit_len = s.iter().take_while(|c| c.is_ascii_digit()).count();
    if (2..=4).contains(&digit_len)
        && s[digit_len - 1] == '0'
        && s.get(digit_len) == Some(&'-')
        && (s[0] != '0' || (digit_len == 2 && s[1] == '0'))
    {
        let after_dash = &s[digit_len + 1..];
        let tal_len = if after_dash.starts_with(&['t', 'a', 'l', 'e', 't']) {
            5
        } else if after_dash.starts_with(&['t', 'a', 'l', 's']) {
            4 + after_dash[4..]
                .iter()
                .take_while(|c| {
                    c.is_ascii_alphabetic() || matches!(**c, 'Å' | 'Ä' | 'Ö' | 'Ü' | 'É' | 'å' | 'ä' | 'ö' | 'ü' | 'é')
                })
                .count()
        } else if after_dash.starts_with(&['t', 'a', 'l']) {
            3
        } else {
            0
        };
        if tal_len > 0 {
            let y_digits: String = s[..digit_len].iter().collect();
            let y_spoken = if y_digits == "00" { "hundra".to_string() } else { format_year(&y_digits)? };
            let total_end = digit_len + 1 + tal_len;
            let sfx: String = after_dash[..tal_len].iter().collect();
            let spoken = format!("{y_spoken}{sfx}");
            return Some((total_end, format!("date {{ year: \"{spoken}\" }}")));
        }
    }

    None
}

fn match_year_era(s: &[char]) -> Option<(usize, String)> {
    let digit_len = s.iter().take_while(|c| c.is_ascii_digit()).count();
    if digit_len >= 1 && digit_len <= 4 {
        if s[0] == '0' {
            return None;
        }
        let mut era_start = digit_len;
        while era_start < s.len() && crate::text::is_nemo_space(s[era_start]) {
            era_start += 1;
        }
        if let Some((era_spoken, era_len)) = match_era(s, era_start) {
            let y_digits: String = s[..digit_len].iter().collect();
            let y_spoken = format_year(&y_digits)?;
            let total_end = era_start + era_len;
            let tag = format!("date {{ year: \"{y_spoken}\" era: \"{era_spoken}\" preserve_order: true }}");
            return Some((total_end, tag));
        }
    }
    None
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese date classifier: DMY, YMD, Vietnamese date phrases, era years, decades.

use super::super::graph_utils::{WEIGHT_DATE, cardinal_cost, spell_cardinal_digits};
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Date;

crate::labels!(DAYS_LABELS, "vi/date/days.tsv");
crate::labels!(MONTHS_LABELS, "vi/date/months.tsv");
crate::labels!(YEAR_SUFFIX_LABELS, "vi/date/year_suffix.tsv");

static ERA_INITIALS: std::sync::LazyLock<[bool; 128]> = std::sync::LazyLock::new(|| {
    let mut initials = [false; 128];
    for row in YEAR_SUFFIX_LABELS.iter() {
        if let Some(c) = row.first().and_then(|s| s.bytes().next()).filter(u8::is_ascii) {
            initials[c.to_ascii_lowercase() as usize] = true;
            initials[c.to_ascii_uppercase() as usize] = true;
        }
    }
    initials
});

fn label_slot(key: &[char]) -> Option<usize> {
    match key {
        [a] if a.is_ascii_digit() => Some((*a as u8 - b'0') as usize),
        [a, b] if a.is_ascii_digit() && b.is_ascii_digit() => {
            Some(10 + (*a as u8 - b'0') as usize * 10 + (*b as u8 - b'0') as usize)
        }
        _ => None,
    }
}

fn make_number_index(rows: &'static [Vec<String>]) -> [Option<&'static str>; 110] {
    let mut index = [None; 110];
    for row in rows {
        if row.len() < 2 {
            continue;
        }
        let mut chars = row[0].chars();
        let mut key = ['\0'; 2];
        let mut len = 0;
        while let Some(c) = chars.next() {
            if len == key.len() {
                break;
            }
            key[len] = c;
            len += 1;
        }
        if row[0].chars().count() != len {
            continue;
        }
        if let Some(slot) = label_slot(&key[..len]) {
            if index[slot].is_none() {
                index[slot] = Some(row[1].as_str());
            }
        }
    }
    index
}

static DAY_INDEX: std::sync::LazyLock<[Option<&'static str>; 110]> =
    std::sync::LazyLock::new(|| make_number_index(&DAYS_LABELS));
static MONTH_INDEX: std::sync::LazyLock<[Option<&'static str>; 110]> =
    std::sync::LazyLock::new(|| make_number_index(&MONTHS_LABELS));

fn get_day(s: &[char]) -> Option<&'static str> {
    DAY_INDEX[label_slot(s)?]
}

fn get_month(s: &[char]) -> Option<&'static str> {
    MONTH_INDEX[label_slot(s)?]
}

fn get_era(abbr: &str) -> Option<&'static str> {
    for row in YEAR_SUFFIX_LABELS.iter() {
        let (Some(key), Some(value)) = (row.first(), row.get(1)) else { continue };
        if abbr == key.as_str()
            || (!key.eq_ignore_ascii_case("cn")
                && abbr.eq_ignore_ascii_case(key)
                && (abbr.bytes().all(|b| b.is_ascii_lowercase()) || abbr.bytes().all(|b| b.is_ascii_uppercase())))
        {
            return Some(value);
        }
    }
    None
}

fn scan_eras(text: &[char], start: usize, mut f: impl FnMut(usize, &'static str)) {
    let Some(&first) = text.get(start) else { return };
    if !first.is_ascii() || !ERA_INITIALS[first as usize] {
        return;
    }
    let mut bytes = [0u8; 16];
    let mut used = 0;
    for &c in text[start..].iter().take(4) {
        used += c.encode_utf8(&mut bytes[used..]).len();
    }
    let rem = std::str::from_utf8(&bytes[..used]).expect("encoded characters are UTF-8");
    for len in [3, 2, 1] {
        if let Some(era) = rem.get(..len).and_then(get_era) {
            f(start + len, era);
        }
    }
}

fn number(digits: &[u8], out: &mut String, render: bool) -> Option<Cost> {
    let cost = cardinal_cost(digits)?;
    if render {
        spell_cardinal_digits(digits, out);
    }
    Some(cost)
}

impl Date {
    pub fn scan_spans(text: &[char], start: usize, f: impl FnMut(usize, String, Cost)) {
        Self::scan(text, start, true, f);
    }

    fn scan(text: &[char], start: usize, render: bool, mut f: impl FnMut(usize, String, Cost)) {
        macro_rules! format {
            ($($arg:tt)*) => { if render { std::format!($($arg)*) } else { String::new() } };
        }
        if start >= text.len() {
            return;
        }

        // 1. Decade patterns: e.g. "1980s", "1980's", "'80s"
        if text[start] == '\''
            && start + 3 <= text.len()
            && text[start + 1].is_ascii_digit()
            && text[start + 2] == '0'
            && start + 3 < text.len()
            && text[start + 3] == 's'
        {
            let d = text[start + 1] as u8;
            let mut decade_words = String::new();
            if let Some(c) = number(&[d, b'0'], &mut decade_words, render) {
                f(start + 4, format!("text: \"những năm {}\"", decade_words), c);
            }
        }
        if (text[start] == '1' || text[start] == '2') && start + 4 <= text.len() {
            if text[start + 1].is_ascii_digit() && text[start + 2].is_ascii_digit() && text[start + 3] == '0' {
                let end = if start + 5 <= text.len() && text[start + 4] == 's' {
                    Some(start + 5)
                } else if start + 6 <= text.len() && text[start + 4] == '\'' && text[start + 5] == 's' {
                    Some(start + 6)
                } else {
                    None
                };
                if let Some(e) = end {
                    let digits = [text[start] as u8, text[start + 1] as u8, text[start + 2] as u8, b'0'];
                    let mut decade_words = String::new();
                    if let Some(c) = number(&digits, &mut decade_words, render) {
                        f(e, format!("text: \"những năm {}\"", decade_words), c);
                    }
                }
            }
        }

        // 2. Phrases starting with "ngày "
        const NGAY: [char; 5] = ['n', 'g', 'à', 'y', ' '];
        if start + 5 < text.len() && text[start..start + 5] == NGAY {
            let day_start = start + 5;
            for d_len in 1..=2 {
                if !text.get(day_start + d_len - 1).is_some_and(char::is_ascii_digit) {
                    break;
                }
                if let Some(day_word) = get_day(&text[day_start..day_start + d_len]) {
                    let p1 = day_start + d_len;

                    // 2a. ngày D/M/YYYY or ngày D-M-YYYY or ngày D.M.YYYY (year can be 1..4 digits)
                    if p1 < text.len() && (text[p1] == '/' || text[p1] == '-' || text[p1] == '.') {
                        let sep = text[p1];
                        let m_start = p1 + 1;
                        for m_len in 1..=2 {
                            if !text.get(m_start + m_len - 1).is_some_and(char::is_ascii_digit) {
                                break;
                            }
                            if let Some(month_word) = get_month(&text[m_start..m_start + m_len]) {
                                let p2 = m_start + m_len;

                                // ngày D sep M alone
                                f(p2, format!("day: \"{}\" month: \"{}\"", day_word, month_word), 0);

                                // with year
                                if p2 < text.len() && text[p2] == sep {
                                    let y_start = p2 + 1;
                                    let mut y_digits = [0u8; 4];
                                    let mut len = 0;
                                    let mut k = y_start;
                                    while k < text.len() && text[k].is_ascii_digit() && len < 4 {
                                        y_digits[len] = text[k] as u8;
                                        len += 1;
                                        k += 1;
                                        let mut y_str = String::new();
                                        if let Some(c) = number(&y_digits[..len], &mut y_str, render) {
                                            f(
                                                k,
                                                format!(
                                                    "day: \"{}\" month: \"{}\" year: \"{}\"",
                                                    day_word, month_word, y_str
                                                ),
                                                c,
                                            );
                                        }
                                    }
                                }
                            }
                        }
                    }

                    // 2b. ngày D tháng M [năm Y]
                    const THANG: [char; 7] = [' ', 't', 'h', 'á', 'n', 'g', ' '];
                    if p1 + 7 < text.len() && text[p1..p1 + 7] == THANG {
                        let m_start = p1 + 7;
                        for m_len in 1..=2 {
                            if !text.get(m_start + m_len - 1).is_some_and(char::is_ascii_digit) {
                                break;
                            }
                            if let Some(month_word) = get_month(&text[m_start..m_start + m_len]) {
                                let p2 = m_start + m_len;
                                f(p2, format!("day: \"{}\" month: \"{}\"", day_word, month_word), 0);

                                const NAM: [char; 5] = [' ', 'n', 'ă', 'm', ' '];
                                if p2 + 5 < text.len() && text[p2..p2 + 5] == NAM {
                                    let y_start = p2 + 5;
                                    let mut y_digits = [0u8; 4];
                                    let mut len = 0;
                                    let mut k = y_start;
                                    while k < text.len() && text[k].is_ascii_digit() && len < 4 {
                                        y_digits[len] = text[k] as u8;
                                        len += 1;
                                        k += 1;
                                        let mut y_str = String::new();
                                        if let Some(c) = number(&y_digits[..len], &mut y_str, render) {
                                            f(
                                                k,
                                                format!(
                                                    "day: \"{}\" month: \"{}\" year: \"{}\"",
                                                    day_word, month_word, y_str
                                                ),
                                                c,
                                            );
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }

        // 3. Phrases starting with "tháng "
        const THANG_PREFIX: [char; 6] = ['t', 'h', 'á', 'n', 'g', ' '];
        if start + 6 < text.len() && text[start..start + 6] == THANG_PREFIX {
            let m_start = start + 6;
            for m_len in 1..=2 {
                if !text.get(m_start + m_len - 1).is_some_and(char::is_ascii_digit) {
                    break;
                }
                if let Some(month_word) = get_month(&text[m_start..m_start + m_len]) {
                    let p1 = m_start + m_len;
                    if p1 < text.len() {
                        let sep = text[p1];
                        if sep == '/' || sep == '-' || sep == '.' || sep == ' ' {
                            let y_start = p1 + 1;
                            let mut y_digits = [0u8; 4];
                            let mut len = 0;
                            let mut k = y_start;
                            while k < text.len() && text[k].is_ascii_digit() && len < 4 {
                                y_digits[len] = text[k] as u8;
                                len += 1;
                                k += 1;
                                let mut y_str = String::new();
                                if let Some(c) = number(&y_digits[..len], &mut y_str, render) {
                                    f(k, format!("month: \"{}\" year: \"{}\"", month_word, y_str), c);
                                }
                            }
                        }
                    }
                }
            }
        }

        // 4. Phrases starting with "năm " or "năm thứ "
        const NAM_THU: [char; 8] = ['n', 'ă', 'm', ' ', 't', 'h', 'ứ', ' '];
        if start + 8 < text.len() && text[start..start + 8] == NAM_THU {
            let y_start = start + 8;
            let mut y_digits = [0u8; 4];
            let mut len = 0;
            let mut k = y_start;
            while k < text.len() && text[k].is_ascii_digit() && len < 4 {
                y_digits[len] = text[k] as u8;
                len += 1;
                k += 1;
                let mut y_str = String::new();
                if let Some(c) = number(&y_digits[..len], &mut y_str, render) {
                    if k < text.len() && text[k] == ' ' {
                        let era_start = k + 1;
                        scan_eras(text, era_start, |end, era_full| {
                            f(end, format!("ordinal: \"{}\" era: \"{}\"", y_str, era_full), c);
                        });
                    }
                }
            }
        }

        const NAM_PREFIX: [char; 4] = ['n', 'ă', 'm', ' '];
        if start + 4 < text.len() && text[start..start + 4] == NAM_PREFIX {
            let y_start = start + 4;
            let mut y_digits = [0u8; 4];
            let mut len = 0;
            let mut k = y_start;
            while k < text.len() && text[k].is_ascii_digit() && len < 4 {
                y_digits[len] = text[k] as u8;
                len += 1;
                k += 1;
                let mut y_str = String::new();
                if let Some(c) = number(&y_digits[..len], &mut y_str, render) {
                    f(k, format!("year: \"{}\"", y_str), c);

                    if k < text.len() && text[k] == ' ' {
                        let era_start = k + 1;
                        scan_eras(text, era_start, |end, era_full| {
                            f(end, format!("year: \"{}\" era: \"{}\"", y_str, era_full), c);
                        });
                    }
                }
            }
        }

        // 5. Bare numeric dates: D/M/YYYY or YYYY/M/D (with consistent separator "/", "-", ".")
        // Bare numeric dates require exactly 4 digits for year
        if text[start].is_ascii_digit() {
            // Check YYYY/M/D (ISO format)
            if start + 8 <= text.len() && text[start..start + 4].iter().all(|c| c.is_ascii_digit()) {
                let sep = text[start + 4];
                if sep == '/' || sep == '-' || sep == '.' {
                    let y_digits =
                        [text[start] as u8, text[start + 1] as u8, text[start + 2] as u8, text[start + 3] as u8];
                    let mut y_str = String::new();
                    if let Some(c) = number(&y_digits, &mut y_str, render) {
                        let m_start = start + 5;
                        for m_len in 1..=2 {
                            if !text.get(m_start + m_len - 1).is_some_and(char::is_ascii_digit) {
                                break;
                            }
                            if let Some(month_word) = get_month(&text[m_start..m_start + m_len]) {
                                let p2 = m_start + m_len;
                                if p2 < text.len() && text[p2] == sep {
                                    let d_start = p2 + 1;
                                    for d_len in 1..=2 {
                                        if !text.get(d_start + d_len - 1).is_some_and(char::is_ascii_digit) {
                                            break;
                                        }
                                        if let Some(day_word) = get_day(&text[d_start..d_start + d_len]) {
                                            f(
                                                d_start + d_len,
                                                format!(
                                                    "year: \"{}\" month: \"{}\" day: \"{}\"",
                                                    y_str, month_word, day_word
                                                ),
                                                c,
                                            );
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }

            // Check D/M/YYYY
            for d_len in 1..=2 {
                if !text.get(start + d_len - 1).is_some_and(char::is_ascii_digit) {
                    break;
                }
                if let Some(day_word) = get_day(&text[start..start + d_len]) {
                    let p1 = start + d_len;
                    if p1 < text.len() {
                        let sep = text[p1];
                        if sep == '/' || sep == '-' || sep == '.' {
                            let m_start = p1 + 1;
                            for m_len in 1..=2 {
                                if !text.get(m_start + m_len - 1).is_some_and(char::is_ascii_digit) {
                                    break;
                                }
                                if let Some(month_word) = get_month(&text[m_start..m_start + m_len]) {
                                    let p2 = m_start + m_len;
                                    if p2 + 5 <= text.len() && text[p2] == sep {
                                        let y_start = p2 + 1;
                                        if text[y_start..y_start + 4].iter().all(|c| c.is_ascii_digit()) {
                                            let y_digits = [
                                                text[y_start] as u8,
                                                text[y_start + 1] as u8,
                                                text[y_start + 2] as u8,
                                                text[y_start + 3] as u8,
                                            ];
                                            let mut y_str = String::new();
                                            if let Some(c) = number(&y_digits, &mut y_str, render) {
                                                f(
                                                    y_start + 4,
                                                    format!(
                                                        "day: \"{}\" month: \"{}\" year: \"{}\"",
                                                        day_word, month_word, y_str
                                                    ),
                                                    c,
                                                );
                                            }
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }

            // Check bare Y era (e.g. "20 SCN", "500 TCN")
            let mut y_digits = [0u8; 4];
            let mut len = 0;
            let mut k = start;
            while k < text.len() && text[k].is_ascii_digit() && len < 4 {
                y_digits[len] = text[k] as u8;
                len += 1;
                k += 1;
                let mut y_str = String::new();
                if let Some(c) = number(&y_digits[..len], &mut y_str, render) {
                    if k < text.len() && text[k] == ' ' {
                        let era_start = k + 1;
                        scan_eras(text, era_start, |end, era_full| {
                            f(end, format!("text: \"{} {}\"", y_str, era_full), c);
                        });
                    }
                }
            }
        }
    }
}

impl Classifier for Date {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan(text, start, false, |end, _, c| {
            out.push(Candidate { end, cost: WEIGHT_DATE + c, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        Self::scan(text, start, true, |end, body, c| {
            if candidate.tag.is_empty() && end == candidate.end && WEIGHT_DATE + c == candidate.cost {
                candidate.tag = format!("date {{ {} }}", body);
            }
        });
    }
}

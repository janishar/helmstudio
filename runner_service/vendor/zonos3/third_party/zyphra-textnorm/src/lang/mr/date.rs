//! Marathi date, era, year range and decade graphs.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const DAYS: &str = include_str!("../../../data/mr/date/days.tsv");
const MONTHS: &str = include_str!("../../../data/mr/date/months.tsv");
const ABBR: &str = include_str!("../../../data/mr/date/month_abbr.tsv");
const ERAS: &str = include_str!("../../../data/mr/date/year_suffix.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static WORDS: LazyLock<super::address::KeywordIndex> = LazyLock::new(|| {
    let mut out = super::address::KeywordIndex::new();
    for word in
        rows(ABBR).map(|(key, _)| key).chain(rows(MONTHS).map(|(_, value)| value)).chain(rows(ERAS).map(|(key, _)| key))
    {
        out.insert(word);
    }
    out
});

pub(crate) fn possible_start(s: &str) -> bool {
    let Some(first) = s.chars().next() else { return false };
    numbers::digit(first).is_some() || first == '\'' || WORDS.get(first).iter().any(|word| s.starts_with(*word))
}

pub(crate) fn possible_char(c: char) -> bool {
    struct Alphabet {
        ascii: [bool; 128],
        devanagari: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static ALPHABET: LazyLock<Alphabet> = LazyLock::new(|| {
        let mut alphabet = Alphabet { ascii: [false; 128], devanagari: [false; 128], other: Default::default() };
        let words = rows(ABBR)
            .map(|(key, _)| key)
            .chain(rows(MONTHS).map(|(_, value)| value))
            .chain(rows(ERAS).map(|(key, _)| key))
            .chain(["चे दशक", "च्या दशकात", "-/.– ',s"]);
        for c in words.flat_map(str::chars) {
            let n = c as usize;
            if n < 128 {
                alphabet.ascii[n] = true;
            } else if (0x0900..=0x097f).contains(&n) {
                alphabet.devanagari[n - 0x0900] = true;
            } else {
                alphabet.other.insert(c);
            }
        }
        alphabet
    });
    if numbers::digit(c).is_some() {
        return true;
    }
    let n = c as usize;
    if n < 128 {
        ALPHABET.ascii[n]
    } else if (0x0900..=0x097f).contains(&n) {
        ALPHABET.devanagari[n - 0x0900]
    } else {
        ALPHABET.other.contains(&c)
    }
}

pub(crate) fn might_match(s: &str) -> bool {
    if s.ends_with('s') || s.ends_with("चे दशक") || s.ends_with("च्या दशकात") {
        return true;
    }
    let digit_end = s.char_indices().find_map(|(i, c)| numbers::digit(c).is_none().then_some(i)).unwrap_or(s.len());
    if digit_end > 0 {
        let tail = &s[digit_end..];
        let tail = tail.strip_prefix(' ').unwrap_or(tail);
        if tail.starts_with(|c| matches!(c, '-' | '/' | '.' | '–')) {
            return true;
        }
    }
    let tail = if digit_end == 0 { s } else { s[digit_end..].strip_prefix(' ').unwrap_or(&s[digit_end..]) };
    tail.chars().next().is_some_and(|first| WORDS.get(first).iter().any(|word| tail.starts_with(*word)))
}

static DAYS_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(DAYS) {
        out.insert(k, v);
    }
    out
});

static MONTHS_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(MONTHS) {
        out.insert(k, v);
    }
    out
});

static ABBR_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(ABBR) {
        out.insert(k, v);
    }
    out
});

static MONTH_NAMES: LazyLock<std::collections::HashSet<&'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashSet::new();
    for (_, v) in rows(MONTHS) {
        out.insert(v);
    }
    out
});

static ERAS_LIST: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| rows(ERAS).collect());

fn pad_day(s: &str) -> Option<&'static str> {
    if let Some(&v) = DAYS_MAP.get(s) {
        return Some(v);
    }
    if s.len() == 1 {
        let c = s.chars().next()?;
        if ('1'..='9').contains(&c) {
            let padded = format!("0{c}");
            return DAYS_MAP.get(padded.as_str()).copied();
        }
    } else if s.chars().count() == 1 {
        let c = s.chars().next()?;
        if ('१'..='९').contains(&c) {
            let padded = format!("०{c}");
            return DAYS_MAP.get(padded.as_str()).copied();
        }
    }
    None
}

fn pad_month(s: &str) -> Option<&'static str> {
    if let Some(&v) = MONTHS_MAP.get(s) {
        return Some(v);
    }
    if s.len() == 1 {
        let c = s.chars().next()?;
        if ('1'..='9').contains(&c) {
            let padded = format!("0{c}");
            return MONTHS_MAP.get(padded.as_str()).copied();
        }
    } else if s.chars().count() == 1 {
        let c = s.chars().next()?;
        if ('१'..='९').contains(&c) {
            let padded = format!("०{c}");
            return MONTHS_MAP.get(padded.as_str()).copied();
        }
    }
    None
}

fn month_name(s: &str) -> Option<&'static str> {
    if let Some(&v) = ABBR_MAP.get(s) {
        return Some(v);
    }
    MONTH_NAMES.get(s).copied()
}

fn year(s: &str) -> Vec<(String, Cost)> {
    if s.chars().count() != 4 || !matches!(s.chars().next(), Some('1' | '2' | '१' | '२')) {
        return Vec::new();
    }
    numbers::graph(s)
}

fn era_year(s: &str) -> Vec<(String, Cost)> {
    if s.is_empty() || s.chars().count() > 4 || matches!(s.chars().next(), Some('0' | '०')) {
        return Vec::new();
    }
    numbers::graph(s)
}

fn emit(out: &mut Vec<Candidate>, end: usize, w: f64, c: Cost, tag_fn: impl FnOnce() -> String) {
    out.push(Candidate { end, cost: cost(1.1 + w) + c, rank: 4, tag: super::lazy::tag(tag_fn) });
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    classify_numeric(s, end, out);
    classify_named(s, end, out);
    classify_era(s, end, out);
    classify_decade(s, end, out);
    classify_range(s, end, out);
}

fn classify_numeric(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. ISO YYYY-MM-DD
    if let Some((y, rest)) = s.split_once('-') {
        if let Some((m, d)) = rest.split_once('-') {
            if let (Some(m_word), Some(d_word)) = (pad_month(m), pad_day(d)) {
                for (y_word, c) in year(y) {
                    emit(out, end, -0.001, c, || {
                        format!("date {{ year: \"{y_word}\" month: \"{m_word}\" day: \"{d_word}\" }}")
                    });
                }
            }
        }
    }

    // 2. DD-MM-YYYY, DD/MM/YYYY, DD.MM.YYYY
    for sep in ['-', '/', '.'] {
        if let Some((d, rest)) = s.split_once(sep) {
            if let Some((m, y)) = rest.split_once(sep) {
                if let (Some(d_word), Some(m_word)) = (pad_day(d), pad_month(m)) {
                    for (y_word, c) in year(y) {
                        emit(out, end, -0.001, c, || {
                            format!("date {{ day: \"{d_word}\" month: \"{m_word}\" year: \"{y_word}\" }}")
                        });
                    }
                }
            }
        }
    }

    // 3. DD-MM
    if let Some((d, m)) = s.split_once('-') {
        if let (Some(d_word), Some(m_word)) = (pad_day(d), pad_month(m)) {
            emit(out, end, -0.001, 0, || format!("date {{ day: \"{d_word}\" month: \"{m_word}\" }}"));
        }
    }
}

fn classify_named(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let mut era_suffix = None;
    let mut base = s;
    for &(era_key, era_spoken) in ERAS_LIST.iter() {
        if let Some(prefix) = s.strip_suffix(era_key) {
            let p = prefix.strip_suffix(' ').unwrap_or(prefix);
            era_suffix = Some(era_spoken);
            base = p;
            break;
        }
    }

    let parts: Vec<&str> = base.split_whitespace().collect();

    // DD Month[,] YYYY [Era]
    if parts.len() == 3 {
        let d = parts[0];
        let m = parts[1].trim_end_matches(',');
        let y = parts[2];
        if let (Some(d_word), Some(m_word)) = (pad_day(d), month_name(m)) {
            for (y_word, c) in year(y) {
                emit(out, end, -0.003, c, || {
                    let era_str = era_suffix.map(|e| format!(" era: \"{e}\"")).unwrap_or_default();
                    format!("date {{ day: \"{d_word}\" month: \"{m_word}\" year: \"{y_word}\"{era_str} }}")
                });
            }
        }
    }

    // DD Month
    if parts.len() == 2 && era_suffix.is_none() {
        let d = parts[0];
        let m = parts[1];
        if let (Some(d_word), Some(m_word)) = (pad_day(d), month_name(m)) {
            emit(out, end, -0.001, 0, || format!("date {{ day: \"{d_word}\" month: \"{m_word}\" }}"));
        }
    }

    // Month[,] YYYY [Era]
    if parts.len() == 2 {
        let m = parts[0].trim_end_matches(',');
        let y = parts[1];
        if let Some(m_word) = month_name(m) {
            for (y_word, c) in year(y) {
                emit(out, end, -0.2, c, || {
                    let era_str = era_suffix.map(|e| format!(" era: \"{e}\"")).unwrap_or_default();
                    format!("date {{ month: \"{m_word}\" year: \"{y_word}\"{era_str} }}")
                });
            }
        }
    }
}

fn classify_era(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. Year Era, e.g. "1990 इ.स."
    for &(era_key, era_spoken) in ERAS_LIST.iter() {
        if let Some(prefix) = s.strip_suffix(era_key) {
            let y = prefix.trim();
            for (y_word, c) in era_year(y) {
                emit(out, end, -0.005, c, || format!("date {{ year: \"{y_word}\" era: \"{era_spoken}\" }}"));
            }
        }
    }

    // 2. Era Year (preserve_order: true), e.g. "इ.स. 1990"
    for &(era_key, era_spoken) in ERAS_LIST.iter() {
        if let Some(suffix) = s.strip_prefix(era_key) {
            let y = suffix.trim();
            for (y_word, c) in era_year(y) {
                emit(out, end, -0.005, c, || {
                    format!("date {{ era: \"{era_spoken}\" year: \"{y_word}\" preserve_order: true }}")
                });
            }
        }
    }
}

fn classify_range(s: &str, end: usize, out: &mut Vec<Candidate>) {
    for sep in ['–', '-'] {
        if let Some((y1, y2)) = s.split_once(sep) {
            let y1 = y1.trim();
            let y2 = y2.trim();
            for (w1, c1) in year(y1) {
                for (w2, c2) in year(y2) {
                    emit(out, end, -0.005, c1 + c2, || format!("date {{ year: \"{w1} ते {w2}\" }}"));
                }
            }
        }
    }
}

fn classify_decade(s: &str, end: usize, out: &mut Vec<Candidate>) {
    for (suffix, spoken_suffix) in [
        ("'s", "चे दशक"),
        ("s", "चे दशक"),
        (" चे दशक", "चे दशक"),
        ("चे दशक", "चे दशक"),
        (" च्या दशकात", "च्या दशकात"),
        ("च्या दशकात", "च्या दशकात"),
    ] {
        if let Some(prefix) = s.strip_suffix(suffix) {
            let prefix = prefix.strip_prefix('\'').unwrap_or(prefix).trim();
            if prefix.ends_with('0') || prefix.ends_with('०') {
                let count = prefix.chars().count();
                if count == 4 {
                    for (y_word, c) in year(prefix) {
                        emit(out, end, -0.01, c, || format!("date {{ year: \"{y_word}{spoken_suffix}\" }}"));
                    }
                } else if count == 2 {
                    for (y_word, c) in numbers::graph(prefix) {
                        emit(out, end, -0.01, c, || format!("date {{ year: \"{y_word}{spoken_suffix}\" }}"));
                    }
                }
            }
        }
    }
}

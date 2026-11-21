// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Date classifier, ported from ru/taggers/date.py.

use super::number::{Case, Gender, OrdinalForm, cardinal, ordinal};
use crate::lattice::Candidate;
use crate::text::cost;

const MONTH_GENITIVE: [&str; 12] = [
    "января",
    "февраля",
    "марта",
    "апреля",
    "мая",
    "июня",
    "июля",
    "августа",
    "сентября",
    "октября",
    "ноября",
    "декабря",
];

const MONTH_ABBRS: &[(&str, usize)] = &[
    ("янв", 1),
    ("янв.", 1),
    ("января", 1),
    ("фев", 2),
    ("фев.", 2),
    ("февраля", 2),
    ("мар", 3),
    ("мар.", 3),
    ("марта", 3),
    ("апр", 4),
    ("апр.", 4),
    ("апреля", 4),
    ("май", 5),
    ("мая", 5),
    ("июнь", 6),
    ("июня", 6),
    ("июль", 7),
    ("июля", 7),
    ("авг", 8),
    ("авг.", 8),
    ("августа", 8),
    ("сент", 9),
    ("сент.", 9),
    ("сентября", 9),
    ("окт", 10),
    ("окт.", 10),
    ("октября", 10),
    ("нояб", 11),
    ("нояб.", 11),
    ("ноября", 11),
    ("дек", 12),
    ("дек.", 12),
    ("декабря", 12),
];

const YEAR_WORDS: &[(&str, OrdinalForm, &str)] = &[
    ("год", OrdinalForm::MascNom, "год"),
    ("года", OrdinalForm::MascGen, "года"),
    ("году", OrdinalForm::MascPrep, "году"),
    ("годом", OrdinalForm::MascIns, "годом"),
    ("г.", OrdinalForm::MascGen, "года"),
    ("г", OrdinalForm::MascGen, "года"),
];

const ERA_MAP: &[(&str, &str)] = &[
    ("до н.э.", "до нашей эры"),
    ("до н. э.", "до нашей эры"),
    ("н.э.", "нашей эры"),
    ("н. э.", "нашей эры"),
    ("BC", "до нашей эры"),
    ("B.C.", "до нашей эры"),
    ("BCE", "до нашей эры"),
    ("B.C.E.", "до нашей эры"),
    ("AD", "нашей эры"),
    ("A.D.", "нашей эры"),
    ("CE", "нашей эры"),
    ("C.E.", "нашей эры"),
];

// A suffix can match only its final byte; retain empty keys if the frozen
// source ever adds one. This rejects ordinary unit/prose endings in O(1).
static ERA_SUFFIX_ENDS: ([bool; 256], bool) = {
    let mut ends = [false; 256];
    let mut empty = false;
    let mut index = 0;
    while index < ERA_MAP.len() {
        let key = ERA_MAP[index].0.as_bytes();
        if key.is_empty() {
            empty = true;
        } else {
            ends[key[key.len() - 1] as usize] = true;
        }
        index += 1;
    }
    (ends, empty)
};

pub fn render_day(d: u64) -> String {
    ordinal(d, OrdinalForm::NeutNom)
}

fn valid_year(y: &str, word: Option<&str>) -> bool {
    (y.len() == 2 || (y.len() == 4 && !y.starts_with('0')))
        && y.bytes().all(|c| c.is_ascii_digit())
        && word.is_none_or(|w| YEAR_WORDS.iter().any(|(key, _, _)| *key == w))
}

pub fn render_year(y_str: &str, year_word: Option<&str>) -> Option<String> {
    if !(y_str.len() == 2 || (y_str.len() == 4 && !y_str.starts_with('0')))
        || !y_str.bytes().all(|c| c.is_ascii_digit())
    {
        return None;
    }
    if y_str.starts_with('0') && y_str.len() == 2 {
        let second: u64 = y_str[1..].parse().ok()?;
        let (form, w_sp) = if let Some(w) = year_word {
            let (_, f, spoken) = YEAR_WORDS.iter().find(|(word, _, _)| *word == w)?;
            (*f, Some(*spoken))
        } else {
            (OrdinalForm::MascGen, None)
        };
        let ord_sp = ordinal(second, form);
        return Some(match w_sp {
            Some(w) => format!("ноль {ord_sp} {w}"),
            None => format!("ноль {ord_sp}"),
        });
    }

    let y: u64 = y_str.parse().ok()?;
    let (form, w_sp) = if let Some(w) = year_word {
        let (_, f, spoken) = YEAR_WORDS.iter().find(|(word, _, _)| *word == w)?;
        (*f, Some(*spoken))
    } else {
        (OrdinalForm::MascGen, None)
    };
    let ord_sp = ordinal(y, form);
    Some(match w_sp {
        Some(w) => format!("{ord_sp} {w}"),
        None => ord_sp,
    })
}

// Recognition mirrors parse_date's branch order but keeps only the ISO cost
// distinction; all spoken fields are constructed for the selected path.
fn recognize_date(s: &str) -> Option<bool> {
    if s.len() == 10 && s.as_bytes()[4] == b'-' && s.as_bytes()[7] == b'-' {
        let (y, m, d) = (&s[0..4], &s[5..7], &s[8..10]);
        if y.bytes().all(|c| c.is_ascii_digit())
            && m.bytes().all(|c| c.is_ascii_digit())
            && d.bytes().all(|c| c.is_ascii_digit())
        {
            let m: usize = m.parse().ok()?;
            let d: u64 = d.parse().ok()?;
            if (1..=12).contains(&m) && (1..=31).contains(&d) {
                return valid_year(y, None).then_some(true);
            }
        }
    }
    for sep in ['.', '/', '-'] {
        let mut parts = s.split(sep);
        let first = parts.next().unwrap_or("");
        let Some(second) = parts.next() else { continue };
        let third = parts.next();
        if parts.next().is_some() {
            continue;
        }
        if let Some(third) = third {
            if let (Ok(d), Ok(m)) = (first.parse::<u64>(), second.parse::<usize>()) {
                if (1..=31).contains(&d) && (1..=12).contains(&m) {
                    let part = third.trim();
                    let (year, word) = if let Some(idx) = part.find(' ') {
                        (&part[..idx], Some(&part[idx + 1..]))
                    } else if let Some(found) = YEAR_WORDS.iter().find(|(w, _, _)| part.ends_with(w)) {
                        let year = part[..part.len() - found.0.len()].trim_end();
                        if !year.is_empty() { (year, Some(found.0)) } else { (part, None) }
                    } else {
                        (part, None)
                    };
                    if valid_year(year, word) {
                        return Some(false);
                    }
                }
            }
        } else if sep == '.' && second.len() == 2 {
            if let (Ok(d), Ok(m)) = (first.parse::<u64>(), second.parse::<usize>()) {
                if (1..=31).contains(&d) && (1..=12).contains(&m) {
                    return Some(false);
                }
            }
        }
    }
    let mut parts = s.split_whitespace();
    if let (Some(day), Some(month)) = (parts.next(), parts.next()) {
        if let Ok(day) = day.parse::<u64>() {
            if (1..=31).contains(&day) && MONTH_ABBRS.iter().any(|(abbr, _)| month == *abbr) {
                match parts.next() {
                    None => return Some(false),
                    Some(year) if valid_year(year, parts.next()) && parts.next().is_none() => return Some(false),
                    _ => {}
                }
            }
        }
    }
    if ERA_SUFFIX_ENDS.1 || s.as_bytes().last().is_some_and(|&byte| ERA_SUFFIX_ENDS.0[byte as usize]) {
        for &(era, _) in ERA_MAP {
            if let Some(rest) = s.strip_suffix(era) {
                let rest = rest.trim();
                if let Some(year) = rest.strip_suffix("г.").or_else(|| rest.strip_suffix('г')) {
                    let value: u64 = year.trim().parse().ok()?;
                    if (1..=9999).contains(&value) {
                        return Some(false);
                    }
                } else if let Ok(value) = rest.parse::<u64>() {
                    if (1..=9999).contains(&value) {
                        return Some(false);
                    }
                }
            }
        }
    }
    if let Some((year, word)) = s.split_once(' ') {
        if year.len() == 4 && year.bytes().all(|c| c.is_ascii_digit()) && valid_year(year, Some(word)) {
            return Some(false);
        }
    }
    None
}

pub(super) fn date_char(c: char) -> bool {
    c.is_ascii_digit()
        || c.is_whitespace()
        || super::whitelist::is_ru_alpha(c)
        || matches!(c, '+' | '-' | '.' | '/' | 'A' | 'B' | 'C' | 'D' | 'E')
}
pub struct ParsedDate {
    pub day: Option<String>,
    pub month: Option<String>,
    pub year: Option<String>,
    pub is_iso: bool,
}

pub fn parse_date(s: &str) -> Option<ParsedDate> {
    // 1. ISO date: YYYY-MM-DD
    if s.len() == 10 && s.as_bytes()[4] == b'-' && s.as_bytes()[7] == b'-' {
        let y_str = &s[0..4];
        let m_str = &s[5..7];
        let d_str = &s[8..10];
        if y_str.chars().all(|c| c.is_ascii_digit())
            && m_str.chars().all(|c| c.is_ascii_digit())
            && d_str.chars().all(|c| c.is_ascii_digit())
        {
            let m: usize = m_str.parse().ok()?;
            let d: u64 = d_str.parse().ok()?;
            if (1..=12).contains(&m) && (1..=31).contains(&d) {
                let y_sp = render_year(y_str, None)?;
                let m_sp = MONTH_GENITIVE[m - 1].to_string();
                let d_sp = render_day(d);
                return Some(ParsedDate { day: Some(d_sp), month: Some(m_sp), year: Some(y_sp), is_iso: true });
            }
        }
    }

    // 2. Numeric date: day.month.year or day/month/year or day-month-year
    for sep in ['.', '/', '-'] {
        let parts: Vec<&str> = s.split(sep).collect();
        if parts.len() == 3 {
            let d_res = parts[0].parse::<u64>();
            let m_res = parts[1].parse::<usize>();
            if let (Ok(d), Ok(m)) = (d_res, m_res) {
                if (1..=31).contains(&d) && (1..=12).contains(&m) {
                    let y_part = parts[2].trim();
                    let (y_str, yw) = if let Some(idx) = y_part.find(' ') {
                        (&y_part[..idx], Some(&y_part[idx + 1..]))
                    } else if let Some(yw_match) = YEAR_WORDS.iter().find(|(w, _, _)| y_part.ends_with(w)) {
                        let y_num = y_part[..y_part.len() - yw_match.0.len()].trim_end();
                        if !y_num.is_empty() { (y_num, Some(yw_match.0)) } else { (y_part, None) }
                    } else {
                        (y_part, None)
                    };
                    if let Some(y_sp) = render_year(y_str, yw) {
                        return Some(ParsedDate {
                            day: Some(render_day(d)),
                            month: Some(MONTH_GENITIVE[m - 1].to_string()),
                            year: Some(y_sp),
                            is_iso: false,
                        });
                    }
                }
            }
        } else if parts.len() == 2 && sep == '.' {
            // Day.Month: 01.05
            if parts[1].len() == 2 {
                if let (Ok(d), Ok(m)) = (parts[0].parse::<u64>(), parts[1].parse::<usize>()) {
                    if (1..=31).contains(&d) && (1..=12).contains(&m) {
                        return Some(ParsedDate {
                            day: Some(render_day(d)),
                            month: Some(MONTH_GENITIVE[m - 1].to_string()),
                            year: None,
                            is_iso: false,
                        });
                    }
                }
            }
        }
    }

    // 3. Spelled date: 25 декабря 2024, 25 декабря 2024 г., 25 декабря
    let parts: Vec<&str> = s.split_whitespace().collect();
    if (2..=4).contains(&parts.len()) {
        if let Ok(d) = parts[0].parse::<u64>() {
            if (1..=31).contains(&d) {
                if let Some((_, m_idx)) = MONTH_ABBRS.iter().find(|(abbr, _)| parts[1] == *abbr) {
                    let m_sp = MONTH_GENITIVE[m_idx - 1].to_string();
                    if parts.len() == 2 {
                        return Some(ParsedDate {
                            day: Some(render_day(d)),
                            month: Some(m_sp),
                            year: None,
                            is_iso: false,
                        });
                    } else {
                        let y_str = parts[2];
                        let yw = parts.get(3).copied();
                        if let Some(y_sp) = render_year(y_str, yw) {
                            return Some(ParsedDate {
                                day: Some(render_day(d)),
                                month: Some(m_sp),
                                year: Some(y_sp),
                                is_iso: false,
                            });
                        }
                    }
                }
            }
        }
    }

    // 4. Year only / Eras: 1999 году, 1006 г., 500 до н.э., 500 г. до н.э.
    for &(era_s, era_spoken) in ERA_MAP {
        if let Some(rest) = s.strip_suffix(era_s) {
            let rest = rest.trim();
            if let Some(y_part) = rest.strip_suffix("г.").or_else(|| rest.strip_suffix('г')) {
                let y_val: u64 = y_part.trim().parse().ok()?;
                if (1..=9999).contains(&y_val) {
                    let ord_sp = ordinal(y_val, OrdinalForm::MascGen);
                    return Some(ParsedDate {
                        day: None,
                        month: None,
                        year: Some(format!("{ord_sp} года {era_spoken}")),
                        is_iso: false,
                    });
                }
            } else if let Ok(y_val) = rest.parse::<u64>() {
                if (1..=9999).contains(&y_val) {
                    let card_sp = cardinal(y_val, Gender::Masc, Case::Nom);
                    return Some(ParsedDate {
                        day: None,
                        month: None,
                        year: Some(format!("{card_sp} {era_spoken}")),
                        is_iso: false,
                    });
                }
            }
        }
    }

    // Year + year word: 1999 году, 1006 г.
    if let Some((y_str, yw)) = s.split_once(' ') {
        if y_str.len() == 4 && y_str.chars().all(|c| c.is_ascii_digit()) {
            if YEAR_WORDS.iter().any(|(w, _, _)| *w == yw) {
                if let Some(y_sp) = render_year(y_str, Some(yw)) {
                    return Some(ParsedDate { day: None, month: None, year: Some(y_sp), is_iso: false });
                }
            }
        }
    }

    None
}

pub fn render_date_tag(parsed: &ParsedDate) -> String {
    let mut tag = String::from("date { ");
    if parsed.is_iso {
        if let Some(ref y) = parsed.year {
            tag.push_str(&format!("year: \"{y}\" "));
        }
        if let Some(ref m) = parsed.month {
            tag.push_str(&format!("month: \"{m}\" "));
        }
        if let Some(ref d) = parsed.day {
            tag.push_str(&format!("day: \"{d}\" "));
        }
    } else {
        if let Some(ref d) = parsed.day {
            tag.push_str(&format!("day: \"{d}\" "));
        }
        if let Some(ref m) = parsed.month {
            tag.push_str(&format!("month: \"{m}\" "));
        }
        if let Some(ref y) = parsed.year {
            tag.push_str(&format!("year: \"{y}\" "));
        }
        tag.push_str("preserve_order: true");
    }
    tag.push_str(" }");
    tag
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(is_iso) = recognize_date(s) {
        out.push(Candidate {
            end,
            cost: if is_iso { cost(1.09 - 0.01) } else { cost(1.09) },
            rank: if is_iso { 1 } else { 0 },
            tag: String::new(),
        });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(parsed) = parse_date(s) {
        candidate.tag = render_date_tag(&parsed);
    }
}

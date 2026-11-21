//! Arabic date classifier and verbalizer.
//! Matching `ar/taggers/date.py` and `ar/verbalizers/date.py`.

use super::cardinal::{parse_cardinal_span, spell_cardinal};
use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;
use std::sync::LazyLock;

crate::tsv!(MONTHS, "ar/date/months.tsv");

static DECADES: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| {
    let centuries = [
        ("15", "القرن السادس عشر"),
        ("16", "القرن السابع عشر"),
        ("17", "القرن الثامن عشر"),
        ("18", "القرن التاسع عشر"),
        ("19", "القرن العشرين"),
        ("20", "القرن الحادي والعشرين"),
    ];
    let dec_tens = [
        ("20", "عشرينيات"),
        ("30", "ثلاثينيات"),
        ("40", "أربعينيات"),
        ("50", "خمسينيات"),
        ("60", "ستينيات"),
        ("70", "سبعينيات"),
        ("80", "ثمانينيات"),
        ("90", "تسعينيات"),
    ];
    let mut pairs = Vec::new();
    for &(c_num, c_word) in &centuries {
        for &(d_num, d_word) in &dec_tens {
            let key1: &'static str = Box::leak(format!("{c_num}{d_num}s").into_boxed_str());
            let key2: &'static str = Box::leak(format!("{c_num}{d_num}'s").into_boxed_str());
            let val: &'static str = Box::leak(format!("{d_word} {c_word}").into_boxed_str());
            pairs.push((key1, val));
            pairs.push((key2, val));
        }
    }
    let early_decades = [
        ("1800", "عقد ألف وثمان مئة"),
        ("1810", "عقد ألف وثمان مئة وعشرة"),
        ("1900", "عقد ألف وتسع مئة"),
        ("1910", "عقد ألف وتسع مئة وعشرة"),
        ("2000", "عقد الألفين"),
        ("2010", "عقد ألفان وعشرة"),
    ];
    for &(y_num, spoken) in &early_decades {
        let k1: &'static str = Box::leak(format!("{y_num}s").into_boxed_str());
        let k2: &'static str = Box::leak(format!("{y_num}'s").into_boxed_str());
        pairs.push((k1, spoken));
        pairs.push((k2, spoken));
    }
    for &(d_num, d_word) in &dec_tens {
        let k1: &'static str = Box::leak(format!("'{d_num}s").into_boxed_str());
        let k2: &'static str = Box::leak(format!("’{d_num}s").into_boxed_str());
        let val: &'static str = Box::leak(format!("ال{d_word}").into_boxed_str());
        pairs.push((k1, val));
        pairs.push((k2, val));
    }
    pairs
});

pub fn get_month_name(m: u32) -> Option<&'static str> {
    let key = m.to_string();
    MONTHS.rows().iter().find(|r| r.key == key).map(|r| r.value.as_str())
}

pub struct DateClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for DateClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }

        // Branch 1: Decades ("1990s", "2020s", etc.)
        let decade_start = match text.get(start) {
            Some('1' | '2') => {
                text.get(start + 1..start + 4).is_some_and(|digits| digits.iter().all(char::is_ascii_digit))
                    && matches!(text.get(start + 4), Some('s' | '\''))
            }
            Some('\'' | '’') => {
                text.get(start + 1..start + 3).is_some_and(|digits| digits.iter().all(char::is_ascii_digit))
                    && text.get(start + 3) == Some(&'s')
            }
            _ => false,
        };
        if decade_start {
            for &(decade_str, _) in DECADES.iter() {
                let len = decade_str.chars().count();
                if start + len <= text.len() && text[start..start + len].iter().copied().eq(decade_str.chars()) {
                    let end = start + len;
                    if token_end(text, end, self.punct) {
                        out.push(Candidate { end, cost: cost(1.10 - 0.1), rank: 0, tag: String::new() });
                    }
                }
            }
        }
        if !text.get(start).is_some_and(|_| char_at(text, start).is_ascii_digit()) {
            return;
        }

        // Branch 2: Numeric dates (DMY or YMD)
        // DMY: d/m/y, d-m-y, d.m.y
        // YMD: y/m/d, y-m-d
        let d1_start = start;
        let mut d1_end = d1_start;
        while d1_end < text.len() && d1_end - d1_start <= 4 && char_at(text, d1_end).is_ascii_digit() {
            d1_end += 1;
        }
        if d1_end - d1_start > 4 {
            return;
        }
        let d1_len = d1_end - d1_start;
        if d1_len >= 1 && d1_len <= 4 && d1_end < text.len() {
            let sep1 = text[d1_end];
            if sep1 == '/' || sep1 == '-' || sep1 == '.' {
                let d2_start = d1_end + 1;
                let mut d2_end = d2_start;
                while d2_end < text.len() && d2_end - d2_start <= 2 && char_at(text, d2_end).is_ascii_digit() {
                    d2_end += 1;
                }
                let d2_len = d2_end - d2_start;
                if d2_len >= 1 && d2_len <= 2 && d2_end < text.len() && text[d2_end] == sep1 {
                    let d3_start = d2_end + 1;
                    let mut d3_end = d3_start;
                    while d3_end < text.len() && d3_end - d3_start <= 4 && char_at(text, d3_end).is_ascii_digit() {
                        d3_end += 1;
                    }
                    let d3_len = d3_end - d3_start;
                    // Check DMY
                    if d1_len <= 2
                        && d2_len <= 2
                        && d3_len == 4
                        && (char_at(text, d3_start) == '1' || char_at(text, d3_start) == '2')
                    {
                        let day = (d1_start..d1_end).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));
                        let month =
                            (d2_start..d2_end).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));
                        if day >= 1 && day <= 31 && month >= 1 && month <= 12 && token_end(text, d3_end, self.punct) {
                            let year_cost = parse_cardinal_span(text, d3_start, d3_end, false).map_or(0.0, |(_, c)| c);
                            out.push(Candidate {
                                end: d3_end,
                                cost: cost(1.10 + year_cost),
                                rank: 1,
                                tag: String::new(),
                            });
                        }
                    }
                    // Check YMD (only with / or -)
                    if (sep1 == '/' || sep1 == '-')
                        && d1_len == 4
                        && d2_len <= 2
                        && d3_len <= 2
                        && (char_at(text, d1_start) == '1' || char_at(text, d1_start) == '2')
                    {
                        let month =
                            (d2_start..d2_end).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));
                        let day = (d3_start..d3_end).fold(0u32, |n, i| n * 10 + (char_at(text, i) as u32 - '0' as u32));
                        if day >= 1 && day <= 31 && month >= 1 && month <= 12 && token_end(text, d3_end, self.punct) {
                            let year_cost = parse_cardinal_span(text, d1_start, d1_end, false).map_or(0.0, |(_, c)| c);
                            out.push(Candidate {
                                end: d3_end,
                                cost: cost(1.10 + year_cost),
                                rank: 2,
                                tag: String::new(),
                            });
                        }
                    }
                }
            }
        }

        // Branch 3: Year with era:
        // "2020م" or "2020هـ" or "2020 هـ"
        if d1_len == 4 && (char_at(text, d1_start) == '1' || char_at(text, d1_start) == '2') {
            let year_cost = parse_cardinal_span(text, d1_start, d1_end, false).map_or(0.0, |(_, c)| c);
            let era_idx = d1_end;
            if era_idx < text.len() && text[era_idx] == 'م' {
                let end = era_idx + 1;
                if token_end(text, end, self.punct) {
                    out.push(Candidate { end, cost: cost(1.10 - 0.2 + year_cost), rank: 3, tag: String::new() });
                }
            } else if era_idx + 1 < text.len() && text[era_idx] == 'ه' && text[era_idx + 1] == 'ـ' {
                let end = era_idx + 2;
                if token_end(text, end, self.punct) {
                    out.push(Candidate { end, cost: cost(1.10 - 0.2 + year_cost), rank: 4, tag: String::new() });
                }
            } else if era_idx < text.len()
                && text[era_idx] == ' '
                && era_idx + 2 < text.len()
                && text[era_idx + 1] == 'ه'
                && text[era_idx + 2] == 'ـ'
            {
                let end = era_idx + 3;
                if token_end(text, end, self.punct) {
                    out.push(Candidate { end, cost: cost(1.10 - 0.2 + year_cost), rank: 5, tag: String::new() });
                }
            }
        }

        // Branch 4: Historical era (1-4 digit year + BC/BCE/AD/CE)
        if d1_len >= 1 && d1_len <= 4 && char_at(text, d1_start) != '0' && d1_end < text.len() && text[d1_end] == ' ' {
            let year_cost = parse_cardinal_span(text, d1_start, d1_end, false).map_or(0.0, |(_, c)| c);
            let era_bc = ["BC", "B.C.", "BCE", "B.C.E."];
            let era_ad = ["AD", "A.D.", "CE", "C.E."];
            for &e in &era_bc {
                if text[d1_end + 1..].iter().copied().take(e.len()).eq(e.chars()) {
                    let end = d1_end + 1 + e.len();
                    if token_end(text, end, self.punct) {
                        out.push(Candidate { end, cost: cost(1.10 + year_cost), rank: 6, tag: String::new() });
                    }
                }
            }
            for &e in &era_ad {
                if text[d1_end + 1..].iter().copied().take(e.len()).eq(e.chars()) {
                    let end = d1_end + 1 + e.len();
                    if token_end(text, end, self.punct) {
                        out.push(Candidate { end, cost: cost(1.10 + year_cost), rank: 7, tag: String::new() });
                    }
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        // Decade
        if candidate.rank == 0 {
            for &(d_str, spoken) in DECADES.iter() {
                if start + d_str.chars().count() == candidate.end
                    && text[start..candidate.end].iter().copied().eq(d_str.chars())
                {
                    let mut tag = String::with_capacity(32 + spoken.len());
                    tag.push_str("date { text: \"");
                    tag.push_str(spoken);
                    tag.push_str("\" }");
                    candidate.tag = tag;
                    return;
                }
            }
        }

        // DMY
        if candidate.rank == 1 {
            let mut sep1 = start;
            while sep1 < candidate.end && char_at(text, sep1).is_ascii_digit() {
                sep1 += 1;
            }
            let mut sep2 = sep1 + 1;
            while sep2 < candidate.end && char_at(text, sep2).is_ascii_digit() {
                sep2 += 1;
            }
            let day_num: u32 = (start..sep1).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(1);
            let month_num: u32 = (sep1 + 1..sep2).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(1);
            let year_num: u64 =
                (sep2 + 1..candidate.end).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(2000);

            let day_spoken = spell_cardinal(day_num as u64);
            let month_spoken = get_month_name(month_num).unwrap_or("يناير");
            let year_spoken = spell_cardinal(year_num);

            let mut tag = String::with_capacity(64);
            tag.push_str("date { day: \"");
            tag.push_str(&day_spoken);
            tag.push_str("\" month: \"");
            tag.push_str(month_spoken);
            tag.push_str("\" year: \"");
            tag.push_str(&year_spoken);
            tag.push_str("\" }");
            candidate.tag = tag;
            return;
        }

        // YMD
        if candidate.rank == 2 {
            let mut sep1 = start;
            while sep1 < candidate.end && char_at(text, sep1).is_ascii_digit() {
                sep1 += 1;
            }
            let mut sep2 = sep1 + 1;
            while sep2 < candidate.end && char_at(text, sep2).is_ascii_digit() {
                sep2 += 1;
            }
            let year_num: u64 = (start..sep1).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(2000);
            let month_num: u32 = (sep1 + 1..sep2).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(1);
            let day_num: u32 =
                (sep2 + 1..candidate.end).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(1);

            let day_spoken = spell_cardinal(day_num as u64);
            let month_spoken = get_month_name(month_num).unwrap_or("يناير");
            let year_spoken = spell_cardinal(year_num);

            let mut tag = String::with_capacity(64);
            tag.push_str("date { day: \"");
            tag.push_str(&day_spoken);
            tag.push_str("\" month: \"");
            tag.push_str(month_spoken);
            tag.push_str("\" year: \"");
            tag.push_str(&year_spoken);
            tag.push_str("\" }");
            candidate.tag = tag;
            return;
        }

        // Year with era م / هـ
        if matches!(candidate.rank, 3 | 4 | 5) {
            let mut y_end = start;
            while y_end < candidate.end && char_at(text, y_end).is_ascii_digit() {
                y_end += 1;
            }
            let year_num: u64 = (start..y_end).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(2000);
            let year_spoken = spell_cardinal(year_num);
            let era_spoken = if candidate.rank == 3 { "ميلادي" } else { "هجري" };

            let mut tag = String::with_capacity(64);
            tag.push_str("date { year: \"");
            tag.push_str(&year_spoken);
            tag.push_str("\" era: \"");
            tag.push_str(era_spoken);
            tag.push_str("\" }");
            candidate.tag = tag;
            return;
        }

        // Historical era BC / AD
        if candidate.rank == 6 || candidate.rank == 7 {
            let mut y_end = start;
            while y_end < candidate.end && char_at(text, y_end).is_ascii_digit() {
                y_end += 1;
            }
            let year_num: u64 = (start..y_end).map(|i| char_at(text, i)).collect::<String>().parse().unwrap_or(1);
            let year_spoken = spell_cardinal(year_num);
            let era_spoken = if candidate.rank == 6 { "قبل الميلاد" } else { "ميلادي" };

            let mut tag = String::with_capacity(64);
            tag.push_str("date { year: \"");
            tag.push_str(&year_spoken);
            tag.push_str("\" era: \"");
            tag.push_str(era_spoken);
            tag.push_str("\" }");
            candidate.tag = tag;
        }
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    if let Some(text) = f.take("text") {
        if !f.done() || text.is_empty() {
            return None;
        }
        return Some(text.to_string());
    }

    let day = f.take("day");
    let month = f.take("month");
    let year = f.take("year")?;
    let era = f.take("era");
    if !f.done() {
        return None;
    }

    if let (Some(d), Some(m)) = (day, month) {
        if d.is_empty() || m.is_empty() || year.is_empty() {
            return None;
        }
        return Some(format!("{d} {m} {year}"));
    }

    if let Some(e) = era {
        if year.is_empty() || e.is_empty() {
            return None;
        }
        return Some(format!("{year} {e}"));
    }

    None
}

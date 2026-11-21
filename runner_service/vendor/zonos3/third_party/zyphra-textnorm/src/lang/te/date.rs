//! Telugu date classification grammar.

use super::lazy;
use super::numbers::{self, is_digit, parse_digits_u64};
use crate::lattice::Candidate;
use crate::text::cost;

const MONTH_NUMBERS: [(&str, &str); 42] = [
    ("01", "జనవరి"),
    ("1", "జనవరి"),
    ("02", "ఫిబ్రవరి"),
    ("2", "ఫిబ్రవరి"),
    ("03", "మార్చి"),
    ("3", "మార్చి"),
    ("04", "ఏప్రిల్"),
    ("4", "ఏప్రిల్"),
    ("05", "మే"),
    ("5", "మే"),
    ("06", "జూన్"),
    ("6", "జూన్"),
    ("07", "జులై"),
    ("7", "జులై"),
    ("08", "ఆగస్టు"),
    ("8", "ఆగస్టు"),
    ("09", "సెప్టెంబర్"),
    ("9", "సెప్టెంబర్"),
    ("10", "అక్టోబర్"),
    ("11", "నవంబర్"),
    ("12", "డిసెంబర్"),
    // Telugu digits
    ("\u{0c66}\u{0c67}", "జనవరి"),
    ("\u{0c67}", "జనవరి"),
    ("\u{0c66}\u{0c68}", "ఫిబ్రవరి"),
    ("\u{0c68}", "ఫిబ్రవరి"),
    ("\u{0c66}\u{0c69}", "మార్చి"),
    ("\u{0c69}", "మార్చి"),
    ("\u{0c66}\u{0c6a}", "ఏప్రిల్"),
    ("\u{0c6a}", "ఏప్రిల్"),
    ("\u{0c66}\u{0c6b}", "మే"),
    ("\u{0c6b}", "మే"),
    ("\u{0c66}\u{0c6c}", "జూన్"),
    ("\u{0c6c}", "జూన్"),
    ("\u{0c66}\u{0c6d}", "జులై"),
    ("\u{0c6d}", "జులై"),
    ("\u{0c66}\u{0c6e}", "ఆగస్టు"),
    ("\u{0c6e}", "ఆగస్టు"),
    ("\u{0c66}\u{0c6f}", "సెప్టెంబర్"),
    ("\u{0c6f}", "సెప్టెంబర్"),
    ("\u{0c67}\u{0c66}", "అక్టోబర్"),
    ("\u{0c67}\u{0c67}", "నవంబర్"),
    ("\u{0c67}\u{0c68}", "డిసెంబర్"),
];

const MONTH_NAMES: [(&str, &str); 28] = [
    ("జనవరి", "జనవరి"),
    ("జన.", "జనవరి"),
    ("జన", "జనవరి"),
    ("ఫిబ్రవరి", "ఫిబ్రవరి"),
    ("ఫిబ్ర.", "ఫిబ్రవరి"),
    ("ఫిబ్ర", "ఫిబ్రవరి"),
    ("మార్చి", "మార్చి"),
    ("ఏప్రిల్", "ఏప్రిల్"),
    ("ఏప్రి.", "ఏప్రిల్"),
    ("ఏప్రి", "ఏప్రిల్"),
    ("మే", "మే"),
    ("జూన్", "జూన్"),
    ("జులై", "జులై"),
    ("ఆగస్టు", "ఆగస్టు"),
    ("ఆగ.", "ఆగస్టు"),
    ("ఆగ", "ఆగస్టు"),
    ("సెప్టెంబర్", "సెప్టెంబర్"),
    ("సెప్టెం.", "సెప్టెంబర్"),
    ("సెప్టెం", "సెప్టెంబర్"),
    ("అక్టోబర్", "అక్టోబర్"),
    ("అక్టో.", "అక్టోబర్"),
    ("అక్టో", "అక్టోబర్"),
    ("నవంబర్", "నవంబర్"),
    ("నవం.", "నవంబర్"),
    ("నవం", "నవంబర్"),
    ("డిసెంబర్", "డిసెంబర్"),
    ("డిసెం.", "డిసెంబర్"),
    ("డిసెం", "డిసెంబర్"),
];

const YEAR_SUFFIXES: [(&str, &str); 10] = [
    ("క్రీ.శ.", "క్రీస్తు శకం"),
    ("క్రీ.శ", "క్రీస్తు శకం"),
    ("క్రీశ", "క్రీస్తు శకం"),
    ("క్రీ.పూ.", "క్రీస్తు పూర్వం"),
    ("క్రీ.పూ", "క్రీస్తు పూర్వం"),
    ("క్రీపూ", "క్రీస్తు పూర్వం"),
    ("CE", "క్రీస్తు శకం"),
    ("BCE", "క్రీస్తు పూర్వం"),
    ("AD", "క్రీస్తు శకం"),
    ("BC", "క్రీస్తు పూర్వం"),
];

fn parse_day(s: &str) -> Option<u32> {
    if !(1..=2).contains(&s.chars().count()) {
        return None;
    }
    let val = parse_digits_u64(s)?;
    (1..=31).contains(&val).then_some(val as u32)
}

fn parse_month_num(s: &str) -> Option<&'static str> {
    for &(key, name) in &MONTH_NUMBERS {
        if s == key {
            return Some(name);
        }
    }
    None
}

fn parse_year_4(s: &str) -> Option<&str> {
    if s.chars().count() != 4 || !s.chars().all(is_digit) {
        return None;
    }
    let d = numbers::digit_val(s.chars().next()?)?;
    (d == 1 || d == 2).then_some(s)
}

fn parse_year_any(s: &str) -> Option<&str> {
    let count = s.chars().count();
    if (1..=4).contains(&count) && s.chars().all(is_digit) && !numbers::is_zero(s.chars().next()?) {
        Some(s)
    } else {
        None
    }
}

type MonthIndex = std::collections::HashMap<char, Vec<(&'static str, &'static str)>>;
static MONTH_INDEX: std::sync::LazyLock<MonthIndex> = std::sync::LazyLock::new(|| {
    let mut index = MonthIndex::new();
    for &(name, spoken) in &MONTH_NAMES {
        index.entry(name.chars().next().unwrap()).or_default().push((name, spoken));
    }
    index
});
pub fn possible_start(input: &str) -> bool {
    let first = match input.chars().next() {
        Some(c) => c,
        None => return false,
    };
    numbers::is_digit(first)
        || first == '\''
        || MONTH_INDEX.get(&first).is_some_and(|names| names.iter().any(|(name, _)| input.starts_with(name)))
}

pub fn possible_char(c: char) -> bool {
    numbers::is_digit(c)
        || matches!(c, '/' | '-' | '.' | '–' | ' ' | ',' | '\'')
        || ('\u{0c00}'..='\u{0c7f}').contains(&c)
        || c.is_ascii_alphabetic()
}

// Once the initial number is followed by a word, only month names, era
// markers and decade suffixes can continue a date. Keep complete lexical
// matches live because further fields may follow them.
pub fn possible_numeric_prefix(s: &str) -> bool {
    let split = s.char_indices().find(|&(_, c)| !is_digit(c)).map_or(s.len(), |(i, _)| i);
    if split == 0 || split == s.len() {
        return true;
    }
    let rest = &s[split..];
    let rest = rest.strip_prefix(' ').unwrap_or(rest);
    if rest.is_empty() || rest.chars().next().is_some_and(|c| matches!(c, '/' | '-' | '.' | '–' | '\'')) {
        return true;
    }
    let first = rest.chars().next().unwrap();
    MONTH_INDEX
        .get(&first)
        .is_some_and(|names| names.iter().any(|(name, _)| name.starts_with(rest) || rest.starts_with(name)))
        || YEAR_SUFFIXES
            .iter()
            .map(|(name, _)| *name)
            .chain(["s", "ల", "లలో", "ల దశాబ్దం"])
            .any(|name| name.starts_with(rest) || rest.starts_with(name))
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. Numeric DMY: DD/MM/YYYY, DD-MM-YYYY, DD.MM.YYYY
    for sep in ['/', '-', '.'] {
        let mut parts = s.split(sep);
        if let (Some(d_s), Some(m_s), Some(y_s), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let (Some(d), Some(m), Some(y)) = (parse_day(d_s), parse_month_num(m_s), parse_year_4(y_s)) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08),
                    rank: 5,
                    tag: lazy::tag(|| {
                        format!(
                            "date {{ day: \"{}\" month: \"{m}\" year: \"{}\" }}",
                            numbers::spell_up_to_999(d).unwrap(),
                            numbers::cardinal(y).unwrap()
                        )
                    }),
                });
                return;
            }
        }
    }

    // 2. Numeric YMD: YYYY-MM-DD
    if s.contains('-') {
        let mut parts = s.split('-');
        if let (Some(y_s), Some(m_s), Some(d_s), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
            if let (Some(y), Some(m), Some(d)) = (parse_year_4(y_s), parse_month_num(m_s), parse_day(d_s)) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08),
                    rank: 5,
                    tag: lazy::tag(|| {
                        format!(
                            "date {{ year: \"{}\" month: \"{m}\" day: \"{}\" }}",
                            numbers::cardinal(y).unwrap(),
                            numbers::spell_up_to_999(d).unwrap()
                        )
                    }),
                });
                return;
            }
        }
    }

    // 3. Year ranges: "1990–1995", "1990-1995", "1995–2005" (sep: "-", "–", " - ", " – ")
    for sep in [" - ", " – ", "–", "-"] {
        if let Some((y1_s, y2_s)) = s.split_once(sep) {
            if let (Some(y1), Some(y2)) = (parse_year_4(y1_s), parse_year_4(y2_s)) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08) - cost(0.05),
                    rank: 5,
                    tag: lazy::tag(|| {
                        format!(
                            "date {{ year: \"{} నుండి {}\" }}",
                            numbers::cardinal(y1).unwrap(),
                            numbers::cardinal(y2).unwrap()
                        )
                    }),
                });
                return;
            }
        }
    }

    // 4. Decades: "1990s", "1990's", "1990ల", "1990లలో", "1990ల దశాబ్దం"
    let decade_suffixes = ["'s", "s", "ల దశాబ్దం", "లలో", "ల"];
    for suf in decade_suffixes {
        if let Some(y_s) = s.strip_suffix(suf) {
            if y_s.chars().count() == 4 && y_s.chars().all(is_digit) {
                let first = numbers::digit_val(y_s.chars().next().unwrap()).unwrap_or(0);
                let last = numbers::digit_val(y_s.chars().last().unwrap()).unwrap_or(1);
                if (first == 1 || first == 2) && last == 0 {
                    out.push(Candidate {
                        end,
                        cost: cost(1.08) - cost(0.05),
                        rank: 5,
                        tag: lazy::tag(|| {
                            let y_sp = numbers::cardinal(y_s).unwrap();
                            let y_gen = if y_sp.ends_with('ల') { y_sp } else { format!("{y_sp}ల") };
                            let decade_str = match suf {
                                "లలో" => format!("{y_gen}ో"),
                                _ => format!("{y_gen} దశాబ్దం"),
                            };
                            format!("date {{ year: \"{decade_str}\" }}")
                        }),
                    });
                    return;
                }
            }
        }
    }

    // 5. Textual date: Day Month Year [Era], Day Month, Month Year [Era], Year Era
    // Check if input ends with era suffix
    let mut text_without_era = s;
    let mut era_spoken = None;
    for &(suf, spoken) in &YEAR_SUFFIXES {
        if let Some(rest) = s.strip_suffix(suf).and_then(|r| r.strip_suffix(' ')) {
            if !rest.is_empty() {
                text_without_era = rest;
                era_spoken = Some(spoken);
                break;
            }
        }
    }

    let era_field = || era_spoken.map(|e| format!(" era: \"{e}\"")).unwrap_or_default();

    // Case 5a: Year Era (e.g. "1947 క్రీ.శ.")
    if let Some(_e_sp) = era_spoken {
        if let Some(y) = parse_year_any(text_without_era) {
            out.push(Candidate {
                end,
                cost: cost(1.08),
                rank: 5,
                tag: lazy::tag(|| format!("date {{ year: \"{}\"{} }}", numbers::cardinal(y).unwrap(), era_field())),
            });
            return;
        }
    }

    // A valid month starts the input or follows a numeric day and one space.
    let (left, month_text) = match text_without_era.split_once(' ') {
        Some((day, rest)) if parse_day(day).is_some() => (day, rest),
        _ => ("", text_without_era),
    };
    let Some(first) = month_text.chars().next() else { return };
    let Some(names) = MONTH_INDEX.get(&first) else { return };
    for &(m_name, m_spoken) in names {
        if let Some(after) = month_text.strip_prefix(m_name) {
            let after = if let Some(after) = after.strip_prefix(',') {
                if after.is_empty() {
                    continue;
                }
                after
            } else {
                after
            };
            let right = if after.is_empty() {
                ""
            } else if let Some(year) = after.strip_prefix(' ') {
                year
            } else {
                continue;
            };

            let day_opt = if !left.is_empty() { parse_day(left) } else { None };
            let year_opt = if !right.is_empty() { parse_year_any(right) } else { None };

            if let (Some(day), Some(year)) = (day_opt, year_opt) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08),
                    rank: 5,
                    tag: lazy::tag(|| {
                        format!(
                            "date {{ day: \"{}\" month: \"{m_spoken}\" year: \"{}\"{} }}",
                            numbers::spell_up_to_999(day).unwrap(),
                            numbers::cardinal(year).unwrap(),
                            era_field()
                        )
                    }),
                });
                return;
            } else if let Some(day) = day_opt.filter(|_| right.is_empty() && era_spoken.is_none()) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08),
                    rank: 5,
                    tag: lazy::tag(|| {
                        format!("date {{ day: \"{}\" month: \"{m_spoken}\" }}", numbers::spell_up_to_999(day).unwrap())
                    }),
                });
                return;
            } else if let Some(year) = year_opt.filter(|_| left.is_empty()) {
                out.push(Candidate {
                    end,
                    cost: cost(1.08),
                    rank: 5,
                    tag: lazy::tag(|| {
                        format!(
                            "date {{ month: \"{m_spoken}\" year: \"{}\"{} }}",
                            numbers::cardinal(year).unwrap(),
                            era_field()
                        )
                    }),
                });
                return;
            }
        }
    }
}

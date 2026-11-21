//! Telugu Roman numeral classification grammar.

use super::lazy;
use crate::lattice::Candidate;
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

const ROMAN_SPOKEN_DATA: &str = include_str!("../../../data/te/roman/roman_to_spoken.tsv");
const ROMAN_ORDINAL_DATA: &str = include_str!("../../../data/te/roman/roman_to_ordinal.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static ROMAN_TO_SPOKEN: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ROMAN_SPOKEN_DATA).collect());

static ROMAN_TO_ORDINAL: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ROMAN_ORDINAL_DATA).collect());

pub const CARDINAL_KEYS: &[&str] = &[
    "అధ్యాయం",
    "భాగం",
    "చంద్రయాన్",
    "ప్రపంచ యుద్ధం",
    "యుద్ధం",
    "తరగతి",
    "ఖండం",
    "అంకం",
    "సర్గ",
    "సర్గం",
    "విభాగం",
    "సంపుటి",
    "శతాబ్దం",
    "శతాబ్దము",
    "దశ",
    "పాఠం",
    "కాండం",
];

pub fn spoken(roman: &str) -> Option<&'static str> {
    ROMAN_TO_SPOKEN.get(roman).copied()
}

pub fn possible_char(c: char) -> bool {
    matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M' | '-' | ' ') || ('\u{0c00}'..='\u{0c7f}').contains(&c)
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let weight = cost(1.1);

    // 1. Key before numeral: <key><sep><roman>
    for &key in CARDINAL_KEYS {
        if let Some(rest) = s.strip_prefix(key) {
            if let Some(roman) = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')) {
                if ROMAN_TO_SPOKEN.contains_key(roman) {
                    let key_nbsp = key.replace(' ', "\u{a0}");
                    let roman = roman.to_string();
                    out.push(Candidate {
                        end,
                        cost: weight,
                        rank: 12,
                        tag: lazy::tag(move || {
                            format!(
                                "roman {{ preserve_order: true key_cardinal: \"{key_nbsp}\" integer: \"{roman}\" }}"
                            )
                        }),
                    });
                }
            }
        }
    }

    // 2. Numeral before key: <roman><sep><key>
    if let Some((roman, key_part)) = s.split_once(' ').or_else(|| s.split_once('-')) {
        if ROMAN_TO_SPOKEN.contains_key(roman) {
            for &key in CARDINAL_KEYS {
                if key == key_part {
                    let key_nbsp = key.replace(' ', "\u{a0}");
                    let roman = roman.to_string();
                    out.push(Candidate {
                        end,
                        cost: weight,
                        rank: 12,
                        tag: lazy::tag(move || {
                            format!(
                                "roman {{ preserve_order: true integer: \"{roman}\" key_cardinal: \"{key_nbsp}\" }}"
                            )
                        }),
                    });
                }
            }
        }
    }

    // 3. Glued ordinal: <roman>వ with optional <space><key>
    let (ordinal_part, key_opt) = match s.split_once(' ') {
        Some((ord, k)) => (ord, Some(k)),
        None => (s, None),
    };

    if let Some(roman) = ordinal_part.strip_suffix('వ') {
        if let Some(&spoken_ord) = ROMAN_TO_ORDINAL.get(roman) {
            let roman_str = roman.to_string();
            match key_opt {
                None => {
                    out.push(Candidate {
                        end,
                        cost: weight,
                        rank: 12,
                        tag: lazy::tag(move || {
                            format!("roman {{ preserve_order: true integer: \"{roman_str}\" default_ordinal: \"{spoken_ord}\" }}")
                        }),
                    });
                }
                Some(key) => {
                    for &k in CARDINAL_KEYS {
                        if k == key {
                            let key_nbsp = key.replace(' ', "\u{a0}");
                            let roman_str = roman_str.clone();
                            out.push(Candidate {
                                end,
                                cost: weight,
                                rank: 12,
                                tag: lazy::tag(move || {
                                    format!("roman {{ preserve_order: true integer: \"{roman_str}\" default_ordinal: \"{spoken_ord}\" key_cardinal: \"{key_nbsp}\" }}")
                                }),
                            });
                        }
                    }
                }
            }
        }
    }
}

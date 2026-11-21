//! Bengali Roman numeral classifier.
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const ROMAN_SPOKEN_DATA: &str = include_str!("../../../data/bn/roman/roman_to_spoken.tsv");
const ROMAN_ORDINAL_EXCEPTIONS_DATA: &str = include_str!("../../../data/bn/roman/roman_ordinal_exceptions.tsv");
const BENGALI_CHARS_DATA: &str = include_str!("../../../data/bn/serial/chars.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static ROMAN_SPOKEN: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ROMAN_SPOKEN_DATA).collect());

static ROMAN_ORDINAL_EXCEPTIONS: LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(ROMAN_ORDINAL_EXCEPTIONS_DATA).collect());

pub const CARDINAL_KEYS: &[&str] = &[
    "অধ্যায়",
    "পর্ব",
    "খণ্ড",
    "ভাগ",
    "শ্রেণি",
    "শ্রেণী",
    "শতাব্দী",
    "অঙ্ক",
    "দৃশ্য",
    "ধারা",
    "অনুচ্ছেদ",
    "পরিচ্ছেদ",
    "সর্গ",
    "হেনরি",
    "লুই",
    "এডওয়ার্ড",
    "জর্জ",
    "চার্লস",
    "এলিজাবেথ",
    "পোপ",
    "জন পল",
];

static KEYS_BY_FIRST: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = std::collections::HashMap::<char, Vec<&'static str>>::new();
    for &key in CARDINAL_KEYS {
        if let Some(first) = key.chars().next() {
            index.entry(first).or_default().push(key);
        }
    }
    index
});

pub fn possible_start(input: &str, first: char) -> bool {
    matches!(first, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M')
        || KEYS_BY_FIRST.get(&first).is_some_and(|keys| keys.iter().any(|key| input.starts_with(key)))
}

pub fn spoken(roman: &str) -> Option<&'static str> {
    ROMAN_SPOKEN.get(roman).copied()
}

pub fn possible_char(c: char) -> bool {
    matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M' | '-' | ' ') || ('\u{0980}'..='\u{09FF}').contains(&c)
}

fn bengali_phrase(s: &str) -> bool {
    !s.is_empty()
        && s.split([' ', '-']).all(|word| {
            !word.is_empty()
                && word.chars().all(|c| {
                    BENGALI_CHARS_DATA
                        .lines()
                        .any(|line| line.split('\t').next().is_some_and(|key| key.chars().eq(std::iter::once(c))))
                })
        })
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.1);

    // 1. Key before numeral: key + (' ' | '-') + roman
    for &key in CARDINAL_KEYS {
        if let Some(rest) = s.strip_prefix(key) {
            if let Some(roman_part) = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')) {
                if let Some(&_spoken) = ROMAN_SPOKEN.get(roman_part) {
                    let key_tag = key.replace(' ', "\u{a0}");
                    let roman_tag = roman_part.to_owned();
                    out.push(Candidate {
                        end,
                        cost: top_weight,
                        rank: 12,
                        tag: super::lazy::tag(move || {
                            format!(
                                "roman {{ preserve_order: true key_cardinal: \"{key_tag}\" integer: \"{roman_tag}\" }}"
                            )
                        }),
                    });
                    return;
                }
            }
        }
    }

    // 2. Numeral before key: roman + (' ' | '-') + key
    for &key in CARDINAL_KEYS {
        if let Some(prefix) = s.strip_suffix(key) {
            if let Some(roman_part) = prefix.strip_suffix(' ').or_else(|| prefix.strip_suffix('-')) {
                if let Some(&_spoken) = ROMAN_SPOKEN.get(roman_part) {
                    let key_tag = key.replace(' ', "\u{a0}");
                    let roman_tag = roman_part.to_owned();
                    out.push(Candidate {
                        end,
                        cost: top_weight,
                        rank: 12,
                        tag: super::lazy::tag(move || {
                            format!(
                                "roman {{ preserve_order: true integer: \"{roman_tag}\" key_cardinal: \"{key_tag}\" }}"
                            )
                        }),
                    });
                    return;
                }
            }
        }
    }

    // 3. Fused roman ordinal: e.g. "XIIতম শ্রেণি", "IVর্থ শ্রেণি", "XIIতম"
    // Split optional key phrase at end:
    let (ordinal_str, key_phrase_opt) = match s.split_once(' ') {
        Some((ord, key_p)) => (ord, Some(key_p)),
        None => (s, None),
    };
    if key_phrase_opt.is_some_and(|key| !bengali_phrase(key)) {
        return;
    }

    // 3a. Exception fused roman: e.g. IVর্থ
    if let Some(&spoken_word) = ROMAN_ORDINAL_EXCEPTIONS.get(ordinal_str) {
        // Find matched numeral
        let matched_numeral = ROMAN_SPOKEN.keys().filter(|&&r| ordinal_str.starts_with(r)).max_by_key(|r| r.len());
        if let Some(&numeral) = matched_numeral {
            let num_tag = numeral.to_owned();
            let def_ord = spoken_word.to_owned();
            let key_tag = match key_phrase_opt {
                Some(kp) => format!(" key_cardinal: \"{}\"", kp.replace(' ', "\u{a0}")),
                None => String::new(),
            };
            out.push(Candidate {
                end,
                cost: top_weight + cost(-0.1),
                rank: 12,
                tag: super::lazy::tag(move || {
                    format!("roman {{ preserve_order: true integer: \"{num_tag}\" default_ordinal: \"{def_ord}\"{key_tag} }}")
                }),
            });
            return;
        }
    }

    // 3b. Regular fused roman ordinal: e.g. XIIতম
    if let Some(roman_part) = ordinal_str.strip_suffix("তম") {
        if let Some(&spoken_num) = ROMAN_SPOKEN.get(roman_part) {
            let num_tag = roman_part.to_owned();
            let def_ord = format!("{spoken_num}তম");
            let key_tag = match key_phrase_opt {
                Some(kp) => format!(" key_cardinal: \"{}\"", kp.replace(' ', "\u{a0}")),
                None => String::new(),
            };
            out.push(Candidate {
                end,
                cost: top_weight,
                rank: 12,
                tag: super::lazy::tag(move || {
                    format!("roman {{ preserve_order: true integer: \"{num_tag}\" default_ordinal: \"{def_ord}\"{key_tag} }}")
                }),
            });
        }
    }
}

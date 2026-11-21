//! Contextual Roman numerals and glued Marathi ordinals.
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const ROMAN: &str = include_str!("../../../data/mr/roman/roman_to_spoken.tsv");
const EXCEPT: &str = include_str!("../../../data/mr/roman/roman_ordinal_exceptions.tsv");
const SUFFIX: &str = include_str!("../../../data/mr/ordinal/suffixes.tsv");
const VA_NUMBERS: &str = include_str!("../../../data/mr/ordinal/va_numbers.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static ROMAN_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(ROMAN) {
        out.insert(k, v);
    }
    out
});

pub(super) fn spoken(numeral: &str) -> Option<&'static str> {
    ROMAN_MAP.get(numeral).copied()
}

static EXCEPT_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(EXCEPT) {
        out.insert(k, v);
    }
    out
});

static SUFFIXES_LIST: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| SUFFIX.lines().map(|l| l.trim_end_matches('\r')).filter(|s| !s.is_empty()).collect());

static VA_NUMBERS_SET: LazyLock<std::collections::HashSet<usize>> = LazyLock::new(|| {
    let mut out = std::collections::HashSet::new();
    for line in VA_NUMBERS.lines() {
        if let Ok(n) = line.trim_end_matches('\r').parse::<usize>() {
            out.insert(n);
        }
    }
    out
});

static ROMAN_ROWS: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| rows(ROMAN).collect());

pub(crate) fn possible_char(c: char) -> bool {
    matches!(c, 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M' | ' ' | '-') || (0x0900..=0x097f).contains(&(c as u32))
}

fn mr_word(s: &str) -> bool {
    !s.is_empty() && s.chars().all(super::serial::mr)
}

fn phrase(s: &str) -> bool {
    !s.is_empty() && s.split(|c| c == ' ' || c == '-').all(mr_word)
}

fn emit(end: usize, out: &mut Vec<Candidate>, fields: impl FnOnce() -> String, c: f64) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + c),
        rank: 12,
        tag: super::lazy::tag(|| format!("roman {{ preserve_order: true {} }}", fields())),
    });
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. key_before_numeral and numeral_before_key
    for (i, c) in s.char_indices() {
        if c != ' ' && c != '-' {
            continue;
        }
        let a = &s[..i];
        let b = &s[i + c.len_utf8()..];
        if phrase(a) && ROMAN_MAP.contains_key(b) {
            emit(end, out, || format!("key_cardinal: \"{}\" integer: \"{b}\"", a.replace(' ', "\u{a0}")), 0.0);
        }
        if ROMAN_MAP.contains_key(a) && phrase(b) {
            emit(end, out, || format!("integer: \"{a}\" key_cardinal: \"{}\"", b.replace(' ', "\u{a0}")), 0.0);
        }
    }

    // 2. Glued ordinal: "XIIवी" or "IVथी" (with optional following phrase)
    let mut variants = vec![(s, None)];
    for (i, c) in s.char_indices() {
        if c == ' ' && phrase(&s[i + 1..]) {
            variants.push((&s[..i], Some(&s[i + 1..])));
        }
    }

    for (fused, key) in variants {
        for (idx, &(numeral, spoken)) in ROMAN_ROWS.iter().enumerate() {
            let val = idx + 1;
            if let Some(suffix) = fused.strip_prefix(numeral) {
                // Check if exceptional fused form
                if let Some(&exc_spoken) = EXCEPT_MAP.get(fused) {
                    emit(
                        end,
                        out,
                        || {
                            let key_str = key
                                .map(|k| format!(" key_cardinal: \"{}\"", k.replace(' ', "\u{a0}")))
                                .unwrap_or_default();
                            format!("integer: \"{numeral}\" default_ordinal: \"{exc_spoken}\"{key_str}")
                        },
                        -0.1,
                    );
                } else if val > 10 && SUFFIXES_LIST.contains(&suffix) {
                    let ord_spoken = if VA_NUMBERS_SET.contains(&val) {
                        format!("{spoken}{suffix}")
                    } else {
                        format!("{spoken}ा{suffix}")
                    };
                    emit(
                        end,
                        out,
                        || {
                            let key_str = key
                                .map(|k| format!(" key_cardinal: \"{}\"", k.replace(' ', "\u{a0}")))
                                .unwrap_or_default();
                            format!("integer: \"{numeral}\" default_ordinal: \"{ord_spoken}\"{key_str}")
                        },
                        0.0,
                    );
                }
            }
        }
    }
}

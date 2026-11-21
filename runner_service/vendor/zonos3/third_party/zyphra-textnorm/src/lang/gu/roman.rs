//! Contextual Roman numerals and glued Gujarati ordinals.
use crate::lattice::Candidate;
use crate::text::cost;
const ROMAN: &str = include_str!("../../../data/gu/roman/roman_to_spoken.tsv");
const EXCEPT: &str = include_str!("../../../data/gu/roman/roman_ordinal_exceptions.tsv");
const SUFFIX: &str = include_str!("../../../data/gu/ordinal/suffixes.tsv");
const CHARS: &str = include_str!("../../../data/gu/serial/chars.tsv");

pub(super) fn possible_char(c: char) -> bool {
    struct Alphabet {
        ascii: [bool; 128],
        gujarati: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static CHARS_IN: std::sync::LazyLock<Alphabet> = std::sync::LazyLock::new(|| {
        let mut alphabet = Alphabet { ascii: [false; 128], gujarati: [false; 128], other: Default::default() };
        for c in
            [ROMAN, EXCEPT, SUFFIX, CHARS].into_iter().flat_map(rows).flat_map(|(key, _)| key.chars()).chain([' ', '-'])
        {
            let n = c as usize;
            if n < 128 {
                alphabet.ascii[n] = true;
            } else if (0x0a80..=0x0aff).contains(&n) {
                alphabet.gujarati[n - 0x0a80] = true;
            } else {
                alphabet.other.insert(c);
            }
        }
        alphabet
    });
    let n = c as usize;
    if n < 128 {
        CHARS_IN.ascii[n]
    } else if (0x0a80..=0x0aff).contains(&n) {
        CHARS_IN.gujarati[n - 0x0a80]
    } else {
        CHARS_IN.other.contains(&c)
    }
}
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
pub fn spoken(s: &str) -> Option<&'static str> {
    static INDEX: std::sync::LazyLock<std::collections::HashMap<&'static str, &'static str>> =
        std::sync::LazyLock::new(|| {
            let mut index = std::collections::HashMap::new();
            for (key, value) in rows(ROMAN) {
                index.entry(key).or_insert(value);
            }
            index
        });
    INDEX.get(s).copied()
}
fn guword(s: &str) -> bool {
    !s.is_empty() && s.chars().all(super::serial::gu)
}
fn phrase(s: &str) -> bool {
    !s.is_empty() && s.split(|c| c == ' ' || c == '-').all(guword)
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
    for (i, c) in s.char_indices() {
        if c != ' ' && c != '-' {
            continue;
        }
        let a = &s[..i];
        let b = &s[i + c.len_utf8()..];
        if phrase(a) && spoken(b).is_some() {
            emit(end, out, || format!("key_cardinal: \"{}\" integer: \"{b}\"", a.replace(' ', "\u{a0}")), 0.0);
        }
        if spoken(a).is_some() && phrase(b) {
            emit(end, out, || format!("integer: \"{a}\" key_cardinal: \"{}\"", b.replace(' ', "\u{a0}")), 0.0);
        }
    }
    let mut variants = vec![(s, None)];
    for (i, c) in s.char_indices() {
        if c == ' ' && phrase(&s[i + 1..]) {
            variants.push((&s[..i], Some(&s[i + 1..])));
        }
    }
    for (fused, key) in variants {
        for (r, w) in rows(ROMAN) {
            if let Some(suffix) = fused.strip_prefix(r) {
                let exceptional = rows(EXCEPT).find_map(|(k, v)| (k == fused).then_some(v)).filter(|_| {
                    rows(ROMAN).filter(|(n, _)| fused.starts_with(*n)).map(|(n, _)| n.len()).max() == Some(r.len())
                });
                let regular = rows(ROMAN).position(|(k, _)| k == r).is_some_and(|i| i >= 6)
                    && rows(SUFFIX).any(|(k, _)| k == suffix);
                if let Some(v) = exceptional {
                    emit(
                        end,
                        out,
                        || {
                            format!(
                                "integer: \"{r}\" default_ordinal: \"{v}\"{}",
                                key.map(|k| format!(" key_cardinal: \"{}\"", k.replace(' ', "\u{a0}")))
                                    .unwrap_or_default()
                            )
                        },
                        -0.1,
                    );
                } else if regular {
                    emit(
                        end,
                        out,
                        || {
                            format!(
                                "integer: \"{r}\" default_ordinal: \"{w}{suffix}\"{}",
                                key.map(|k| format!(" key_cardinal: \"{}\"", k.replace(' ', "\u{a0}")))
                                    .unwrap_or_default()
                            )
                        },
                        0.0,
                    );
                }
            }
        }
    }
}

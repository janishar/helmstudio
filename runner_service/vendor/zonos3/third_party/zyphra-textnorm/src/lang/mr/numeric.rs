//! Cardinal, decimal, ordinal and fractional classifier branches.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const SUFFIXES: &str = include_str!("../../../data/mr/ordinal/suffixes.tsv");
const EXCEPTIONS: &str = include_str!("../../../data/mr/ordinal/exceptions.tsv");
const VA_NUMBERS: &str = include_str!("../../../data/mr/ordinal/va_numbers.tsv");
const COMMON: &str = include_str!("../../../data/mr/fraction/common_fractions.tsv");
const PAUNE: &str = include_str!("../../../data/mr/whitelist/paune_mappings.tsv");
const QUANTITIES: &str = include_str!("../../../data/mr/numbers/thousands.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static EXCEPTIONS_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(EXCEPTIONS) {
        out.insert(k, v);
    }
    out
});

static COMMON_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(COMMON) {
        out.insert(k, v);
    }
    out
});

static PAUNE_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(PAUNE) {
        out.insert(k, v);
    }
    out
});

static VA_SET: LazyLock<std::collections::HashSet<&'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashSet::new();
    for line in VA_NUMBERS.lines() {
        let trimmed = line.trim_end_matches('\r');
        if !trimmed.is_empty() {
            out.insert(trimmed);
        }
    }
    out
});

static QUANTITIES_LIST: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| rows(QUANTITIES).collect());

static SUFFIXES_LIST: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| SUFFIXES.lines().map(|l| l.trim_end_matches('\r')).filter(|s| !s.is_empty()).collect());

pub(crate) fn structural_char(c: char) -> bool {
    numbers::digit(c).is_some()
        || matches!(
            c,
            '.' | ','
                | '/'
                | '-'
                | '−'
                | '–'
                | '~'
                | '±'
                | ' '
                | '½'
                | '¼'
                | '¾'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅐'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
                | '⅑'
                | '⅒'
        )
}

pub(crate) fn lexical_ending(s: &str) -> bool {
    type Endings = std::collections::HashMap<char, Vec<&'static str>>;
    static ENDINGS: LazyLock<Endings> = LazyLock::new(|| {
        let mut index = Endings::new();
        for key in SUFFIXES_LIST
            .iter()
            .copied()
            .chain(QUANTITIES_LIST.iter().map(|(k, _)| *k))
            .chain(EXCEPTIONS_MAP.keys().copied())
            .chain(COMMON_MAP.keys().copied())
        {
            if let Some(last) = key.chars().last() {
                index.entry(last).or_default().push(key);
            }
        }
        index
    });
    s.chars()
        .last()
        .and_then(|last| ENDINGS.get(&last))
        .is_some_and(|endings| endings.iter().any(|ending| s.ends_with(*ending)))
}
pub(crate) fn last_lexical_end(input: &str, tail_start: usize) -> Option<usize> {
    static SUFFIXES_IDX: LazyLock<super::address::KeywordIndex> = LazyLock::new(|| {
        let mut index = super::address::KeywordIndex::new();
        for key in ["वा", "वी", "वे", "ावा", "ावी", "ावे", "ला", "ली", "ले", "रा", "री", "रे", "था", "थी", "थे"]
        {
            index.insert(key);
        }
        for (key, _) in rows(QUANTITIES) {
            index.insert(key);
        }
        index
    });
    static EXACT: LazyLock<super::address::KeywordIndex> = LazyLock::new(|| {
        let mut index = super::address::KeywordIndex::new();
        for (key, _) in rows(EXCEPTIONS).chain(rows(COMMON)) {
            index.insert(key);
        }
        index
    });
    let tail = &input[tail_start..];
    let suffix_end = tail
        .chars()
        .next()
        .into_iter()
        .flat_map(|c| SUFFIXES_IDX.get(c).iter())
        .filter(|key| tail.starts_with(**key))
        .map(|key| tail_start + key.len())
        .max();
    let exact_end = input
        .chars()
        .next()
        .into_iter()
        .flat_map(|c| EXACT.get(c).iter())
        .filter(|key| input.starts_with(**key))
        .map(|key| key.len())
        .max();
    suffix_end.into_iter().chain(exact_end).max()
}

fn digit(c: char) -> bool {
    numbers::digit(c).is_some()
}

fn grouped(s: &str) -> Option<String> {
    if !s.contains(',') {
        return None;
    }
    let parts: Vec<&str> = s.split(',').collect();
    if parts.len() < 2 || parts.iter().any(|x| x.is_empty() || !x.chars().all(digit)) {
        return None;
    }
    let lens: Vec<usize> = parts.iter().map(|x| x.chars().count()).collect();
    let western = (1..=3).contains(&lens[0]) && lens[1..].iter().all(|&n| n == 3);
    let indian = (1..=2).contains(&lens[0]) && *lens.last()? == 3 && lens[1..lens.len() - 1].iter().all(|&n| n == 2);
    (western || indian).then(|| parts.concat())
}

fn for_each_cardinal(s: &str, leading_zeros: bool, emit: &mut dyn FnMut(String, Cost)) {
    if let Some(n) = grouped(s) {
        numbers::for_each_graph(&n, emit);
        return;
    }
    if s.is_empty() || !s.chars().all(digit) {
        return;
    }
    numbers::for_each_graph(s, emit);
    if leading_zeros && s.chars().next().is_some_and(numbers::is_zero) {
        if let Some(spoken) = numbers::digitwise(s) {
            emit(spoken, cost(0.5));
        }
    }
}

pub fn cardinal(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    for_each_cardinal(s, true, &mut |value, weight| out.push((value, weight)));
    out
}

pub fn cardinal_without_leading_zeros(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    for_each_cardinal(s, false, &mut |value, weight| out.push((value, weight)));
    out
}

fn sign(s: &str) -> (&str, Option<&'static str>) {
    if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (r, Some("true"))
    } else if let Some(r) = s.strip_prefix('~') {
        (r.strip_prefix(' ').unwrap_or(r), Some("सुमारे"))
    } else if let Some(r) = s.strip_prefix('±') {
        (r.strip_prefix(' ').unwrap_or(r), Some("अधिक किंवा उणे"))
    } else {
        (s, None)
    }
}

fn signed(s: Option<&str>) -> String {
    s.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if !s.is_empty() && s.chars().all(digit) {
        numbers::for_each_graph(s, &mut |val, c| {
            out.push(Candidate {
                end,
                cost: cost(1.1) + c,
                rank: 1,
                tag: super::lazy::tag(|| format!("cardinal {{ integer: \"{val}\" }}")),
            });
        });
        if s.chars().next().is_some_and(numbers::is_zero) {
            if let Some(val) = numbers::digitwise(s) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1) + cost(0.5),
                    rank: 1,
                    tag: super::lazy::tag(|| format!("cardinal {{ integer: \"{val}\" }}")),
                });
            }
        }
        return;
    }
    classify_cardinal(s, end, out);
    classify_decimal(s, end, out);
    classify_fraction(s, end, out);
    classify_ordinal(s, end, out);
}

fn classify_cardinal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (rem, sgn) = sign(s);

    // Endash range: "10–20" -> "दहा ते वीस"
    if let Some((a, b)) = rem.split_once('–') {
        let a = a.trim();
        let b = b.trim();
        for_each_cardinal(a, false, &mut |v1, c1| {
            for_each_cardinal(b, false, &mut |v2, c2| {
                out.push(Candidate {
                    end,
                    cost: cost(1.1) + c1 + c2,
                    rank: 1,
                    tag: super::lazy::tag(|| format!("cardinal {{ integer: \"{v1} ते {v2}\" }}")),
                });
            });
        });
        return;
    }

    for_each_cardinal(rem, true, &mut |val, c| {
        out.push(Candidate {
            end,
            cost: cost(1.1) + c,
            rank: 1,
            tag: super::lazy::tag(|| {
                let s = signed(sgn);
                format!("cardinal {{ {s}integer: \"{val}\" }}")
            }),
        });
    });
}

fn classify_decimal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (rem, sgn) = sign(s);

    // Check quantity suffix, e.g. "१ लाख", "१.५ लाख", "1 lakh"
    let mut quantity_splits = Vec::new();
    for &(q, _) in QUANTITIES_LIST.iter() {
        if let Some(prefix) = rem.strip_suffix(q) {
            let num = prefix.strip_suffix(' ').unwrap_or(prefix);
            if !num.is_empty() {
                quantity_splits.push((num, q));
            }
        }
    }

    for (num, q) in quantity_splits {
        if let Some((int_part, frac_part)) = num.split_once('.') {
            if frac_part.chars().all(digit) {
                if let Some(frac_words) = numbers::digitwise(frac_part) {
                    for (int_word, c) in cardinal_without_leading_zeros(int_part) {
                        out.push(Candidate {
                            end,
                            cost: cost(1.1) + c,
                            rank: 2,
                            tag: super::lazy::tag(|| {
                                let s = signed(sgn);
                                format!("decimal {{ {s}integer_part: \"{int_word}\" fractional_part: \"{frac_words}\" quantity: \"{q}\" }}")
                            }),
                        });
                    }
                }
            }
        } else {
            for (int_word, c) in cardinal_without_leading_zeros(num) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1) + c,
                    rank: 2,
                    tag: super::lazy::tag(|| {
                        let s = signed(sgn);
                        format!("decimal {{ {s}integer_part: \"{int_word}\" quantity: \"{q}\" }}")
                    }),
                });
            }
        }
    }

    // Regular decimal: "12.34"
    if let Some((int_part, frac_part)) = rem.split_once('.') {
        if frac_part.chars().all(digit) {
            if let Some(frac_words) = numbers::digitwise(frac_part) {
                for (int_word, c) in cardinal_without_leading_zeros(int_part) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1) + c,
                        rank: 2,
                        tag: super::lazy::tag(|| {
                            let s = signed(sgn);
                            format!("decimal {{ {s}integer_part: \"{int_word}\" fractional_part: \"{frac_words}\" }}")
                        }),
                    });
                }
            }
        }
    }
}

fn unicode_fraction_to_slash(s: &str) -> Option<(&'static str, &'static str)> {
    match s {
        "½" => Some(("1", "2")),
        "¼" => Some(("1", "4")),
        "¾" => Some(("3", "4")),
        "⅓" => Some(("1", "3")),
        "⅔" => Some(("2", "3")),
        "⅕" => Some(("1", "5")),
        "⅖" => Some(("2", "5")),
        "⅗" => Some(("3", "5")),
        "⅘" => Some(("4", "5")),
        "⅙" => Some(("1", "6")),
        "⅚" => Some(("5", "6")),
        "⅐" => Some(("1", "7")),
        "⅛" => Some(("1", "8")),
        "⅜" => Some(("3", "8")),
        "⅝" => Some(("5", "8")),
        "⅞" => Some(("7", "8")),
        "⅑" => Some(("1", "9")),
        "⅒" => Some(("1", "10")),
        _ => None,
    }
}

fn classify_fraction(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (rem, sgn) = sign(s);
    if !rem.chars().any(|c| {
        matches!(
            c,
            '/' | '½'
                | '¼'
                | '¾'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅐'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
                | '⅑'
                | '⅒'
        )
    }) {
        return;
    }

    // 1. Common fractions map (weight -0.3)
    if let Some(&spoken) = COMMON_MAP.get(rem) {
        out.push(Candidate {
            end,
            cost: cost(1.05 - 0.3),
            rank: 3,
            tag: super::lazy::tag(|| {
                let s = signed(sgn);
                format!("fraction {{ {s}morphosyntactic_features: \"{spoken}\" }}")
            }),
        });
    }

    // 2. Dedh / Dhai (weight -0.2): "1 1/2", "1½", "१ १/२", "१½" -> "दीड", "2 1/2" -> "अडीच"
    for (lead, word) in [("1", "दीड"), ("१", "दीड"), ("2", "अडीच"), ("२", "अडीच")] {
        if let Some(tail) = rem.strip_prefix(lead) {
            let tail = tail.strip_prefix(' ').unwrap_or(tail);
            if tail == "1/2" || tail == "१/२" || tail == "½" {
                out.push(Candidate {
                    end,
                    cost: cost(1.05 - 0.2),
                    rank: 3,
                    tag: super::lazy::tag(|| {
                        let s = signed(sgn);
                        format!("fraction {{ {s}morphosyntactic_features: \"{word}\" }}")
                    }),
                });
            }
        }
    }

    // 3. Paune (weight -0.2): "<paune_num> (3/4 | ¾)" -> "पावणे <mapped>"
    let paune_lead = rem.strip_suffix("3/4").or_else(|| rem.strip_suffix("३/४")).or_else(|| rem.strip_suffix('¾'));
    if let Some(lead) = paune_lead {
        let lead = lead.strip_suffix(' ').unwrap_or(lead);
        if let Some(&mapped) = PAUNE_MAP.get(lead) {
            out.push(Candidate {
                end,
                cost: cost(1.05 - 0.2),
                rank: 3,
                tag: super::lazy::tag(|| {
                    let s = signed(sgn);
                    format!("fraction {{ {s}morphosyntactic_features: \"पावणे {mapped}\" }}")
                }),
            });
        }
    }

    // 4. Savva (weight -0.1): "<cardinal> (1/4 | ¼)" -> "सव्वा <cardinal>"
    // 5. Sadhe (weight -0.1): "<cardinal> (1/2 | ½)" -> "साडे <cardinal>"
    for (tail_char_slash, tail_unicode, prefix_word, w) in
        [("1/4", "¼", "सव्वा", -0.1), ("१/४", "¼", "सव्वा", -0.1), ("1/2", "½", "साडे", -0.1), ("१/२", "½", "साडे", -0.1)]
    {
        let candidate_num = if let Some(lead) = rem.strip_suffix(tail_char_slash) {
            Some(lead.strip_suffix(' ').unwrap_or(lead))
        } else if let Some(lead) = rem.strip_suffix(tail_unicode) {
            Some(lead.strip_suffix(' ').unwrap_or(lead))
        } else {
            None
        };
        if let Some(num) = candidate_num {
            for (card_word, c) in cardinal_without_leading_zeros(num) {
                out.push(Candidate {
                    end,
                    cost: cost(1.05 + w) + c,
                    rank: 3,
                    tag: super::lazy::tag(|| {
                        let s = signed(sgn);
                        format!("fraction {{ {s}morphosyntactic_features: \"{prefix_word} {card_word}\" }}")
                    }),
                });
            }
        }
    }

    // 6. General slash fraction: "[integer_part ]numerator/denominator"
    let (int_part, frac_str) =
        if let Some((lead, frac)) = rem.split_once(' ') { (Some(lead), frac) } else { (None, rem) };

    let slash_split = if let Some((num, den)) = frac_str.split_once('/') {
        let num = num.trim();
        let den = den.trim();
        if !num.is_empty() && !den.is_empty() { Some((num, den)) } else { None }
    } else if let Some((num, den)) = unicode_fraction_to_slash(frac_str) {
        Some((num, den))
    } else {
        None
    };

    if let Some((num_str, den_str)) = slash_split {
        for (num_word, c_num) in cardinal_without_leading_zeros(num_str) {
            for (den_word, c_den) in cardinal_without_leading_zeros(den_str) {
                if let Some(int_s) = int_part {
                    for (int_word, c_int) in cardinal_without_leading_zeros(int_s) {
                        out.push(Candidate {
                            end,
                            cost: cost(1.05) + c_int + c_num + c_den,
                            rank: 3,
                            tag: super::lazy::tag(|| {
                                let s = signed(sgn);
                                format!("fraction {{ {s}integer_part: \"{int_word}\" numerator: \"{num_word}\" denominator: \"{den_word}\" }}")
                            }),
                        });
                    }
                } else {
                    out.push(Candidate {
                        end,
                        cost: cost(1.05) + c_num + c_den,
                        rank: 3,
                        tag: super::lazy::tag(|| {
                            let s = signed(sgn);
                            format!("fraction {{ {s}numerator: \"{num_word}\" denominator: \"{den_word}\" }}")
                        }),
                    });
                }
            }
        }
    }
}

fn classify_ordinal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. Exceptions (weight -0.1)
    if let Some(&spoken) = EXCEPTIONS_MAP.get(s) {
        out.push(Candidate {
            end,
            cost: cost(1.1 - 0.1),
            rank: 9,
            tag: super::lazy::tag(|| format!("ordinal {{ integer: \"{spoken}\" }}")),
        });
        return;
    }

    // 2. Regular ordinals with suffixes: "वा", "वी", "वे"
    for &suffix in SUFFIXES_LIST.iter() {
        if let Some(num_str) = s.strip_suffix(suffix) {
            if num_str.is_empty() {
                continue;
            }
            // Check if ASCII or Devanagari representation of number
            let is_va = VA_SET.contains(num_str);
            for (card_word, c) in cardinal_without_leading_zeros(num_str) {
                let ord_word = super::lazy::tag(|| {
                    if is_va { format!("{card_word}{suffix}") } else { format!("{card_word}ा{suffix}") }
                });
                out.push(Candidate {
                    end,
                    cost: cost(1.1) + c,
                    rank: 9,
                    tag: super::lazy::tag(|| format!("ordinal {{ integer: \"{ord_word}\" }}")),
                });
            }
        }
    }
}

//! Cardinal, decimal, ordinal and fractional classifier branches.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

const SUFFIXES: &str = include_str!("../../../data/hi/ordinal/suffixes.tsv");
const SUFFIXES_MAP: &str = include_str!("../../../data/hi/ordinal/suffixes_map.tsv");
const EXCEPTIONS: &str = include_str!("../../../data/hi/ordinal/exceptions.tsv");
const COMMON: &str = include_str!("../../../data/hi/fraction/common_fractions.tsv");
const PAUNE: &str = include_str!("../../../data/hi/whitelist/paune_mappings.tsv");
const QUANTITIES: &str = include_str!("../../../data/hi/numbers/thousands.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static EXCEPTION_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(EXCEPTIONS).collect());

static COMMON_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(COMMON).collect());

static PAUNE_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(PAUNE).collect());

static QUANTITIES_ROWS: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| rows(QUANTITIES).collect());
static ORDINAL_SUFFIXES: LazyLock<HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(&'static str, &'static str)>> = HashMap::new();
    for (suffix, target) in SUFFIXES
        .lines()
        .map(|s| s.trim_end_matches('\r'))
        .filter(|s| !s.is_empty())
        .map(|s| (s, s))
        .chain(rows(SUFFIXES_MAP))
    {
        if let Some(last) = suffix.chars().next_back() {
            index.entry(last).or_default().push((suffix, target));
        }
    }
    index
});
static NUMERIC_ENDS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
    rows(COMMON)
        .map(|(key, _)| key)
        .chain(rows(EXCEPTIONS).map(|(key, _)| key))
        .chain(SUFFIXES.lines().map(|s| s.trim_end_matches('\r')).filter(|s| !s.is_empty()))
        .chain(rows(SUFFIXES_MAP).map(|(key, _)| key))
        .filter_map(|key| key.chars().next_back())
        .chain("½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".chars())
        .collect()
});
pub(crate) fn possible_end(s: &str) -> bool {
    s.chars().next_back().is_some_and(|c| numbers::is_digit(c) || NUMERIC_ENDS.contains(&c))
}
static ORDINAL_STARTS: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    let suffixes = SUFFIXES
        .lines()
        .map(|s| s.trim_end_matches('\r'))
        .filter(|s| !s.is_empty())
        .chain(rows(SUFFIXES_MAP).map(|(key, _)| key));
    let exceptions = rows(EXCEPTIONS)
        .filter_map(|(key, _)| key.char_indices().find(|&(_, c)| !structural_char(c)).map(|(i, _)| &key[i..]));
    super::trie::Trie::from_keys(suffixes.chain(exceptions))
});
pub(crate) fn numeric_prefix(s: &str) -> bool {
    match s.char_indices().find(|&(_, c)| !structural_char(c)) {
        Some((i, _)) => ORDINAL_STARTS.has_prefix(&s[i..]),
        None => true,
    }
}

pub(crate) fn structural_char(c: char) -> bool {
    numbers::is_digit(c)
        || matches!(
            c,
            '.' | ','
                | '/'
                | '-'
                | '−'
                | '–'
                | '~'
                | '≈'
                | '±'
                | '§'
                | '©'
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

fn sign(s: &str) -> (&str, Option<&'static str>) {
    if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (x, Some("true"))
    } else if let Some(x) = s.strip_prefix('~').or_else(|| s.strip_prefix('≈')) {
        (x.strip_prefix(' ').unwrap_or(x), Some("approx"))
    } else if let Some(x) = s.strip_prefix('±') {
        (x.strip_prefix(' ').unwrap_or(x), Some("plus_minus"))
    } else if let Some(x) = s.strip_prefix('§') {
        (x.strip_prefix(' ').unwrap_or(x), Some("section"))
    } else if let Some(x) = s.strip_prefix('©') {
        (x.strip_prefix(' ').unwrap_or(x), Some("copyright"))
    } else {
        (s, None)
    }
}

fn signed(neg: Option<&'static str>) -> String {
    neg.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
}

fn push(
    out: &mut Vec<Candidate>,
    end: usize,
    base_cost: f64,
    extra_cost: Cost,
    rank: i32,
    class: &str,
    fields: impl FnOnce() -> String,
) {
    out.push(Candidate {
        end,
        cost: cost(base_cost) + extra_cost,
        rank,
        tag: super::lazy::tag(|| format!("{class} {{ {} }}", fields())),
    });
}

fn clean_separators(s: &str) -> Option<String> {
    if !s.contains(',') {
        return None;
    }
    let mut parts = s.split(',').peekable();
    let first = parts.next()?;
    let first_len = first.chars().count();
    let mut indian = (1..=2).contains(&first_len);
    let mut western = (1..=3).contains(&first_len);
    if !first.chars().all(numbers::is_digit) {
        return None;
    }
    while let Some(part) = parts.next() {
        if !part.chars().all(numbers::is_digit) {
            return None;
        }
        let len = part.chars().count();
        indian &= len == if parts.peek().is_some() { 2 } else { 3 };
        western &= len == 3;
    }
    if indian || western {
        let mut clean = String::with_capacity(s.len());
        for part in s.split(',') {
            clean.push_str(part);
        }
        Some(clean)
    } else {
        None
    }
}

pub fn cardinal(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    if let Some(clean) = clean_separators(s) {
        numbers::for_each_graph(&clean, &mut |v, c| out.push((v, c)));
    } else if s.chars().all(numbers::is_digit) {
        numbers::for_each_graph(s, &mut |v, c| out.push((v, c)));
        if s.starts_with(numbers::zero) {
            if let Some(value) = numbers::digitwise(s) {
                out.push((value, cost(0.5)));
            }
        }
    }
    out
}

/// Render the first cardinal witness with the selected lattice cost.
pub(crate) fn materialize_cardinal(s: &str, candidate: &mut Candidate) {
    let (unsigned, neg) = sign(s);
    if unsigned.is_empty() {
        return;
    }
    let clean = clean_separators(unsigned);
    let input = clean.as_deref().unwrap_or(unsigned);
    if !input.chars().all(numbers::is_digit) {
        return;
    }
    numbers::for_each_recipe(input, &mut |parts, weight| {
        if !candidate.tag.is_empty() || cost(1.1) + weight != candidate.cost {
            return;
        }
        let capacity = parts.iter().map(|part| part.len()).sum::<usize>() + parts.len() + 48;
        let mut tag = cardinal_tag_start(neg, capacity);
        for (i, part) in parts.iter().enumerate() {
            if i != 0 {
                tag.push(' ');
            }
            tag.push_str(part);
        }
        tag.push_str("\" }");
        candidate.tag = tag;
    });
    if clean.is_none()
        && candidate.tag.is_empty()
        && unsigned.starts_with(numbers::zero)
        && candidate.cost == cost(1.1) + cost(0.5)
    {
        let mut tag = cardinal_tag_start(neg, unsigned.len() * 5 + 48);
        for (i, c) in unsigned.chars().enumerate() {
            if i != 0 {
                tag.push(' ');
            }
            tag.push_str(numbers::digit(c).unwrap());
        }
        tag.push_str("\" }");
        candidate.tag = tag;
    }
}
fn cardinal_tag_start(neg: Option<&str>, capacity: usize) -> String {
    let mut tag = String::with_capacity(capacity);
    tag.push_str("cardinal { ");
    if let Some(negative) = neg {
        tag.push_str("negative: \"");
        tag.push_str(negative);
        tag.push_str("\" ");
    }
    tag.push_str("integer: \"");
    tag
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (unsigned, neg) = sign(s);

    // 1. Standalone cardinal
    if !unsigned.is_empty() {
        if let Some(clean) = clean_separators(unsigned) {
            numbers::for_each_graph(&clean, &mut |n, c| {
                push(out, end, 1.1, c, 1, "cardinal", || format!("{}integer: \"{n}\"", signed(neg)));
            });
        } else if unsigned.chars().all(numbers::is_digit) {
            numbers::for_each_graph(unsigned, &mut |n, c| {
                push(out, end, 1.1, c, 1, "cardinal", || format!("{}integer: \"{n}\"", signed(neg)));
            });
            if unsigned.starts_with(numbers::zero) {
                if let Some(n) = numbers::digitwise(unsigned) {
                    push(out, end, 1.1, cost(0.5), 1, "cardinal", || format!("{}integer: \"{n}\"", signed(neg)));
                }
            }
        }
    }
    // ASCII digit strings have no fraction, ordinal, or quantity-table paths.
    if !unsigned.is_empty() && unsigned.bytes().all(|c| c.is_ascii_digit()) {
        return;
    }

    // 2. Decimal
    let dec_neg = match neg {
        Some("true") | Some("approx") | Some("plus_minus") => neg,
        None => None,
        _ => None, // § and © do not apply to decimal
    };

    if neg.is_none() || dec_neg.is_some() {
        // Version numbers (3 segments, e.g. 1.2.3)
        let mut segments = unsigned.split('.');
        let first = segments.next().unwrap_or("");
        if let (Some(second), Some(third), None) = (segments.next(), segments.next(), segments.next()) {
            if [first, second, third]
                .iter()
                .all(|p| !p.is_empty() && p.chars().all(numbers::is_digit) && p.chars().count() <= 3)
            {
                numbers::for_each_graph(first, &mut |s1, c1| {
                    numbers::for_each_graph(second, &mut |s2, c2| {
                        numbers::for_each_graph(third, &mut |s3, c3| {
                            push(out, end, 1.1, cost(0.05) + c1 + c2 + c3, 2, "decimal", || {
                                format!(
                                    "{}integer_part: \"{s1}\" fractional_part: \"{s2} दशमलव {s3}\"",
                                    signed(dec_neg)
                                )
                            });
                        });
                    });
                });
            }
        }

        if let Some((int_str, frac_str)) = unsigned.split_once('.') {
            if let Some(f) = numbers::digitwise(frac_str) {
                numbers::for_each_graph(int_str, &mut |v, c| {
                    push(out, end, 1.1, c, 2, "decimal", || {
                        format!("{}integer_part: \"{v}\" fractional_part: \"{f}\"", signed(dec_neg))
                    });
                });
            }
            for &(key, word) in QUANTITIES_ROWS.iter() {
                if let Some(prefix) = frac_str.strip_suffix(key) {
                    if let Some(f) = numbers::digitwise(prefix) {
                        numbers::for_each_graph(int_str, &mut |v, c| {
                            push(out, end, 1.1, c, 2, "decimal", || {
                                format!(
                                    "{}integer_part: \"{v}\" fractional_part: \"{f}\" quantity: \"{word}\"",
                                    signed(dec_neg)
                                )
                            });
                        });
                    }
                }
            }
        } else {
            for &(key, word) in QUANTITIES_ROWS.iter() {
                if let Some(prefix) = unsigned.strip_suffix(key) {
                    numbers::for_each_graph(prefix, &mut |v, c| {
                        push(out, end, 1.1, c, 2, "decimal", || {
                            format!("{}integer_part: \"{v}\" quantity: \"{word}\"", signed(dec_neg))
                        });
                    });
                }
            }
        }
    }

    // 3. Fraction
    let frac_neg = dec_neg;
    if neg.is_none() || frac_neg.is_some() {
        // Common fraction table lookup
        if let Some(v) = COMMON_INDEX.get(unsigned) {
            push(out, end, 1.05, cost(-0.3), 3, "fraction", || {
                format!("{}morphosyntactic_features: \"{v}\"", signed(frac_neg))
            });
        }

        // Dedh / Dhai
        match unsigned {
            "१ १/२" | "१½" | "1½" | "१ ½" | "1 ½" => {
                push(out, end, 1.05, cost(-0.2), 3, "fraction", || {
                    format!("{}morphosyntactic_features: \"डेढ़\"", signed(frac_neg))
                });
            }
            "२ १/२" | "२½" | "2½" | "२ ½" | "2 ½" => {
                push(out, end, 1.05, cost(-0.2), 3, "fraction", || {
                    format!("{}morphosyntactic_features: \"ढाई\"", signed(frac_neg))
                });
            }
            _ => {}
        }

        // Paune (quarter less: e.g. 1 ¾ or 1¾ or 1 ३/४)
        let paune_match = if let Some(p) = unsigned.strip_suffix(" ३/४") {
            Some(p)
        } else if let Some(p) = unsigned.strip_suffix(" ¾").or_else(|| unsigned.strip_suffix('¾')) {
            Some(p)
        } else {
            None
        };
        if let Some(p) = paune_match {
            let p_clean = p;
            // Lookup in PAUNE_INDEX (which maps Devanagari digits to word)
            // Convert any ASCII digits to Devanagari first if needed
            let deva_p: String =
                p_clean
                    .chars()
                    .map(|c| {
                        if c.is_ascii_digit() {
                            char::from_u32('०' as u32 + c as u32 - '0' as u32).unwrap()
                        } else {
                            c
                        }
                    })
                    .collect();
            let valid_script = if unsigned.ends_with(" ३/४") {
                p_clean.chars().all(|c| ('०'..='९').contains(&c))
            } else {
                p_clean.chars().all(|c| c.is_ascii_digit()) || p_clean.chars().all(|c| ('०'..='९').contains(&c))
            };
            if let Some(target) = valid_script.then(|| PAUNE_INDEX.get(deva_p.as_str())).flatten() {
                push(out, end, 1.05, cost(-0.2), 3, "fraction", || {
                    format!("{}morphosyntactic_features: \"पौने {target}\"", signed(frac_neg))
                });
            }
        }

        // Savva (quarter more: e.g. 2 ¼ or 2¼ or 2 १/४)
        let savva_match = if let Some(p) = unsigned.strip_suffix(" १/४") {
            Some(p)
        } else if let Some(p) = unsigned.strip_suffix(" ¼").or_else(|| unsigned.strip_suffix('¼')) {
            Some(p)
        } else {
            None
        };
        if let Some(p) = savva_match {
            let p_clean = p;
            for (card_word, c) in cardinal(p_clean) {
                push(out, end, 1.05, c + cost(-0.1), 3, "fraction", || {
                    format!("{}morphosyntactic_features: \"सवा {card_word}\"", signed(frac_neg))
                });
            }
        }

        // Sadhe (half more: e.g. 3 ½ or 3½ or 3 १/२)
        let sadhe_match = if let Some(p) = unsigned.strip_suffix(" १/२") {
            Some(p)
        } else if let Some(p) = unsigned.strip_suffix(" ½").or_else(|| unsigned.strip_suffix('½')) {
            Some(p)
        } else {
            None
        };
        if let Some(p) = sadhe_match {
            let p_clean = p;
            for (card_word, c) in cardinal(p_clean) {
                push(out, end, 1.05, c + cost(-0.1), 3, "fraction", || {
                    format!("{}morphosyntactic_features: \"साढ़े {card_word}\"", signed(frac_neg))
                });
            }
        }

        // Standard fraction with slash: e.g. "4/6" or "23 4/6" or "4 / 6"
        let slash_split = if let Some((int_num, den)) = unsigned.split_once(" / ") {
            Some((int_num, den))
        } else {
            unsigned.split_once('/')
        };

        if let Some((left, den)) = slash_split {
            if !left.is_empty() && !den.is_empty() {
                let den_cards = cardinal(den);
                if !den_cards.is_empty() {
                    if let Some((int_str, num)) = left.rsplit_once(' ') {
                        if !int_str.is_empty() && !num.is_empty() {
                            let int_cards = cardinal(int_str);
                            let num_cards = cardinal(num);
                            for (iv, ic) in &int_cards {
                                for (nv, nc) in &num_cards {
                                    for (dv, dc) in &den_cards {
                                        push(out, end, 1.05, ic + nc + dc, 3, "fraction", || {
                                            format!(
                                                "{}integer_part: \"{iv}\" numerator: \"{nv}\" denominator: \"{dv}\"",
                                                signed(frac_neg)
                                            )
                                        });
                                    }
                                }
                            }
                        }
                    } else {
                        let num_cards = cardinal(left);
                        for (nv, nc) in &num_cards {
                            for (dv, dc) in &den_cards {
                                push(out, end, 1.05, nc + dc, 3, "fraction", || {
                                    format!("{}numerator: \"{nv}\" denominator: \"{dv}\"", signed(frac_neg))
                                });
                            }
                        }
                    }
                }
            }
        }

        // Unicode fraction with integer, e.g. "2⅓" or "2 ⅓"
        const UNICODE_FRACTIONS: &[(char, &str, &str)] = &[
            ('⅓', "एक", "तीन"),
            ('⅔', "दो", "तीन"),
            ('⅕', "एक", "पाँच"),
            ('⅖', "दो", "पाँच"),
            ('⅗', "तीन", "पाँच"),
            ('⅘', "चार", "पाँच"),
            ('⅙', "एक", "छह"),
            ('⅚', "पाँच", "छह"),
            ('⅛', "एक", "आठ"),
            ('⅜', "तीन", "आठ"),
            ('⅝', "पाँच", "आठ"),
            ('⅞', "सात", "आठ"),
            ('⅐', "एक", "सात"),
            ('⅑', "एक", "नौ"),
            ('⅒', "एक", "दस"),
        ];

        for &(u_char, u_num, u_den) in UNICODE_FRACTIONS {
            if let Some(prefix) = unsigned.strip_suffix(u_char) {
                let p = prefix.strip_suffix(' ').unwrap_or(prefix);
                if !p.is_empty() {
                    for (iv, ic) in cardinal(p) {
                        push(out, end, 1.05, ic, 3, "fraction", || {
                            format!(
                                "{}integer_part: \"{iv}\" numerator: \"{u_num}\" denominator: \"{u_den}\"",
                                signed(frac_neg)
                            )
                        });
                    }
                }
            }
        }
    }

    // 4. Ordinal
    if neg.is_none() {
        if let Some(v) = EXCEPTION_INDEX.get(s) {
            push(out, end, 1.1, cost(-0.1), 9, "ordinal", || format!("integer: \"{v}\""));
        }

        // Check regular suffix or mapped suffix
        for &(suffix, target_suffix) in
            s.chars().next_back().and_then(|c| ORDINAL_SUFFIXES.get(&c)).into_iter().flatten()
        {
            if let Some(n) = s.strip_suffix(suffix) {
                let n_clean = n;
                // Limited to 0-99999 (cardinal up to ten thousands)
                let length = n_clean.chars().count();
                if (1..=5).contains(&length) && n_clean.chars().all(numbers::is_digit) {
                    for (spoken, c) in numbers::graph(n_clean) {
                        push(out, end, 1.1, c, 9, "ordinal", || format!("integer: \"{spoken}{target_suffix}\""));
                    }
                }
            }
        }
    }
}

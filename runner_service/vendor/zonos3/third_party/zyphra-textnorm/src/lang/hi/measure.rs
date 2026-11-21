//! Measure and Address classifier for Hindi.
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

const UNIT_DATA: &str = include_str!("../../../data/hi/measure/unit.tsv");
const UNIT_YEAR_FORMAL_DATA: &str = include_str!("../../../data/hi/measure/unit_year_formal.tsv");
const QUARTERLY_MAP_DATA: &str = include_str!("../../../data/hi/measure/quarterly_units_map.tsv");
const QUARTERLY_LIST_DATA: &str = include_str!("../../../data/hi/measure/quarterly_units_list.tsv");

const STATES_DATA: &str = include_str!("../../../data/hi/address/states.tsv");
const CITIES_DATA: &str = include_str!("../../../data/hi/address/cities.tsv");
const CONTEXT_DATA: &str = include_str!("../../../data/hi/address/context.tsv");
const SPECIAL_CHARS_DATA: &str = include_str!("../../../data/hi/address/special_characters.tsv");
const LETTERS_DATA: &str = include_str!("../../../data/hi/address/letters.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

fn lines_list(data: &'static str) -> impl Iterator<Item = &'static str> {
    data.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty())
}

static UNIT_MAP: LazyLock<Vec<(&'static str, &'static str)>> = LazyLock::new(|| rows(UNIT_DATA).collect());
static UNIT_YEAR_FORMAL_MAP: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| rows(UNIT_YEAR_FORMAL_DATA).collect());
static QUARTERLY_MAP: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| rows(QUARTERLY_MAP_DATA).collect());
static QUARTERLY_LIST: LazyLock<Vec<&'static str>> = LazyLock::new(|| lines_list(QUARTERLY_LIST_DATA).collect());
static UNITS_BY_END: LazyLock<HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(&'static str, &'static str)>> = HashMap::new();
    for &(key, value) in UNIT_MAP.iter() {
        if let Some(last) = key.chars().next_back() {
            index.entry(last).or_default().push((key, value));
        }
    }
    index
});
static UNIT_KEYS_BY_END: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for key in UNIT_MAP
        .iter()
        .map(|(key, _)| *key)
        .chain(UNIT_YEAR_FORMAL_MAP.iter().map(|(key, _)| *key))
        .chain(QUARTERLY_MAP.iter().map(|(key, _)| *key))
        .chain(QUARTERLY_LIST.iter().copied())
    {
        if let Some(last) = key.chars().next_back() {
            index.entry(last).or_default().push(key);
        }
    }
    index
});
static UNIT_PREFIXES: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    super::trie::Trie::from_keys(
        UNIT_MAP
            .iter()
            .map(|(key, _)| *key)
            .chain(UNIT_YEAR_FORMAL_MAP.iter().map(|(key, _)| *key))
            .chain(QUARTERLY_MAP.iter().map(|(key, _)| *key))
            .chain(QUARTERLY_LIST.iter().copied()),
    )
});
pub(crate) fn numeric_prefix(s: &str) -> bool {
    match s.char_indices().find(|&(_, c)| {
        !super::numbers::is_digit(c)
            && !crate::text::is_nemo_space(c)
            && !matches!(c, '.' | '-' | '−' | '–' | '—' | '~' | '≈' | '±' | 'x' | 'X' | '*')
    }) {
        Some((i, _)) => UNIT_PREFIXES.has_prefix(&s[i..]),
        None => true,
    }
}

static STATES_LIST: LazyLock<Vec<&'static str>> = LazyLock::new(|| lines_list(STATES_DATA).collect());
static CITIES_LIST: LazyLock<Vec<&'static str>> = LazyLock::new(|| lines_list(CITIES_DATA).collect());
static CONTEXT_LIST: LazyLock<Vec<&'static str>> = LazyLock::new(|| lines_list(CONTEXT_DATA).collect());
static ADDRESS_TRIE: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    super::trie::Trie::from_keys(STATES_LIST.iter().chain(CITIES_LIST.iter()).chain(CONTEXT_LIST.iter()).copied())
});
static SPECIAL_CHARS_MAP: LazyLock<HashMap<&'static str, &'static str>> =
    LazyLock::new(|| rows(SPECIAL_CHARS_DATA).collect());
static LETTERS_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(LETTERS_DATA).collect());
pub(crate) fn last_address_start(s: &str) -> Option<usize> {
    ADDRESS_TRIE.last_start(s)
}
pub(crate) fn might_match(s: &str) -> bool {
    let numeric_start =
        s.chars().next().is_some_and(|c| super::numbers::is_digit(c) || matches!(c, '.' | '-' | '−' | '~' | '≈' | '±'));
    if numeric_start
        && (s.contains(['x', 'X', '*'])
            || s.chars()
                .next_back()
                .and_then(|c| UNIT_KEYS_BY_END.get(&c))
                .is_some_and(|keys| keys.iter().any(|key| s.ends_with(*key))))
    {
        return true;
    }
    ADDRESS_TRIE.contains_in(s)
}

fn parse_negative(s: &str) -> (Option<&'static str>, &str) {
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (Some("\"true\""), rest)
    } else if let Some(rest) = s.strip_prefix('~').or_else(|| s.strip_prefix('≈')) {
        (Some("\"approx\""), rest.trim_start_matches(crate::text::is_nemo_space))
    } else if let Some(rest) = s.strip_prefix('±') {
        (Some("\"plus_minus\""), rest.trim_start_matches(crate::text::is_nemo_space))
    } else {
        (None, s)
    }
}

fn glued_decade(s: &str) -> bool {
    let Some(raw) = s.strip_suffix('s') else { return false };
    let bare = raw.strip_prefix('\'').unwrap_or(raw);
    let chars: Vec<char> = bare.chars().collect();
    (chars.len() == 2 && chars.iter().all(|&c| super::numbers::is_digit(c)) && super::numbers::zero(chars[1]))
        || (raw == bare
            && chars.len() == 4
            && matches!(chars[0], '1' | '2' | '१' | '२')
            && chars.iter().all(|&c| super::numbers::is_digit(c))
            && super::numbers::zero(chars[3]))
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. Address matching
    if ADDRESS_TRIE.contains_in(s) {
        classify_address(s, end, out);
    }

    // graph_exceptions closes this token and opens a second cardinal token.
    for (position, c) in s.char_indices().filter(|(_, c)| matches!(*c, 'x' | 'X' | '*')) {
        let left = &s[..position];
        let right = &s[position + c.len_utf8()..];
        let (left_sign, left) = parse_negative(left);
        let (right_sign, right) = parse_negative(right);
        let lsign = super::lazy::tag(|| left_sign.map(|v| format!("negative: {v} ")).unwrap_or_default());
        let rsign = super::lazy::tag(|| right_sign.map(|v| format!("negative: {v} ")).unwrap_or_default());
        for (l, lc) in limited_number(left) {
            for (r, rc) in limited_number(right) {
                out.push(Candidate { end, cost: cost(1.1 + 0.1) + lc + rc, rank: 6,
                    tag: super::lazy::tag(|| format!("measure {{ cardinal {{ {lsign}integer: \"{l}\" }} units: \"बाई\" }} }} tokens {{ cardinal {{ {rsign}integer: \"{r}\" }}")) });
            }
        }
    }

    // 3. Units measurement
    let (neg, s_no_neg) = parse_negative(s);
    let neg_tag = super::lazy::tag(|| neg.map(|n| format!("negative: {n} ")).unwrap_or_default());

    for &(unit_sym, unit_name) in s_no_neg.chars().next_back().and_then(|c| UNITS_BY_END.get(&c)).into_iter().flatten()
    {
        if unit_sym == "yr" || (unit_sym == "s" && glued_decade(s)) {
            continue;
        }
        if let Some(num_str) = strip_unit_suffix(s_no_neg, unit_sym) {
            emit_measure_candidates(out, end, num_str, unit_name, &neg_tag, false);
        }
    }
    for &(symbol, name) in UNIT_YEAR_FORMAL_MAP.iter() {
        if let Some(num_str) = strip_unit_suffix(s_no_neg, symbol) {
            emit_measure_candidates(out, end, num_str, name, &neg_tag, true);
        }
    }
    for (symbol, unit) in QUARTERLY_MAP.iter().copied().chain(QUARTERLY_LIST.iter().map(|&v| (v, v))) {
        if let Some(number) = strip_unit_suffix(s_no_neg, symbol) {
            classify_quarter(number, end, unit, &neg_tag, out);
        }
    }
}

fn strip_unit_suffix<'a>(s: &'a str, unit: &str) -> Option<&'a str> {
    if let Some(rest) = s.strip_suffix(unit) {
        let trimmed = rest.trim_end_matches(crate::text::is_nemo_space);
        if !trimmed.is_empty() {
            return Some(trimmed);
        }
    }
    None
}

fn limited_number(s: &str) -> Vec<(String, Cost)> {
    if s.chars().count() > 7 { Vec::new() } else { super::numbers::graph(s) }
}

fn range_number(s: &str) -> Vec<(String, Cost)> {
    if let Some((integer, fractional)) = s.split_once('.') {
        let Some(frac) = super::numbers::digitwise(fractional) else { return Vec::new() };
        limited_number(integer)
            .into_iter()
            .map(|(v, c)| (super::lazy::tag(|| format!("{v} दशमलव {frac}")), c))
            .collect()
    } else {
        limited_number(s)
    }
}

fn emit_measure_candidates(
    out: &mut Vec<Candidate>,
    end: usize,
    num_str: &str,
    unit_name: &str,
    neg_tag: &str,
    year: bool,
) {
    if !year {
        for dash in ['-', '–', '—'] {
            if let Some((left, right)) = num_str.split_once(dash) {
                let left = left.strip_suffix(' ').unwrap_or(left);
                let right = right.strip_prefix(' ').unwrap_or(right);
                for (l, lc) in range_number(left) {
                    for (r, rc) in range_number(right) {
                        out.push(Candidate { end, cost: cost(1.1 - 0.05) + lc + rc, rank: 6,
                            tag: super::lazy::tag(|| format!("measure {{ cardinal {{ {neg_tag}integer: \"{l} से {r}\" }} units: \"{unit_name}\" }}")) });
                    }
                }
            }
        }
    }
    if let Some((integer, fractional)) = num_str.split_once('.') {
        if let Some(frac) = super::numbers::digitwise(fractional) {
            for (value, weight) in limited_number(integer) {
                out.push(Candidate { end, cost: cost(1.1 + 0.1) + weight, rank: 6,
                    tag: super::lazy::tag(|| format!("measure {{ decimal {{ {neg_tag}integer_part: \"{value}\" fractional_part: \"{frac}\" }} units: \"{unit_name}\" }}")) });
            }
        }
    } else {
        let values = if year { super::numbers::graph(num_str) } else { limited_number(num_str) };
        for (value, weight) in values {
            let (unit, extra) = if year && num_str.chars().count() <= 3 {
                ("साल", -0.1)
            } else if year {
                (unit_name, 0.1)
            } else {
                (unit_name, -0.05)
            };
            out.push(Candidate {
                end,
                cost: cost(1.1 + extra) + weight,
                rank: 6,
                tag: super::lazy::tag(|| {
                    format!("measure {{ cardinal {{ {neg_tag}integer: \"{value}\" }} units: \"{unit}\" }}")
                }),
            });
            if year && num_str.chars().count() <= 3 {
                out.push(Candidate {
                    end,
                    cost: cost(1.1 + 0.1) + weight,
                    rank: 6,
                    tag: super::lazy::tag(|| {
                        format!("measure {{ cardinal {{ {neg_tag}integer: \"{value}\" }} units: \"{unit_name}\" }}")
                    }),
                });
            }
        }
    }
}

fn classify_quarter(s: &str, end: usize, unit: &str, neg_tag: &str, out: &mut Vec<Candidate>) {
    let mut emit = |value: &str, weight: Cost| {
        out.push(Candidate {
            end,
            cost: cost(1.1) + weight,
            rank: 6,
            tag: super::lazy::tag(|| {
                format!("measure {{ cardinal {{ {neg_tag}integer: \"{value}\" }} units: \"{unit}\" }}")
            }),
        });
    };
    for (input, value) in [("1.5", "डेढ़"), ("१.५", "डेढ़"), ("2.5", "ढाई"), ("२.५", "ढाई")]
    {
        if s == input {
            emit(value, cost(-0.2));
        }
    }
    for (suffix, word, weight) in [(".25", "सवा", -0.1), (".२५", "सवा", -0.1), (".5", "साढ़े", -0.1), (".५", "साढ़े", -0.1)]
    {
        if let Some(number) = s.strip_suffix(suffix) {
            for (spoken, c) in limited_number(number) {
                emit(&super::lazy::tag(|| format!("{word} {spoken}")), cost(weight) + c);
            }
        }
    }
    for suffix in [".75", ".७५"] {
        if let Some(number) = s.strip_suffix(suffix) {
            if let Some(value) = rows(include_str!("../../../data/hi/whitelist/paune_mappings.tsv"))
                .find_map(|(k, v)| (k == number).then_some(v))
            {
                emit(&super::lazy::tag(|| format!("पौने {value}")), cost(-0.5));
            }
        }
    }
}

fn classify_address(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // Structured graph: optional initial number, up to five word/number tokens,
    // then a state or city and an optional six-digit pincode.
    for name in STATES_LIST.iter().chain(CITIES_LIST.iter()) {
        for (position, _) in s.match_indices(*name) {
            let prefix = &s[..position];
            let suffix = &s[position + name.len()..];
            let pin = if suffix.is_empty() {
                Some(None)
            } else if let Some(p) = suffix.strip_prefix(' ') {
                if p.chars().count() == 6 { super::numbers::digitwise(p).map(Some) } else { None }
            } else {
                None
            };
            let Some(pin) = pin else { continue };
            let mut paths = vec![(String::new(), 0)];
            let mut count = 0;
            let mut first_number = false;
            for piece in prefix.split_inclusive(' ') {
                let Some(raw) = piece.strip_suffix(' ') else {
                    paths.clear();
                    break;
                };
                let word = raw.strip_suffix(',').unwrap_or(raw);
                if word.is_empty() {
                    paths.clear();
                    break;
                }
                count += 1;
                let numeric = word.chars().all(super::numbers::is_digit);
                if count == 1 {
                    first_number = numeric;
                }
                if count > 5 + usize::from(first_number) {
                    paths.clear();
                    break;
                }
                let alternatives = if numeric {
                    super::numbers::code_num(word)
                } else if word.chars().all(|c| {
                    !super::numbers::is_digit(c) && !matches!(c, ',' | '.' | '।') && !crate::text::is_nemo_space(c)
                }) {
                    vec![(super::lazy::tag(|| word.to_owned()), 0)]
                } else {
                    Vec::new()
                };
                let mut next = Vec::new();
                for (old, old_cost) in &paths {
                    for (value, weight) in &alternatives {
                        next.push((
                            super::lazy::tag(|| format!("{old}{value}{} ", if raw.ends_with(',') { "," } else { "" })),
                            old_cost + weight,
                        ));
                    }
                }
                paths = next;
            }
            for (prefix_value, weight) in paths {
                out.push(Candidate { end, cost: cost(2.1) + weight, rank: 6,
                    tag: super::lazy::tag(|| {
                        let suffix = pin.as_ref().map(|p| format!(" {p}")).unwrap_or_default();
                        format!("measure {{ units: \"address\" cardinal {{ integer: \"{prefix_value}{name}{suffix}\" }} preserve_order: true }}")
                    }) });
            }
        }
    }

    if address_context(s) {
        for (value, weight) in address_paths(s) {
            out.push(Candidate {
                end,
                cost: cost(1.1) + weight,
                rank: 6,
                tag: super::lazy::tag(|| {
                    format!("measure {{ units: \"address\" cardinal {{ integer: \"{value}\" }} preserve_order: true }}")
                }),
            });
        }
    }
}

fn address_boundary(c: char) -> bool {
    crate::text::is_nemo_space(c) || matches!(c, ',' | '.' | '।')
}

fn address_window(s: &str) -> bool {
    if s.is_empty() {
        return true;
    }
    if s.chars().next().is_some_and(address_boundary) {
        return false;
    }
    s.split(address_boundary).filter(|part| !part.is_empty()).count() <= 5
}

fn address_context(s: &str) -> bool {
    CONTEXT_LIST.iter().any(|keyword| {
        s.match_indices(*keyword).any(|(pos, _)| {
            let before = &s[..pos];
            let after = &s[pos + keyword.len()..];
            let first = before.is_empty()
                && after.chars().next().is_some_and(address_boundary)
                && address_window(after.trim_start_matches(address_boundary));
            let second = before.chars().next_back().is_some_and(address_boundary)
                && address_window(before.trim_end_matches(address_boundary))
                && (after.is_empty()
                    || (after.chars().next().is_some_and(address_boundary)
                        && address_window(after.trim_start_matches(address_boundary))));
            first || second
        })
    })
}

fn address_paths(s: &str) -> Vec<(String, Cost)> {
    let mut paths: Vec<Vec<(String, Cost)>> = (0..=s.len()).map(|_| Vec::new()).collect();
    paths[0].push((String::new(), 0));
    for (i, c) in s.char_indices() {
        if paths[i].is_empty() {
            continue;
        }
        let mut edges: Vec<(usize, String, Cost)> = Vec::new();
        if c == ' ' {
            edges.push((i + 1, super::lazy::tag(|| " ".to_owned()), 0));
        }
        let mut j = i;
        while let Some(ch) = s[j..].chars().next() {
            if crate::text::is_nemo_space(ch) {
                j += ch.len_utf8();
            } else {
                break;
            }
        }
        if s[j..].starts_with(',') {
            edges.push((j + 1, super::lazy::tag(|| ",".to_owned()), 0));
        }
        if matches!(c, '-' | '/') {
            let key = c.to_string();
            if let Some(&v) = SPECIAL_CHARS_MAP.get(key.as_str()) {
                edges.push((i + c.len_utf8(), super::lazy::tag(|| format!(" {v}")), 0));
            }
        }
        if c.is_ascii_alphabetic() {
            let key = c.to_ascii_lowercase().to_string();
            if let Some(&v) = LETTERS_MAP.get(key.as_str()) {
                edges.push((i + c.len_utf8(), super::lazy::tag(|| format!(" {v}")), 0));
            }
            for (offset, ch) in s[i..].char_indices() {
                if !ch.is_ascii_alphabetic() {
                    break;
                }
                let end = i + offset + ch.len_utf8();
                if end - i >= 2 {
                    edges.push((end, super::lazy::tag(|| format!(" {}", &s[i..end])), 0));
                }
            }
        }
        if super::numbers::is_digit(c) {
            for (offset, ch) in s[i..].char_indices() {
                if !super::numbers::is_digit(ch) {
                    break;
                }
                let end = i + offset + ch.len_utf8();
                for (v, w) in super::numbers::code_num(&s[i..end]) {
                    edges.push((end, super::lazy::tag(|| format!(" {v}")), cost(0.5) + w));
                }
            }
            for (offset, ch) in s[i..].char_indices() {
                let end = i + offset + ch.len_utf8();
                let mut candidates = Vec::new();
                super::numeric::classify(&s[i..end], end, &mut candidates);
                for candidate in candidates.into_iter().filter(|x| x.rank == 9) {
                    let value = if super::lazy::building() {
                        candidate
                            .tag
                            .strip_prefix("ordinal { integer: \"")
                            .and_then(|v| v.strip_suffix("\" }"))
                            .expect("ordinal tag emitted by Hindi classifier")
                            .to_owned()
                    } else {
                        String::new()
                    };
                    edges.push((
                        end,
                        super::lazy::tag(|| format!(" {value}")),
                        candidate.cost - cost(1.1) + cost(-0.0001),
                    ));
                }
            }
        }
        if c.is_ascii_alphanumeric() || super::numbers::is_digit(c) || ('\u{0900}'..='\u{097f}').contains(&c) {
            for (offset, ch) in s[i..].char_indices() {
                let end = i + offset + ch.len_utf8();
                for (value, weight) in super::serial::mixed(&s[i..end]) {
                    edges.push((end, super::lazy::tag(|| format!(" {value}")), weight));
                }
            }
        }
        if !crate::text::is_nemo_space(c)
            && !super::numbers::is_digit(c)
            && !c.is_ascii_alphabetic()
            && !matches!(c, '-' | '/' | ',')
        {
            for (offset, ch) in s[i..].char_indices() {
                if crate::text::is_nemo_space(ch)
                    || super::numbers::is_digit(ch)
                    || ch.is_ascii_alphabetic()
                    || matches!(ch, '-' | '/' | ',')
                {
                    break;
                }
                let end = i + offset + ch.len_utf8();
                edges.push((end, super::lazy::tag(|| format!(" {}", &s[i..end])), cost(0.1)));
            }
        }
        let current = std::mem::take(&mut paths[i]);
        for (end, value, weight) in edges {
            for (prefix, old_weight) in &current {
                let new_weight = old_weight + weight;
                if paths[end].first().is_some_and(|(_, best)| new_weight > *best) {
                    continue;
                }
                if paths[end].first().is_some_and(|(_, best)| new_weight < *best) {
                    paths[end].clear();
                }
                paths[end].push((super::lazy::tag(|| format!("{prefix}{value}")), new_weight));
            }
        }
    }
    std::mem::take(&mut paths[s.len()])
}

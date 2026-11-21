//! Telugu measured quantities, units, prefixed units, ranges and fractions.
use super::{lazy, numbers, numeric};
use crate::lattice::Candidate;
use crate::text::{Cost, Table, cost};
use crate::tsv;
use std::collections::HashMap;
use std::sync::LazyLock;

tsv!(UNIT_ONE, "te/measure/unit_one.tsv");
tsv!(UNIT_OTHER, "te/measure/unit_other.tsv");
tsv!(UNIT_PREFIX, "te/measure/unit_prefix.tsv");
tsv!(UNIT_PREFIXED_ONE, "te/measure/unit_prefixed_one.tsv");
tsv!(UNIT_PREFIXED_OTHER, "te/measure/unit_prefixed_other.tsv");

static INPUT_CHARS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
    UNIT_ONE
        .rows()
        .iter()
        .chain(UNIT_OTHER.rows())
        .chain(UNIT_PREFIXED_ONE.rows())
        .chain(UNIT_PREFIXED_OTHER.rows())
        .flat_map(|row| row.key.chars())
        .collect()
});

pub fn possible_char(c: char) -> bool {
    numbers::is_digit(c)
        || matches!(c, '-' | '–' | '.' | ',' | ' ' | '½' | '¼' | '¾' | '~' | '±')
        || INPUT_CHARS.contains(&c)
}

static UNIT_STARTS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for row in UNIT_ONE
        .rows()
        .iter()
        .chain(UNIT_OTHER.rows())
        .chain(UNIT_PREFIXED_ONE.rows())
        .chain(UNIT_PREFIXED_OTHER.rows())
    {
        if let Some(first) = row.key.chars().next() {
            index.entry(first).or_default().push(row.key.as_str());
        }
    }
    index
});

// Every measure ends at a unit key, preceded by a number expression.
pub fn last_possible_end(input: &str) -> Option<usize> {
    let mut last = None;
    for (pos, c) in input.char_indices() {
        if let Some(keys) = UNIT_STARTS.get(&c) {
            for &key in keys {
                if input[pos..].starts_with(key) {
                    last = Some(last.map_or(pos + key.len(), |n: usize| n.max(pos + key.len())));
                }
            }
        }
        if !numbers::is_digit(c) && !matches!(c, '-' | '–' | '.' | ',' | ' ' | '½' | '¼' | '¾') {
            break;
        }
    }
    last
}

type SuffixIndex = HashMap<char, Vec<&'static crate::text::Row>>;

fn suffix_index(table: &'static Table) -> SuffixIndex {
    let mut index: SuffixIndex = HashMap::new();
    for row in table.rows() {
        if let Some(last) = row.key.chars().next_back() {
            index.entry(last).or_default().push(row);
        }
    }
    index
}

static ONE_INDEX: LazyLock<SuffixIndex> = LazyLock::new(|| suffix_index(&UNIT_ONE));
static OTHER_INDEX: LazyLock<SuffixIndex> = LazyLock::new(|| suffix_index(&UNIT_OTHER));
static PREFIXED_ONE_INDEX: LazyLock<SuffixIndex> = LazyLock::new(|| suffix_index(&UNIT_PREFIXED_ONE));
static PREFIXED_OTHER_INDEX: LazyLock<SuffixIndex> = LazyLock::new(|| suffix_index(&UNIT_PREFIXED_OTHER));

fn range_number(s: &str, final_endpoint: bool) -> Option<(String, Cost)> {
    let (integer, fraction) = s.split_once('.').map_or((s, None), |(a, b)| (a, Some(b)));
    if !(1..=5).contains(&integer.chars().count()) || !integer.chars().all(numbers::is_digit) {
        return None;
    }
    let (mut word, weight) = numeric::fraction_cardinal(integer)?;
    if let Some(fraction) = fraction {
        if !(1..=3).contains(&fraction.chars().count()) {
            return None;
        }
        word.push_str(" దశాంశం ");
        word.push_str(&numbers::digitwise(fraction)?);
    } else if final_endpoint {
        word = numbers::to_attributive(&word);
    }
    Some((word, weight))
}

fn numbers(s: &str) -> Vec<(bool, Cost, String)> {
    let mut result = Vec::new();
    let (raw, sign) = if let Some(rest) = s.strip_prefix('-') { (rest, "negative: \"true\" ") } else { (s, "") };
    if let Some((word, weight)) = numeric::fraction_cardinal(raw) {
        let one = raw == "1" || raw == "౧";
        let word = numbers::to_attributive(&word);
        result.push((one, weight, format!("cardinal {{ {sign}integer: \"{word}\" }}")));
    }
    if let Some((integer, fraction)) = raw.split_once('.') {
        if let (Some(integer), Some(fraction)) = (numbers::cardinal(integer), numbers::digitwise(fraction)) {
            result.push((
                false,
                0,
                format!("decimal {{ {sign}integer_part: \"{integer}\" fractional_part: \"{fraction}\" }}"),
            ));
        }
    }
    if let Some(word) = match raw {
        "½" => Some("సగం"),
        "¼" => Some("పావు"),
        "¾" => Some("ముప్పావు"),
        _ => None,
    } {
        result.push((true, 0, format!("cardinal {{ {sign}integer: \"{word}\" }}")));
    }
    if let Some(integer) = raw.strip_suffix('½') {
        let integer = integer.strip_suffix(' ').unwrap_or(integer);
        if (1..=4).contains(&integer.chars().count()) && integer.chars().all(numbers::is_digit) {
            if let Some((word, weight)) = numeric::fraction_cardinal(integer) {
                if word.ends_with('ు') || word.ends_with('ి') {
                    result.push((false, weight, format!("cardinal {{ {sign}integer: \"{word}న్నర\" }}")));
                }
            }
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some((a, wa)), Some((b, wb))) = (range_number(a, false), range_number(b, true)) {
                result.push((false, wa + wb, format!("cardinal {{ integer: \"{a} నుండి {b}\" }}")));
            }
        }
    }
    result
}

fn decade(s: &str) -> bool {
    let mut c = s.chars();
    let (Some(first), Some(a), Some(b), Some(last), None) = (c.next(), c.next(), c.next(), c.next(), c.next()) else {
        return false;
    };
    matches!(numbers::digit_val(first), Some(1 | 2))
        && numbers::is_digit(a)
        && numbers::is_digit(b)
        && numbers::is_zero(last)
}

fn collect(s: &str, end: usize, out: &mut Vec<Candidate>, index: &SuffixIndex, one: bool, prefixed: bool) {
    let Some(rows) = s.chars().next_back().and_then(|last| index.get(&last)) else { return };
    for row in rows {
        let Some(rest) = s.strip_suffix(row.key.as_str()) else { continue };
        let number = rest.strip_suffix(' ').unwrap_or(rest);
        if !one && !prefixed && row.key == "s" && rest == number && decade(number) {
            continue;
        }
        // A digit/grouping-only number has exactly the cardinal branch; avoid
        // building the alternatives vector and its spoken strings for it.
        let (raw, sign) =
            if let Some(raw) = number.strip_prefix('-') { (raw, "negative: \"true\" ") } else { (number, "") };
        if !raw.is_empty() && raw.chars().all(|c| numbers::is_digit(c) || c == ',') {
            if let Some(recipe) = numeric::cardinal_recipe(raw) {
                if (raw == "1" || raw == "౧") == one {
                    if prefixed {
                        for prefix in UNIT_PREFIX.exact(&row.key) {
                            out.push(Candidate { end, cost: cost(1.08) + recipe.weight, rank: 7,
                                tag: lazy::tag(|| {
                                    let word = numbers::to_attributive(&recipe.render());
                                    format!("measure {{ morphosyntactic_features: \"{}\" cardinal {{ {sign}integer: \"{word}\" }} units: \"{}\" preserve_order: true }}", prefix.value, row.value)
                                }) });
                        }
                    } else {
                        out.push(Candidate { end, cost: cost(1.08) + recipe.weight, rank: 7,
                            tag: lazy::tag(|| {
                                let word = numbers::to_attributive(&recipe.render());
                                format!("measure {{ cardinal {{ {sign}integer: \"{word}\" }} units: \"{}\" preserve_order: true }}", row.value)
                            }) });
                    }
                }
            }
            continue;
        }
        for (is_one, weight, tag) in numbers(number) {
            if is_one != one {
                continue;
            }
            if prefixed {
                for prefix in UNIT_PREFIX.exact(&row.key) {
                    out.push(Candidate { end, cost: cost(1.08) + weight, rank: 7,
                        tag: lazy::tag(|| format!("measure {{ morphosyntactic_features: \"{}\" {tag} units: \"{}\" preserve_order: true }}", prefix.value, row.value)) });
                }
            } else {
                out.push(Candidate {
                    end,
                    cost: cost(1.08) + weight,
                    rank: 7,
                    tag: lazy::tag(|| format!("measure {{ {tag} units: \"{}\" preserve_order: true }}", row.value)),
                });
            }
        }
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    collect(s, end, out, &ONE_INDEX, true, false);
    collect(s, end, out, &OTHER_INDEX, false, false);
    collect(s, end, out, &PREFIXED_ONE_INDEX, true, true);
    collect(s, end, out, &PREFIXED_OTHER_INDEX, false, true);
}

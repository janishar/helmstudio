//! Date, era, year range and decade graphs.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

const DAYS: &str = include_str!("../../../data/hi/date/days.tsv");
const MONTHS: &str = include_str!("../../../data/hi/date/months.tsv");
const YEAR_SUFFIX: &str = include_str!("../../../data/hi/date/year_suffix.tsv");
const PREFIXES: &str = include_str!("../../../data/hi/date/prefixes.tsv");
const SUFFIXES: &str = include_str!("../../../data/hi/date/suffixes.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static DAYS_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(DAYS).collect());
static DAY_NUMBERS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    rows(include_str!("../../../data/hi/numbers/teens_and_ties.tsv"))
        .chain(rows(include_str!("../../../data/hi/numbers/teens_and_ties_en.tsv")))
        .collect()
});

static MONTHS_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(MONTHS).collect());

static YEAR_SUFFIX_MAP: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| rows(YEAR_SUFFIX).collect());
fn padded_keys(data: &'static str) -> HashMap<&'static str, &'static str> {
    rows(data)
        .filter_map(|(key, value)| {
            let rest = key.strip_prefix('0').or_else(|| key.strip_prefix('०'))?;
            let mut chars = rest.chars();
            let c = chars.next()?;
            (numbers::is_digit(c) && chars.next().is_none()).then_some((rest, value))
        })
        .collect()
}
static PADDED_DAYS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| padded_keys(DAYS));
static PADDED_MONTHS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| padded_keys(MONTHS));
static SPOKEN_DIGITS: LazyLock<HashSet<&'static str>> =
    LazyLock::new(|| rows(include_str!("../../../data/hi/numbers/digit.tsv")).map(|(_, value)| value).collect());
static SPOKEN_PAIRS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    rows(include_str!("../../../data/hi/numbers/teens_and_ties.tsv")).map(|(_, value)| value).collect()
});

static PREFIXES_VEC: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| PREFIXES.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());

static SUFFIXES_VEC: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| SUFFIXES.lines().map(|l| l.trim_end_matches('\r')).filter(|l| !l.is_empty()).collect());
static SUFFIXES_BY_END: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    for &suffix in SUFFIXES_VEC.iter() {
        if let Some(last) = suffix.chars().next_back() {
            index.entry(last).or_default().push(suffix);
        }
    }
    index
});
static ERA_BY_END: LazyLock<HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(&'static str, &'static str)>> = HashMap::new();
    for (&suffix, &value) in YEAR_SUFFIX_MAP.iter() {
        if let Some(last) = suffix.chars().next_back() {
            index.entry(last).or_default().push((suffix, value));
        }
    }
    index
});
fn suffixes(s: &str) -> impl Iterator<Item = &'static str> {
    s.chars().next_back().and_then(|c| SUFFIXES_BY_END.get(&c)).into_iter().flatten().copied()
}
fn eras(s: &str) -> impl Iterator<Item = (&'static str, &'static str)> {
    s.chars().next_back().and_then(|c| ERA_BY_END.get(&c)).into_iter().flatten().copied()
}

static MONTH_NAMES: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    let mut names: Vec<&'static str> = rows(MONTHS).map(|(_, v)| v).collect();
    names.sort_unstable();
    names.dedup();
    names
});
static DATE_ENDS: LazyLock<HashSet<char>> = LazyLock::new(|| {
    SUFFIXES_VEC
        .iter()
        .copied()
        .chain(YEAR_SUFFIX_MAP.keys().copied())
        .chain(MONTH_NAMES.iter().copied())
        .chain(SPOKEN_DIGITS.iter().copied())
        .chain(SPOKEN_PAIRS.iter().copied())
        .chain(["सौ", "वीं"])
        .filter_map(|key| key.chars().next_back())
        .chain(std::iter::once('s'))
        .collect()
});
pub(crate) fn possible_end(s: &str) -> bool {
    s.chars().next_back().is_some_and(|c| numbers::is_digit(c) || DATE_ENDS.contains(&c))
}
static NUMERIC_LEXEMES: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    super::trie::Trie::from_keys(
        MONTH_NAMES
            .iter()
            .copied()
            .chain(YEAR_SUFFIX_MAP.keys().copied())
            .chain(SUFFIXES_VEC.iter().map(|s| s.trim_start_matches(' ')))
            .chain(SPOKEN_DIGITS.iter().copied())
            .chain(SPOKEN_PAIRS.iter().copied())
            .chain(["वीं", "सौ"]),
    )
});
pub(crate) fn numeric_prefix(s: &str) -> bool {
    let Some((i, _)) = s
        .char_indices()
        .find(|&(_, c)| !numbers::is_digit(c) && !matches!(c, ' ' | ',' | '.' | '-' | '–' | '/' | '\'' | 's'))
    else {
        return true;
    };
    let rest = &s[i..];
    let mut complete = false;
    NUMERIC_LEXEMES.matching_prefixes(rest, |_| complete = true);
    complete || NUMERIC_LEXEMES.has_prefix(rest)
}
static STARTS: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    super::trie::Trie::from_keys(
        PREFIXES_VEC
            .iter()
            .copied()
            .chain(YEAR_SUFFIX_MAP.keys().copied())
            .chain(MONTH_NAMES.iter().copied())
            .chain(rows(include_str!("../../../data/hi/numbers/digit.tsv")).map(|(_, v)| v))
            .chain(rows(include_str!("../../../data/hi/numbers/teens_and_ties.tsv")).map(|(_, v)| v)),
    )
});

pub(crate) fn possible_start(s: &str) -> bool {
    let mut found = false;
    STARTS.matching_prefixes(s, |_| found = true);
    found
}
static MONTH_STARTS: LazyLock<super::trie::Trie> =
    LazyLock::new(|| super::trie::Trie::from_keys(rows(MONTHS).map(|(k, _)| k).chain(MONTH_NAMES.iter().copied())));
fn starts_month(s: &str, has_day: bool) -> bool {
    let mut found = false;
    MONTH_STARTS.matching_prefixes(s, |len| {
        let rest = &s[len..];
        if rest.starts_with(',')
            || (rest.starts_with(' ') && MONTH_NAMES.contains(&&s[..len]))
            || (has_day && rest.is_empty() && !MONTH_NAMES.contains(&&s[..len]))
        {
            found = true;
        }
    });
    found
}

fn emit(out: &mut Vec<Candidate>, end: usize, w: f64, fields: impl FnOnce() -> String) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + w),
        rank: 4,
        tag: super::lazy::tag(|| format!("date {{ {} }}", fields())),
    });
}

/// Convert 4-digit year string to spoken form
pub fn year_spoken(s: &str) -> Option<(String, Cost)> {
    let mut iter = s.chars();
    let chars = [iter.next()?, iter.next()?, iter.next()?, iter.next()?];
    if iter.next().is_some() || !chars.iter().all(|&c| numbers::is_digit(c)) {
        return None;
    }
    // If second digit is 0: thousands graph
    if numbers::zero(chars[1]) {
        let mut first = None;
        numbers::for_each_graph(s, &mut |value, weight| {
            if first.is_none() {
                first = Some((value, weight));
            }
        });
        first
    } else {
        // Second digit non-zero: hundreds as thousands (e.g. 1990 -> उन्नीस सौ नब्बे)
        numbers::hundreds_as_thousand(s)
    }
}

fn date_number(s: &str) -> Vec<(String, Cost)> {
    // date.py projects its shared digit FST in place: this branch accepts the
    // spoken digit words, not written one-digit numbers.
    if SPOKEN_DIGITS.contains(s) {
        return vec![(super::lazy::tag(|| s.to_owned()), 0)];
    }
    match s.chars().count() {
        2..=3 => numbers::graph(s),
        4 => year_spoken(s).into_iter().collect(),
        _ => Vec::new(),
    }
}

fn verbalized_year(s: &str) -> bool {
    let Some((hundreds, remainder)) = s.split_once(" सौ") else { return false };
    if !SPOKEN_PAIRS.contains(hundreds) {
        return false;
    }
    if remainder.is_empty() {
        return true;
    }
    let Some(unit) = remainder.strip_prefix(' ') else { return false };
    SPOKEN_PAIRS.contains(unit) || SPOKEN_DIGITS.contains(unit)
}

fn parse_day(s: &str) -> Option<(&'static str, Cost)> {
    if let Some(&spoken) = DAY_NUMBERS.get(s) {
        return Some((spoken, cost(-0.1)));
    }
    if let Some(&spoken) = DAYS_MAP.get(s) {
        return Some((spoken, 0));
    }
    PADDED_DAYS.get(s).copied().map(|spoken| (spoken, 0))
}

fn parse_month(s: &str) -> Option<&'static str> {
    if let Some(&spoken) = MONTHS_MAP.get(s) {
        return Some(spoken);
    }
    if let Some(&spoken) = PADDED_MONTHS.get(s) {
        return Some(spoken);
    }
    // Month name directly
    if let Some(&name) = MONTH_NAMES.iter().find(|&&name| name == s) {
        return Some(name);
    }
    None
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.contains(" सौ") {
        if verbalized_year(s) {
            emit(out, end, -0.009, || format!("era: \"{s}\" "));
        }
        for suffix in suffixes(s) {
            if let Some(year) = s.strip_suffix(suffix) {
                if verbalized_year(year) {
                    emit(out, end, -0.010, || format!("era: \"{s}\" "));
                }
            }
        }
        for &prefix in PREFIXES_VEC.iter() {
            if let Some(year) = s.strip_prefix(prefix).and_then(|v| v.strip_prefix(' ')) {
                if verbalized_year(year) {
                    emit(out, end, -0.011, || format!("era: \"{s}\""));
                }
                for suffix in suffixes(year) {
                    if year.strip_suffix(suffix).is_some_and(verbalized_year) {
                        emit(out, end, -0.012, || format!("era: \"{s}\""));
                    }
                }
            }
        }
    }
    // 1. Year suffix alone (e.g. "ई.", "ई.पू.")
    if let Some(&era_spoken) = YEAR_SUFFIX_MAP.get(s) {
        emit(out, end, -0.001, || format!("era: \"{era_spoken}\" "));
    }

    // 2. Year + era (e.g. "१९९० ई.")
    for (suffix_key, era_spoken) in eras(s) {
        if let Some(prefix) = s.strip_suffix(suffix_key) {
            let year_str = prefix.strip_suffix(' ').unwrap_or(prefix);
            let reading = if year_str.chars().count() == 3 {
                numbers::graph(year_str).into_iter().next()
            } else {
                year_spoken(year_str)
            };
            if let Some((y_val, weight)) = reading {
                emit(out, end, -0.005 + weight as f64 / 1_000_000.0, || format!("era: \"{y_val} {era_spoken}\" "));
            }
        }
    }

    // 3. Century (e.g. "20वीं" -> "बीसवीं")
    if let Some(num_str) = s.strip_suffix("वीं") {
        if num_str.chars().all(numbers::is_digit) && num_str.chars().count() <= 4 {
            for (spoken, weight) in date_number(num_str) {
                emit(out, end, -0.001 + weight as f64 / 1_000_000.0, || format!("era: \"{spoken}वीं\" "));
            }
        }
    }

    // 4. Decade (e.g. 1990s, 90s, '90s -> "उन्नीस सौ नब्बे का दशक")
    if s.ends_with('s') {
        let core = s.strip_suffix("'s").or_else(|| s.strip_suffix('s')).unwrap_or(s);
        let num_str = core.strip_prefix('\'').unwrap_or(core);
        let chars: Vec<char> = num_str.chars().collect();
        if chars.len() == 4 && !core.starts_with('\'') && numbers::zero(chars[3]) {
            if let Some((y_val, weight)) = year_spoken(num_str) {
                emit(out, end, -0.2 + weight as f64 / 1_000_000.0, || format!("era: \"{y_val} का दशक\""));
            }
        } else if chars.len() == 2 && !numbers::zero(chars[0]) && numbers::zero(chars[1]) {
            if let Some((teens_val, weight)) = numbers::small(&chars) {
                emit(out, end, -0.2 + weight as f64 / 1_000_000.0, || format!("era: \"{teens_val} का दशक\""));
            }
        }
    }

    // The source range endpoints use the projected-digit date number graph.
    for dash in ["-", "–"] {
        if let Some((a, b)) = s.split_once(dash) {
            for (a_val, ac) in date_number(a) {
                for (b_val, bc) in date_number(b) {
                    emit(out, end, -0.005 + (ac + bc) as f64 / 1_000_000.0, || {
                        format!("era: \"{a_val} से {b_val}\" preserve_order: true ")
                    });
                }
            }
        }
    }

    // 6. Year with prefix / suffix (e.g. "वर्ष १९९०", "१९९० में", "सन् १९९० में")
    for &prefix in PREFIXES_VEC.iter() {
        if let Some(after_prefix) = s.strip_prefix(prefix).and_then(|rest| rest.strip_prefix(' ')) {
            for suffix in suffixes(after_prefix) {
                if let Some(year_str) = after_prefix.strip_suffix(suffix) {
                    let y = year_str;
                    if let Some((y_val, weight)) = year_spoken(y) {
                        emit(out, end, -0.010 + weight as f64 / 1_000_000.0, || {
                            format!("era: \"{prefix} {y_val}{suffix}\"")
                        });
                    }
                }
            }
            let y = after_prefix;
            if let Some((y_val, weight)) = year_spoken(y) {
                emit(out, end, -0.009 + weight as f64 / 1_000_000.0, || format!("era: \"{prefix} {y_val}\""));
            }
        }
    }
    for suffix in suffixes(s) {
        if let Some(year_str) = s.strip_suffix(suffix) {
            let y = year_str;
            if let Some((y_val, weight)) = year_spoken(y) {
                emit(out, end, -0.001 + weight as f64 / 1_000_000.0, || format!("era: \"{y_val}{suffix}\" "));
            }
        }
    }

    // 7. Full numeric dates with separators: "-", "/", "."
    for sep in ['-', '/', '.'] {
        if !s.contains(sep) {
            continue;
        }
        let parts: Vec<&str> = s.split(sep).collect();
        if parts.len() == 3 {
            // Check DD-MM-YYYY
            if let (Some((d, day_weight)), Some(m), Some((y, weight))) =
                (parse_day(parts[0]), parse_month(parts[1]), year_spoken(parts[2]))
            {
                let named_month = MONTH_NAMES.contains(&parts[1]);
                let w = if named_month || parts[0].chars().count() == parts[1].chars().count() { -0.15 } else { -0.14 };
                emit(out, end, w + (weight + day_weight) as f64 / 1_000_000.0, || {
                    format!("day: \"{d}\" month: \"{m}\" year: \"{y}\" ")
                });
            }

            // Check YYYY-MM-DD
            if (sep == '-' || sep == '/') && parts[1].chars().all(numbers::is_digit) {
                if let (Some((y, weight)), Some(m), Some((d, day_weight))) =
                    (year_spoken(parts[0]), parse_month(parts[1]), parse_day(parts[2]))
                {
                    emit(out, end, -0.15 + (weight + day_weight) as f64 / 1_000_000.0, || {
                        format!("year: \"{y}\" month: \"{m}\" day: \"{d}\" preserve_order: true ")
                    });
                }
            }
        } else if parts.len() == 2 && sep == '-' {
            // DD-MM
            if MONTH_NAMES.contains(&parts[1]) || parts[0].chars().count() == parts[1].chars().count() {
                if let (Some((d, day_weight)), Some(m)) = (parse_day(parts[0]), parse_month(parts[1])) {
                    emit(out, end, -0.001 + day_weight as f64 / 1_000_000.0, || {
                        format!("day: \"{d}\" month: \"{m}\" ")
                    });
                }
            }
        }
    }

    let separators: Vec<char> = s.chars().filter(|c| matches!(*c, '-' | '/' | '.')).collect();
    if separators.len() == 2 && separators[0] != separators[1] {
        let parts: Vec<&str> = s.split(|c| matches!(c, '-' | '/' | '.')).collect();
        if parts.len() == 3 {
            if parts[1].chars().all(numbers::is_digit) {
                if let (Some((d, dw)), Some(m), Some((y, yw))) =
                    (parse_day(parts[0]), parse_month(parts[1]), year_spoken(parts[2]))
                {
                    emit(out, end, -0.14 + (dw + yw) as f64 / 1_000_000.0, || {
                        format!("day: \"{d}\" month: \"{m}\" year: \"{y}\" ")
                    });
                }
                if separators.iter().all(|c| matches!(*c, '-' | '/')) {
                    if let (Some((y, yw)), Some(m), Some((d, dw))) =
                        (year_spoken(parts[0]), parse_month(parts[1]), parse_day(parts[2]))
                    {
                        emit(out, end, -0.15 + (dw + yw) as f64 / 1_000_000.0, || {
                            format!("year: \"{y}\" month: \"{m}\" day: \"{d}\" preserve_order: true ")
                        });
                    }
                }
            }
        }
    }

    // Space and comma forms use padded numeric months or month names.
    if !starts_month(s, false)
        && !s.split_once(' ').is_some_and(|(day, rest)| parse_day(day).is_some() && starts_month(rest, true))
    {
        return;
    }
    for (written, month, is_name) in
        rows(MONTHS).map(|(k, v)| (k, v, false)).chain(MONTH_NAMES.iter().map(|&name| (name, name, true)))
    {
        for (idx, _) in s.match_indices(written) {
            let before = &s[..idx];
            let after = &s[idx + written.len()..];
            let day = if before.is_empty() {
                None
            } else if let Some(value) = before.strip_suffix(' ') {
                parse_day(value).map(|d| (value, d))
            } else {
                continue;
            };
            if !before.is_empty() && day.is_none() {
                continue;
            }
            if after.is_empty() && !is_name {
                if let Some((raw, (d, dw))) = day {
                    if raw.chars().count() == 2 {
                        emit(out, end, -0.001 + dw as f64 / 1_000_000.0, || {
                            format!("day: \"{d}\" month: \"{month}\" ")
                        });
                    }
                }
            }
            if let Some(rest) = after.strip_prefix(',') {
                if day.is_some_and(|(raw, _)| raw.chars().count() != 2) {
                    continue;
                }
                let rest = rest.strip_prefix(' ').unwrap_or(rest);
                if let Some((y, yw)) = year_spoken(rest) {
                    if let Some((_, (d, dw))) = day {
                        emit(out, end, -0.001 + (dw + yw) as f64 / 1_000_000.0, || {
                            format!("day: \"{d}\" month: \"{month}\" year: \"{y}\" ")
                        });
                    } else {
                        emit(out, end, -0.2 + yw as f64 / 1_000_000.0, || format!("month: \"{month}\" year: \"{y}\" "));
                    }
                }
                for (suffix, era) in eras(rest) {
                    if let Some(year) = rest.strip_suffix(suffix) {
                        if let Some((y, yw)) = year_spoken(year) {
                            if let Some((_, (d, dw))) = day {
                                emit(out, end, -0.003 + (dw + yw) as f64 / 1_000_000.0, || {
                                    format!("day: \"{d}\" month: \"{month}\" year: \"{y}\" era: \"{era}\" ")
                                });
                            } else {
                                emit(out, end, -0.003 + yw as f64 / 1_000_000.0, || {
                                    format!("month: \"{month}\" year: \"{y}\" era: \"{era}\" ")
                                });
                            }
                        }
                    }
                }
            } else if is_name {
                if let Some(year) = after.strip_prefix(' ') {
                    if let Some((y, yw)) = year_spoken(year) {
                        if let Some((_, (d, dw))) = day {
                            emit(out, end, -0.15 + (dw + yw) as f64 / 1_000_000.0, || {
                                format!("day: \"{d}\" month: \"{month}\" year: \"{y}\" ")
                            });
                        } else {
                            emit(out, end, -0.2 + yw as f64 / 1_000_000.0, || {
                                format!("month: \"{month}\" year: \"{y}\" ")
                            });
                        }
                    }
                }
            }
        }
    }
}

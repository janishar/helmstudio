//! Units, currencies and telephone numbers.
use super::graph_utils::{digit, digitwise, lookup, number, number_weight};
use crate::text::{Cost, cost};
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;
const UNITS: &str = include_str!("../../../data/pa/measure/unit.tsv");
const CURRENCIES: &str = include_str!("../../../data/pa/money/currency.tsv");
const MINORS: &str = include_str!("../../../data/pa/money/major_minor_currencies.tsv");
const QUANTITIES: &str = include_str!("../../../data/pa/numbers/quantities.tsv");
struct PrefixIndex {
    rows: Vec<(&'static str, &'static str)>,
    rows_by_first: HashMap<char, Vec<usize>>,
    rows_by_last: HashMap<char, Vec<usize>>,
    prefixes: HashSet<&'static str>,
    ascii_initials: [bool; 128],
    gurmukhi_initials: [bool; 128],
    other_initials: HashSet<char>,
}

impl PrefixIndex {
    fn new(table: &'static str) -> Self {
        let mut index = Self {
            rows: Vec::new(),
            rows_by_first: HashMap::new(),
            rows_by_last: HashMap::new(),
            prefixes: HashSet::new(),
            ascii_initials: [false; 128],
            gurmukhi_initials: [false; 128],
            other_initials: HashSet::new(),
        };
        for row in table.lines() {
            if let Some((key, value)) = row.split_once('\t') {
                let row_index = index.rows.len();
                index.rows.push((key, value));
                if let Some(c) = key.chars().next() {
                    index.rows_by_first.entry(c).or_default().push(row_index);
                    if c.is_ascii() {
                        index.ascii_initials[c as usize] = true;
                    } else if ('\u{a00}'..='\u{a7f}').contains(&c) {
                        index.gurmukhi_initials[c as usize - 0x0a00] = true;
                    } else {
                        index.other_initials.insert(c);
                    }
                }
                if let Some(c) = key.chars().next_back() {
                    index.rows_by_last.entry(c).or_default().push(row_index);
                }
                for (i, _) in key.char_indices().skip(1) {
                    index.prefixes.insert(&key[..i]);
                }
                index.prefixes.insert(key);
            }
        }
        index
    }

    fn can_start(&self, s: &str) -> bool {
        s.chars().next().is_some_and(|c| {
            if c.is_ascii() {
                self.ascii_initials[c as usize]
            } else if ('\u{a00}'..='\u{a7f}').contains(&c) {
                self.gurmukhi_initials[c as usize - 0x0a00]
            } else {
                self.other_initials.contains(&c)
            }
        })
    }

    fn initial_rows(&self, s: &str) -> impl Iterator<Item = (&'static str, &'static str)> + '_ {
        let indices = s.chars().next().and_then(|c| self.rows_by_first.get(&c));
        indices.into_iter().flatten().map(|&i| self.rows[i])
    }

    fn final_rows(&self, s: &str) -> impl Iterator<Item = (&'static str, &'static str)> + '_ {
        let indices = s.chars().next_back().and_then(|c| self.rows_by_last.get(&c));
        indices.into_iter().flatten().map(|&i| self.rows[i])
    }

    // Both lists are in original TSV order. Merging their row numbers keeps
    // the first accepted source reading while visiting only possible affixes.
    fn affix_rows(&self, s: &str) -> impl Iterator<Item = (&'static str, &'static str)> + '_ {
        let first = s.chars().next().and_then(|c| self.rows_by_first.get(&c)).map(Vec::as_slice).unwrap_or(&[]);
        let last = s.chars().next_back().and_then(|c| self.rows_by_last.get(&c)).map(Vec::as_slice).unwrap_or(&[]);
        let (mut i, mut j) = (0, 0);
        std::iter::from_fn(move || {
            let a = first.get(i).copied();
            let b = last.get(j).copied();
            let row = match (a, b) {
                (Some(a), Some(b)) if a < b => {
                    i += 1;
                    a
                }
                (Some(a), Some(b)) if b < a => {
                    j += 1;
                    b
                }
                (Some(a), Some(_)) => {
                    i += 1;
                    j += 1;
                    a
                }
                (Some(a), None) => {
                    i += 1;
                    a
                }
                (None, Some(b)) => {
                    j += 1;
                    b
                }
                (None, None) => return None,
            };
            Some(self.rows[row])
        })
    }
}

static CURRENCY_INDEX: LazyLock<PrefixIndex> = LazyLock::new(|| PrefixIndex::new(CURRENCIES));
static QUANTITY_INDEX: LazyLock<PrefixIndex> = LazyLock::new(|| PrefixIndex::new(QUANTITIES));

pub(crate) fn quantity_suffix(s: &str) -> bool {
    QUANTITY_INDEX.final_rows(s).any(|(key, _)| s.ends_with(key))
}
pub(crate) fn quantity_prefix(s: &str) -> bool {
    s.is_empty() || QUANTITY_INDEX.prefixes.contains(s)
}
static UNIT_PREFIXES: LazyLock<HashSet<&'static str>> = LazyLock::new(|| {
    let mut prefixes = HashSet::new();
    for row in UNITS.lines() {
        if let Some((key, _)) = row.split_once('\t') {
            for (i, _) in key.char_indices().skip(1) {
                prefixes.insert(&key[..i]);
            }
            prefixes.insert(key);
        }
    }
    prefixes
});
static UNIT_ROWS: LazyLock<HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut result: HashMap<char, Vec<(&'static str, &'static str)>> = HashMap::new();
    for row in UNITS.lines() {
        if let Some((key, value)) = row.split_once('\t') {
            if let Some(last) = key.chars().last() {
                result.entry(last).or_default().push((key, value));
            }
        }
    }
    result
});

fn signed(s: &str) -> (&str, &'static str) {
    if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (x, "negative: \"true\" ")
    } else if let Some(x) = s.strip_prefix('~') {
        (x.strip_prefix(' ').unwrap_or(x), "negative: \"approx\" ")
    } else if let Some(x) = s.strip_prefix('±') {
        (x.strip_prefix(' ').unwrap_or(x), "negative: \"pm\" ")
    } else {
        (s, "")
    }
}
/// Reject prefixes that cannot grow into a currency amount. The suffix form
/// may contain a quantity before the currency; ranges may continue after it.
pub(crate) fn money_prefix(input: &str, numeric_end: Option<usize>) -> bool {
    let (s, _) = signed(input);
    if s.is_empty() {
        return true;
    }
    if CURRENCY_INDEX.can_start(s)
        && (CURRENCY_INDEX.prefixes.contains(s) || CURRENCY_INDEX.initial_rows(s).any(|(key, _)| s.starts_with(key)))
    {
        return true;
    }
    let end = numeric_end.unwrap_or_else(|| {
        s.char_indices().find(|&(_, c)| digit(c).is_none() && c != ',' && c != '.').map_or(s.len(), |(i, _)| i)
    });
    if end == 0 {
        return false;
    }
    let rest = s[end..].trim_start_matches(' ');
    if rest.is_empty() {
        return true;
    }
    if QUANTITY_INDEX.can_start(rest)
        && (QUANTITY_INDEX.prefixes.contains(rest)
            || QUANTITY_INDEX.initial_rows(rest).any(|(key, _)| rest.starts_with(key)))
    {
        return true;
    }
    CURRENCY_INDEX.can_start(rest)
        && (CURRENCY_INDEX.prefixes.contains(rest)
            || CURRENCY_INDEX.initial_rows(rest).any(|(key, _)| {
                rest.strip_prefix(key).is_some_and(|suffix| {
                    suffix.is_empty() || suffix.chars().next().is_some_and(|c| matches!(c, ' ' | '-' | '–' | '—' | '/'))
                })
            }))
}

pub(crate) fn unit_prefix(unit: &str) -> bool {
    UNIT_PREFIXES.contains(unit)
}

fn range_number(s: &str) -> Option<(String, Cost)> {
    if let Some((integer, fraction)) = s.split_once('.') {
        let (integer, w) = number(integer)?;
        Some((format!("{integer} ਦਸ਼ਮਲਵ {}", digitwise(fraction)?), w))
    } else {
        number(s)
    }
}

fn range_weight(s: &str) -> Option<Cost> {
    if let Some((integer, fraction)) = s.split_once('.') {
        if fraction.is_empty() || !fraction.chars().all(|c| digit(c).is_some()) {
            return None;
        }
        number_weight(integer)
    } else {
        number_weight(s)
    }
}

pub(crate) fn measure_cost(input: &str) -> Option<Cost> {
    let (s, _) = signed(input);
    if let Some(i) = s.find(|c| matches!(c, 'x' | 'X' | '*')) {
        if let (Some(a), Some(b)) = (number_weight(s[..i].trim()), number_weight(s[i + 1..].trim())) {
            return Some(a + b);
        }
    }
    for &(unit, _) in UNIT_ROWS.get(&s.chars().last()?)? {
        let Some(prefix) = s.strip_suffix(unit) else { continue };
        let num = prefix.trim_end_matches(' ');
        if num.is_empty() {
            continue;
        }
        if unit == "s" && !prefix.ends_with(' ') {
            let mut chars = num.chars();
            let first = chars.next().and_then(digit);
            let len = num.chars().count();
            if num.chars().last().and_then(digit) == Some(0)
                && (len == 2 || len == 4 && matches!(first, Some(1 | 2)))
                && num.chars().all(|c| digit(c).is_some())
            {
                continue;
            }
        }
        if let Some(i) = num.find(|c| matches!(c, '-' | '–' | '—')) {
            let Some(a) = range_weight(num[..i].trim()) else { continue };
            let c = num[i..].chars().next()?;
            let Some(b) = range_weight(num[i + c.len_utf8()..].trim()) else { continue };
            return Some(a + b);
        }
        if num.contains('.') {
            let Some((integer, fraction)) = num.split_once('.') else { continue };
            if fraction.is_empty() || !fraction.chars().all(|c| digit(c).is_some()) {
                continue;
            }
            if let Some(w) = number_weight(integer) {
                return Some(w);
            }
            continue;
        }
        if let Some(w) = number_weight(num) {
            return Some(w);
        }
    }
    None
}

pub(crate) fn measure_tag(s: &str) -> Option<(String, Cost)> {
    let (s, sign) = signed(s);
    if let Some(i) = s.find(|c| matches!(c, 'x' | 'X' | '*')) {
        if let (Some((a, wa)), Some((b, wb))) = (number(s[..i].trim()), number(s[i + 1..].trim())) {
            return Some((format!("measure {{ cardinal {{ {sign}integer: \"{a}\" }} units: \"ਬਾਈ {b}\" }}"), wa + wb));
        }
    }
    for &(unit, spoken) in UNIT_ROWS.get(&s.chars().last()?)? {
        let Some(prefix) = s.strip_suffix(unit) else { continue };
        let num = prefix.trim_end_matches(' ');
        if num.is_empty() {
            continue;
        }
        if unit == "s" && !prefix.ends_with(' ') {
            let d: Vec<char> = num.chars().collect();
            if d.last().and_then(|&c| digit(c)) == Some(0)
                && (d.len() == 2 && d.iter().all(|&c| digit(c).is_some())
                    || d.len() == 4 && matches!(digit(d[0]), Some(1 | 2)) && d.iter().all(|&c| digit(c).is_some()))
            {
                continue;
            }
        }
        if let Some(i) = num.find(|c| matches!(c, '-' | '–' | '—')) {
            let Some((a, wa)) = range_number(num[..i].trim()) else { continue };
            let c = num[i..].chars().next()?;
            let Some((b, wb)) = range_number(num[i + c.len_utf8()..].trim()) else { continue };
            return Some((
                format!("measure {{ cardinal {{ {sign}integer: \"{a} ਤੋਂ {b}\" }} units: \"{spoken}\" }}"),
                wa + wb,
            ));
        }
        if num.contains('.') {
            let Some((integer, fraction)) = num.split_once('.') else { continue };
            let (Some((integer, w)), Some(fraction)) = (number(integer), digitwise(fraction)) else { continue };
            return Some((
                format!(
                    "measure {{ decimal {{ {sign}integer_part: \"{integer}\" fractional_part: \"{fraction}\" }} units: \"{spoken}\" }}"
                ),
                w,
            ));
        }
        if let Some((n, w)) = number(num) {
            return Some((format!("measure {{ cardinal {{ {sign}integer: \"{n}\" }} units: \"{spoken}\" }}"), w));
        }
    }
    None
}

pub(crate) fn money_cost(s: &str) -> Option<Cost> {
    for (i, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—') {
            if let (Some(left), Some(right)) =
                (money_cost(s[..i].trim_end_matches(' ')), money_cost(s[i + c.len_utf8()..].trim_start_matches(' ')))
            {
                return Some(left + right + cost(-0.01));
            }
        }
    }
    let (s, _) = signed(s);
    let s = if let Some((p, unit)) = s.rsplit_once('/') {
        if !matches!(unit, "kg" | "h" | "hr") {
            return None;
        }
        p.trim_end_matches(' ')
    } else {
        s
    };
    for (symbol, major) in CURRENCY_INDEX.affix_rows(s) {
        let (n, prefix) = if let Some(x) = s.strip_prefix(symbol) {
            (x.trim_start_matches(|c| c == ' ' || c == '.'), true)
        } else if let Some(x) = s.strip_suffix(symbol) {
            (x.trim_end_matches(' '), false)
        } else {
            continue;
        };
        if n.is_empty() {
            continue;
        }
        let major = if prefix && matches!(symbol, "₹" | "ਰੁ." | "ਰੁ" | "Rs." | "Rs" | "INR") && matches!(n, "1" | "੧")
        {
            "ਰੁਪਇਆ"
        } else {
            major
        };
        for (key, _) in QUANTITY_INDEX.final_rows(n) {
            if let Some(base) = n.strip_suffix(key) {
                let base = base.trim_end_matches(' ');
                let parsed = if let Some((i, f)) = base.split_once('.') {
                    if f.is_empty() || !f.chars().all(|c| digit(c).is_some()) { None } else { number_weight(i) }
                } else {
                    number_weight(base)
                };
                if let Some(w) = parsed {
                    return Some(w + cost(-0.01));
                }
            }
        }
        if let Some(w) = number_weight(n) {
            return Some(w + if major == "ਰੁਪਇਆ" { cost(-0.001) } else { 0 });
        }
        if let Some((int, frac)) = n.rsplit_once(|c| c == '.' || c == ',') {
            let Some(w) = number_weight(int) else { continue };
            if n.contains('.') && !frac.is_empty() && frac.chars().all(|c| digit(c) == Some(0)) {
                return Some(w + cost(-0.0005));
            }
            if frac.chars().all(|c| digit(c) == Some(0)) {
                return Some(w);
            }
            if lookup(MINORS, major).is_some() {
                let mut first = 0u8;
                let mut second = 0u8;
                let mut count = 0usize;
                let mut trimmed = 0usize;
                for c in frac.chars() {
                    let d = digit(c)?;
                    if count == 0 {
                        first = d;
                    }
                    if count == 1 {
                        second = d;
                    }
                    count += 1;
                    if d != 0 {
                        trimmed = count;
                    }
                }
                if trimmed == 0 {
                    return None;
                }
                if trimmed <= 2 {
                    let val = if trimmed == 1 { first * 10 } else { first * 10 + second };
                    let wf = if val >= 10 { cost(-0.1) } else { 0 };
                    return Some(w + if n.contains('.') && wf > cost(-0.0005) { cost(-0.0005) } else { wf });
                }
                if frac.is_empty() || !frac.chars().all(|c| digit(c).is_some()) {
                    return None;
                }
                return Some(w + cost(-0.0005));
            }
            if n.contains('.') {
                if frac.is_empty() || !frac.chars().all(|c| digit(c).is_some()) {
                    return None;
                }
                return Some(w + cost(-0.0005));
            }
        }
    }
    None
}
pub(crate) fn money_tag(s: &str) -> Option<(String, Cost)> {
    for (i, c) in s.char_indices().skip(1) {
        if matches!(c, '-' | '–' | '—') {
            if let (Some((left, wl)), Some((right, wr))) =
                (money_tag(s[..i].trim_end_matches(' ')), money_tag(s[i + c.len_utf8()..].trim_start_matches(' ')))
            {
                return Some((format!("{left} }} tokens {{ name: \"ਤੋਂ\" }} tokens {{ {right}"), wl + wr + cost(-0.01)));
            }
        }
    }
    let (s, sign) = signed(s);
    let (s, per) = if let Some((p, u)) = s.rsplit_once('/') {
        let spoken = match u {
            "kg" => "ਪ੍ਰਤੀ ਕਿਲੋਗ੍ਰਾਮ",
            "h" | "hr" => "ਪ੍ਰਤੀ ਘੰਟਾ",
            _ => return None,
        };
        (p.trim_end_matches(' '), format!(" morphosyntactic_features: \"{spoken}\""))
    } else {
        (s, String::new())
    };
    for (symbol, major) in CURRENCY_INDEX.affix_rows(s) {
        let (n, prefix) = if let Some(x) = s.strip_prefix(symbol) {
            (x.trim_start_matches(|c| c == ' ' || c == '.'), true)
        } else if let Some(x) = s.strip_suffix(symbol) {
            (x.trim_end_matches(' '), false)
        } else {
            continue;
        };
        if n.is_empty() {
            continue;
        }
        let major = if prefix && matches!(symbol, "₹" | "ਰੁ." | "ਰੁ" | "Rs." | "Rs" | "INR") && matches!(n, "1" | "੧")
        {
            "ਰੁਪਇਆ"
        } else {
            major
        };
        for (key, quantity) in QUANTITY_INDEX.final_rows(n) {
            if let Some(base) = n.strip_suffix(key) {
                let base = base.trim_end_matches(' ');
                let parsed = if let Some((i, f)) = base.split_once('.') {
                    number(i).and_then(|(v, w)| digitwise(f).map(|fraction| (format!("{v} ਦਸ਼ਮਲਵ {fraction}"), w)))
                } else {
                    number(base)
                };
                if let Some((value, w)) = parsed {
                    let value = format!("{value} {quantity}");
                    let fields = if prefix {
                        format!("currency_maj: \"{major}\" integer_part: \"{value}\"")
                    } else {
                        format!("integer_part: \"{value}\" currency_maj: \"{major}\"")
                    };
                    return Some((format!("money {{ {sign}{fields}{per} }}"), w + cost(-0.01)));
                }
            }
        }
        if let Some((v, w)) = number(n) {
            let fields = if prefix {
                format!("currency_maj: \"{major}\" integer_part: \"{v}\"")
            } else {
                format!("integer_part: \"{v}\" currency_maj: \"{major}\"")
            };
            let extra = if major == "ਰੁਪਇਆ" { cost(-0.001) } else { 0 };
            return Some((format!("money {{ {sign}{fields}{per} }}"), w + extra));
        }
        if let Some((int, frac)) = n.rsplit_once(|c| c == '.' || c == ',') {
            let Some((integer, w)) = number(int) else { continue };
            if n.contains('.') && frac.chars().all(|c| digit(c) == Some(0)) && !frac.is_empty() {
                let fraction = digitwise(frac)?;
                let fields = if prefix {
                    format!("currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{fraction}\"")
                } else {
                    format!("integer_part: \"{integer}\" fractional_part: \"{fraction}\" currency_maj: \"{major}\"")
                };
                return Some((format!("money {{ {sign}{fields}{per} }}"), w + cost(-0.0005)));
            }
            if frac.chars().all(|c| digit(c) == Some(0)) {
                let fields = if prefix {
                    format!("currency_maj: \"{major}\" integer_part: \"{integer}\"")
                } else {
                    format!("integer_part: \"{integer}\" currency_maj: \"{major}\"")
                };
                return Some((format!("money {{ {sign}{fields}{per} }}"), w));
            }
            if let Some(minor) = lookup(MINORS, major) {
                let ds: Vec<u8> = frac.chars().map(digit).collect::<Option<Vec<_>>>()?;
                let trimmed = ds.iter().rposition(|&x| x != 0)? + 1;
                let ds = &ds[..trimmed];
                if ds.len() <= 2 {
                    let val = if ds.len() == 1 { ds[0] * 10 } else { ds[0] * 10 + ds[1] };
                    let (fraction, wf) = number(&val.to_string())?;
                    if n.contains('.') && wf > cost(-0.0005) {
                        let fraction = digitwise(frac)?;
                        let fields = if prefix {
                            format!(
                                "currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{fraction}\""
                            )
                        } else {
                            format!(
                                "integer_part: \"{integer}\" fractional_part: \"{fraction}\" currency_maj: \"{major}\""
                            )
                        };
                        return Some((format!("money {{ {sign}{fields}{per} }}"), w + cost(-0.0005)));
                    }
                    let fields = if prefix {
                        format!(
                            "currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{fraction}\" currency_min: \"{minor}\""
                        )
                    } else {
                        format!(
                            "integer_part: \"{integer}\" fractional_part: \"{fraction}\" currency_maj: \"{major}\" currency_min: \"{minor}\""
                        )
                    };
                    return Some((format!("money {{ {sign}{fields}{per} }}"), w + wf));
                }
                let fraction = digitwise(frac)?;
                let fields = if prefix {
                    format!("currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{fraction}\"")
                } else {
                    format!("integer_part: \"{integer}\" fractional_part: \"{fraction}\" currency_maj: \"{major}\"")
                };
                return Some((format!("money {{ {sign}{fields}{per} }}"), w + cost(-0.0005)));
            }
            if n.contains('.') {
                let fraction = digitwise(frac)?;
                let fields = if prefix {
                    format!("currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{fraction}\"")
                } else {
                    format!("integer_part: \"{integer}\" fractional_part: \"{fraction}\" currency_maj: \"{major}\"")
                };
                return Some((format!("money {{ {sign}{fields}{per} }}"), w + cost(-0.0005)));
            }
        }
    }
    None
}

fn grouped_valid(s: &str, min: usize, max: usize, first_min: usize) -> bool {
    let mut groups = s.split(|c| c == ' ' || c == '-' || c == '.');
    let Some(first) = groups.next() else { return false };
    let first_count = first.chars().count();
    if first_count < first_min || !first.chars().all(|c| digit(c).is_some()) {
        return false;
    }
    let mut total = first_count;
    for group in groups {
        let count = group.chars().count();
        if count < 2 || !group.chars().all(|c| digit(c).is_some()) {
            return false;
        }
        total += count;
    }
    (min..=max).contains(&total)
}

pub(crate) fn telephone_cost(s: &str) -> Option<Cost> {
    if let Some(rest) = s.strip_prefix('+') {
        for split in (1..=3).rev() {
            let end = rest.char_indices().nth(split).map(|x| x.0).unwrap_or(rest.len());
            let (code, remainder) = rest.split_at(end);
            let remainder = remainder.strip_prefix(' ').or_else(|| remainder.strip_prefix('-')).unwrap_or(remainder);
            if !code.is_empty() && code.chars().all(|c| digit(c).is_some()) && grouped_valid(remainder, 6, 12, 1) {
                return Some(0);
            }
        }
        if rest.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')'))
            && (7..=15).contains(&rest.chars().filter(|c| c.is_ascii_digit()).count())
        {
            return Some(0);
        }
    }
    if let Some(rest) = s.strip_prefix('(') {
        let (area, rest) = rest.split_once(')')?;
        if !(2..=5).contains(&area.chars().count())
            || area.chars().next().and_then(digit) != Some(0)
            || !area.chars().all(|c| digit(c).is_some())
        {
            return None;
        }
        let rest = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')).unwrap_or(rest);
        return grouped_valid(rest, 5, 8, 2).then_some(0);
    }
    if s.chars().next().and_then(digit) == Some(0) {
        return grouped_valid(s, 9, 11, 2).then_some(0);
    }
    None
}

fn grouped(s: &str, min: usize, max: usize, first_min: usize) -> Option<String> {
    let groups: Vec<&str> = s.split(|c| c == ' ' || c == '-' || c == '.').collect();
    if groups.first()?.chars().count() < first_min || groups.iter().skip(1).any(|x| x.chars().count() < 2) {
        return None;
    }
    let digits = groups.concat();
    if !(min..=max).contains(&digits.chars().count()) {
        return None;
    }
    digitwise(&digits)
}
pub(crate) fn telephone_tag(s: &str) -> Option<(String, Cost)> {
    if let Some(rest) = s.strip_prefix('+') {
        for split in (1..=3).rev() {
            let end = rest.char_indices().nth(split).map(|x| x.0).unwrap_or(rest.len());
            let (code, remainder) = rest.split_at(end);
            let remainder = remainder.strip_prefix(' ').or_else(|| remainder.strip_prefix('-')).unwrap_or(remainder);
            if let (Some(code), Some(number)) = (digitwise(code), grouped(remainder, 6, 12, 1)) {
                return Some((format!("telephone {{ country_code: \"ਪਲੱਸ {code}\" number_part: \"{number}\" }}"), 0));
            }
        }
        if rest.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
            let digits: String = rest.chars().filter(|c| c.is_ascii_digit()).collect();
            if (7..=15).contains(&digits.len()) {
                let spoken = digitwise(&digits)?;
                return Some((format!("name: \"ਪਲੱਸ\u{a0}{}\"", spoken.replace(' ', "\u{a0}")), 0));
            }
        }
    }
    if let Some(rest) = s.strip_prefix('(') {
        let (area, rest) = rest.split_once(')')?;
        if !(2..=5).contains(&area.chars().count()) || area.chars().next().and_then(digit) != Some(0) {
            return None;
        }
        let area = digitwise(area)?;
        let rest = rest.strip_prefix(' ').or_else(|| rest.strip_prefix('-')).unwrap_or(rest);
        let number = grouped(rest, 5, 8, 2)?;
        return Some((format!("telephone {{ number_part: \"{area} {number}\" }}"), 0));
    }
    if s.chars().next().and_then(digit) == Some(0) {
        let number = grouped(s, 9, 11, 2)?;
        return Some((format!("telephone {{ number_part: \"{number}\" }}"), 0));
    }
    None
}

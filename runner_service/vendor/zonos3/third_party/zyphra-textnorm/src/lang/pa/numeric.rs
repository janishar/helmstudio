//! Numeric classifiers shared by the Punjabi grammar's union branches.
use super::graph_utils::{cardinal, cardinal_input_weight, digit, digitwise, lookup, number, number_weight};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

const QUANTITIES: &str = include_str!("../../../data/pa/numbers/quantities.tsv");
const EXCEPTIONS: &str = include_str!("../../../data/pa/ordinal/exceptions.tsv");
const SUFFIXES: &str = include_str!("../../../data/pa/ordinal/suffixes.tsv");
const FRACTIONS: &str = include_str!("../../../data/pa/fraction/common_fractions.tsv");
const NEXT: &str = include_str!("../../../data/pa/fraction/next_number.tsv");

static QUANTITY_ROWS: LazyLock<HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut rows: HashMap<char, Vec<(&'static str, &'static str)>> = HashMap::new();
    for row in QUANTITIES.lines() {
        if let Some((key, value)) = row.split_once('\t') {
            if let Some(last) = key.chars().last() {
                rows.entry(last).or_default().push((key, value));
            }
        }
    }
    rows
});

fn quantities_ending(s: &str) -> &'static [(&'static str, &'static str)] {
    s.chars().last().and_then(|c| QUANTITY_ROWS.get(&c)).map(Vec::as_slice).unwrap_or(&[])
}

fn sign(s: &str) -> (&str, &'static str) {
    if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (t, "negative: \"true\" ")
    } else if let Some(t) = s.strip_prefix('~') {
        (t.strip_prefix(' ').unwrap_or(t), "negative: \"approx\" ")
    } else if let Some(t) = s.strip_prefix('±') {
        (t.strip_prefix(' ').unwrap_or(t), "negative: \"pm\" ")
    } else {
        (s, "")
    }
}

fn arithmetic(s: &str) -> Option<(String, Cost)> {
    let ranges = s.chars().filter(|&c| matches!(c, '-' | '–')).count();
    if ranges > 0 && (ranges != 1 || s.chars().any(|c| matches!(c, '+' | '=' | '×' | '÷'))) {
        return None;
    }
    let mut pieces = s.split(|c| matches!(c, '+' | '=' | '×' | '÷' | '-' | '–'));
    let first = pieces.next()?.trim_matches(' ');
    let (mut spoken, mut weight) = number(first)?;
    let mut pos = first.len();
    while pos < s.len() {
        let rest = &s[pos..];
        let rest = rest.strip_prefix(' ').unwrap_or(rest);
        let op = rest.chars().next()?;
        let word = match op {
            '+' => " ਜੋੜ ",
            '=' => " ਬਰਾਬਰ ",
            '×' => " ਗੁਣਾ ",
            '÷' => " ਵੰਡ ",
            '-' | '–' => " ਤੋਂ ",
            _ => return None,
        };
        let rest = &rest[op.len_utf8()..];
        let rest = rest.strip_prefix(' ').unwrap_or(rest);
        let end = rest.find(|c| matches!(c, '+' | '=' | '×' | '÷' | '-' | '–')).unwrap_or(rest.len());
        let part = rest[..end].trim_end_matches(' ');
        let (n, w) = number(part)?;
        spoken.push_str(word);
        spoken.push_str(&n);
        weight += w;
        pos = s.len() - rest.len() + end;
    }
    Some((spoken, weight))
}

pub(crate) fn cardinal_tag(s: &str) -> Option<(String, Cost)> {
    let (s, sign) = sign(s);
    let found = number(s).or_else(|| arithmetic(s)).or_else(|| {
        for suffix in ["ਵਾਂ", "ਵੇਂ", "ਵੀਂ"] {
            if let Some(stem) = s.strip_suffix(suffix) {
                let (value, weight) = number(stem)?;
                return Some((format!("{value}{suffix}"), weight));
            }
        }
        None
    })?;
    Some((format!("cardinal {{ {sign}integer: \"{}\" }}", found.0), found.1))
}
/// Recognize the cardinal branch and compute its cost without spelling it.
pub(crate) fn cardinal_cost(input: &str) -> Option<Cost> {
    let (s, _) = sign(input);
    if let Some(weight) = number_weight(s) {
        return Some(weight);
    }
    let ranges = s.chars().filter(|&c| matches!(c, '-' | '–')).count();
    if ranges == 0 || ranges == 1 && !s.chars().any(|c| matches!(c, '+' | '=' | '×' | '÷')) {
        let first = s.split(|c| matches!(c, '+' | '=' | '×' | '÷' | '-' | '–')).next()?.trim_matches(' ');
        if let Some(mut weight) = number_weight(first) {
            let mut pos = first.len();
            let mut valid = true;
            while pos < s.len() {
                let rest = s[pos..].strip_prefix(' ').unwrap_or(&s[pos..]);
                let Some(op) = rest.chars().next() else {
                    valid = false;
                    break;
                };
                if !matches!(op, '+' | '=' | '×' | '÷' | '-' | '–') {
                    valid = false;
                    break;
                }
                let rest = &rest[op.len_utf8()..];
                let rest = rest.strip_prefix(' ').unwrap_or(rest);
                let end = rest.find(|c| matches!(c, '+' | '=' | '×' | '÷' | '-' | '–')).unwrap_or(rest.len());
                if let Some(w) = number_weight(rest[..end].trim_end_matches(' ')) {
                    weight += w;
                } else {
                    valid = false;
                    break;
                }
                pos = s.len() - rest.len() + end;
            }
            if valid {
                return Some(weight);
            }
        }
    }
    for suffix in ["ਵਾਂ", "ਵੇਂ", "ਵੀਂ"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            return number_weight(stem);
        }
    }
    None
}
pub(crate) fn decimal_prefix(input: &str) -> bool {
    let (s, _) = sign(input);
    if s.is_empty() {
        return true;
    }
    let mut point = false;
    let mut end = s.len();
    for (i, c) in s.char_indices() {
        if digit(c).is_some() {
            continue;
        }
        if c == '.' {
            if point {
                return false;
            }
            point = true;
            continue;
        }
        end = i;
        break;
    }
    if end == s.len() {
        return true;
    }
    if end == 0 {
        return false;
    }
    let mut rest = &s[end..];
    if let Some(r) = rest.strip_prefix(' ') {
        if !point {
            return false;
        }
        rest = r;
    }
    super::commerce::quantity_prefix(rest)
}

fn decimal_fields(s: &str) -> Option<(String, Cost)> {
    let (s, sign) = sign(s);
    let mut quantity = None;
    let mut main = s;
    for &(key, val) in quantities_ending(s) {
        if let Some(prefix) = s.strip_suffix(key) {
            if prefix.ends_with(' ') && !prefix.contains('.') {
                continue;
            }
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if !prefix.is_empty() {
                main = prefix;
                quantity = Some(val);
                break;
            }
        }
    }
    let (integer, fraction) = match main.split_once('.') {
        Some((i, f)) if !f.is_empty() => (i, Some(digitwise(f)?)),
        None if quantity.is_some() => (main, None),
        _ => return None,
    };
    let (integer, weight) = cardinal(integer)?;
    let mut fields = format!("{sign}integer_part: \"{integer}\"");
    if let Some(f) = fraction {
        fields.push_str(&format!(" fractional_part: \"{f}\""));
    }
    if let Some(q) = quantity {
        fields.push_str(&format!(" quantity: \"{q}\""));
    }
    Some((fields, weight))
}

pub(crate) fn decimal_tag(s: &str) -> Option<(String, Cost)> {
    let (fields, weight) = decimal_fields(s)?;
    Some((format!("decimal {{ {fields} }}"), weight))
}

pub(crate) fn decimal_cost(input: &str) -> Option<Cost> {
    let (s, _) = sign(input);
    let mut main = s;
    let mut quantity = false;
    for &(key, _) in quantities_ending(s) {
        if let Some(prefix) = s.strip_suffix(key) {
            if prefix.ends_with(' ') && !prefix.contains('.') {
                continue;
            }
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if !prefix.is_empty() {
                main = prefix;
                quantity = true;
                break;
            }
        }
    }
    let integer = match main.split_once('.') {
        Some((integer, fraction)) if !fraction.is_empty() && fraction.chars().all(|c| digit(c).is_some()) => integer,
        None if quantity => main,
        _ => return None,
    };
    cardinal_input_weight(integer)
}

pub(crate) fn ordinal_cost(s: &str) -> Option<Cost> {
    if s.chars().any(|c| digit(c).is_none()) && lookup(EXCEPTIONS, s).is_some() {
        return Some(cost(-0.3));
    }
    for suffix in ["st", "nd", "rd", "th"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            if (2..=5).contains(&stem.chars().count()) {
                if let Some(w) = cardinal_input_weight(stem) {
                    let weight = w + cost(-0.1);
                    if weight <= cost(-0.2) {
                        return Some(weight);
                    }
                }
            }
        }
    }
    for suffix in ["ਵਾਂ", "ਵਾ", "st", "nd", "rd", "th"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            if lookup(EXCEPTIONS, stem).is_some() {
                return Some(cost(-0.2));
            }
        }
    }
    for row in SUFFIXES.lines() {
        let (suffix, _) = row.split_once('\t')?;
        if let Some(stem) = s.strip_suffix(suffix) {
            if (2..=5).contains(&stem.chars().count()) {
                return cardinal_input_weight(stem);
            }
        }
    }
    for suffix in ["st", "nd", "rd", "th"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            if (2..=5).contains(&stem.chars().count()) {
                return Some(cardinal_input_weight(stem)? + cost(-0.1));
            }
        }
    }
    None
}

pub(crate) fn ordinal_tag(s: &str) -> Option<(String, Cost)> {
    if let Some(value) = lookup(EXCEPTIONS, s) {
        if s.chars().any(|c| digit(c).is_none()) {
            return Some((format!("ordinal {{ integer: \"{value}\" }}"), cost(-0.3)));
        }
    }
    // Both the productive English suffix graph and the marked-exception
    // graph can accept these forms. Prefer the productive reading when its
    // path is at least as cheap; a fully marked exception above still wins.
    for suffix in ["st", "nd", "rd", "th"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            if (2..=5).contains(&stem.chars().count()) {
                if let Some((value, w)) = cardinal(stem) {
                    let weight = w + cost(-0.1);
                    if weight <= cost(-0.2) {
                        return Some((format!("ordinal {{ integer: \"{value}ਵਾਂ\" }}"), weight));
                    }
                }
            }
        }
    }
    for suffix in ["ਵਾਂ", "ਵਾ", "st", "nd", "rd", "th"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            if let Some(value) = lookup(EXCEPTIONS, stem) {
                return Some((format!("ordinal {{ integer: \"{value}\" }}"), cost(-0.2)));
            }
        }
    }
    for row in SUFFIXES.lines() {
        let (suffix, spoken) = row.split_once('\t')?;
        if let Some(stem) = s.strip_suffix(suffix) {
            let len = stem.chars().count();
            if (2..=5).contains(&len) {
                let (value, w) = cardinal(stem)?;
                return Some((format!("ordinal {{ integer: \"{value}{spoken}\" }}"), w));
            }
        }
    }
    for suffix in ["st", "nd", "rd", "th"] {
        if let Some(stem) = s.strip_suffix(suffix) {
            let len = stem.chars().count();
            if (2..=5).contains(&len) {
                let (value, w) = cardinal(stem)?;
                return Some((format!("ordinal {{ integer: \"{value}ਵਾਂ\" }}"), w + cost(-0.1)));
            }
        }
    }
    None
}
pub(crate) fn fraction_cost(input: &str) -> Option<Cost> {
    let s = input.strip_prefix('-').or_else(|| input.strip_prefix('−')).unwrap_or(input);
    if lookup(FRACTIONS, s).is_some() {
        return Some(cost(-0.1));
    }
    for (suffix, kind) in
        [(" 1/2", 0), (" ੧/੨", 0), ("½", 0), (" 1/4", 1), (" ੧/੪", 1), ("¼", 1), (" 3/4", 2), (" ੩/੪", 2), ("¾", 2)]
    {
        if let Some(stem) = s.strip_suffix(suffix) {
            let stem = stem.strip_suffix(' ').unwrap_or(stem);
            let w = number_weight(stem)?;
            if kind == 2 {
                lookup(NEXT, stem)?;
            }
            return Some((if kind == 2 { 0 } else { w }) + cost(-0.1));
        }
    }
    if let Some(last) = s.chars().last() {
        let fraction = match last {
            '¼' => Some(("1", "4")),
            '½' => Some(("1", "2")),
            '¾' => Some(("3", "4")),
            '⅐' => Some(("1", "7")),
            '⅑' => Some(("1", "9")),
            '⅒' => Some(("1", "10")),
            '⅓' => Some(("1", "3")),
            '⅔' => Some(("2", "3")),
            '⅕' => Some(("1", "5")),
            '⅖' => Some(("2", "5")),
            '⅗' => Some(("3", "5")),
            '⅘' => Some(("4", "5")),
            '⅙' => Some(("1", "6")),
            '⅚' => Some(("5", "6")),
            '⅛' => Some(("1", "8")),
            '⅜' => Some(("3", "8")),
            '⅝' => Some(("5", "8")),
            '⅞' => Some(("7", "8")),
            _ => None,
        };
        if let Some((a, b)) = fraction {
            let prefix = s[..s.len() - last.len_utf8()].trim_end_matches(' ');
            let mut weight = number_weight(a)? + number_weight(b)?;
            if !prefix.is_empty() {
                weight += number_weight(prefix)?;
            }
            return Some(weight);
        }
    }
    let (mut left, mut right) = s.split_once('/')?;
    if right.contains('/') {
        return None;
    }
    match (left.strip_suffix(' '), right.strip_prefix(' ')) {
        (Some(a), Some(b)) => {
            left = a;
            right = b;
        }
        (None, None) => {}
        _ => return None,
    }
    if left.ends_with(' ') || right.starts_with(' ') {
        return None;
    }
    let (integer, numerator) =
        if let Some((i, n)) = left.rsplit_once(' ') { (Some(i.trim()), n) } else { (None, left) };
    let mut weight = number_weight(numerator.trim())? + number_weight(right.trim())?;
    if let Some(i) = integer {
        weight += number_weight(i)?;
    }
    Some(weight)
}

pub(crate) fn fraction_tag(s: &str) -> Option<(String, Cost)> {
    let (s, negative) = if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (t, "negative: \"true\" ")
    } else {
        (s, "")
    };
    if let Some(value) = lookup(FRACTIONS, s) {
        return Some((format!("fraction {{ {negative}morphosyntactic_features: \"{value}\" }}"), cost(-0.1)));
    }
    for (suffix, kind) in
        [(" 1/2", 0), (" ੧/੨", 0), ("½", 0), (" 1/4", 1), (" ੧/੪", 1), ("¼", 1), (" 3/4", 2), (" ੩/੪", 2), ("¾", 2)]
    {
        if let Some(stem) = s.strip_suffix(suffix) {
            let stem = stem.strip_suffix(' ').unwrap_or(stem);
            let (num, w) = number(stem)?;
            let value = match (kind, stem) {
                (0, "1" | "੧") => "ਡੇਢ".to_string(),
                (0, "2" | "੨") => "ਢਾਈ".to_string(),
                (0, _) => format!("ਸਾਢੇ {num}"),
                (1, _) => format!("ਸਵਾ {num}"),
                _ => format!("ਪੌਣੇ {}", lookup(NEXT, stem)?),
            };
            let weight = if kind == 2 { 0 } else { w };
            return Some((
                format!("fraction {{ {negative}morphosyntactic_features: \"{value}\" }}"),
                weight + cost(-0.1),
            ));
        }
    }
    let vulgar = [
        ('¼', "1/4"),
        ('½', "1/2"),
        ('¾', "3/4"),
        ('⅐', "1/7"),
        ('⅑', "1/9"),
        ('⅒', "1/10"),
        ('⅓', "1/3"),
        ('⅔', "2/3"),
        ('⅕', "1/5"),
        ('⅖', "2/5"),
        ('⅗', "3/5"),
        ('⅘', "4/5"),
        ('⅙', "1/6"),
        ('⅚', "5/6"),
        ('⅛', "1/8"),
        ('⅜', "3/8"),
        ('⅝', "5/8"),
        ('⅞', "7/8"),
    ];
    let mut owned = String::new();
    let mut input = s;
    if let Some(last) = s.chars().last() {
        if let Some((_, repl)) = vulgar.iter().find(|(c, _)| *c == last) {
            owned.push_str(s[..s.len() - last.len_utf8()].trim_end_matches(' '));
            if !owned.is_empty() {
                owned.push(' ');
            }
            owned.push_str(repl);
            input = &owned;
        }
    }
    let normalized = input.replace(" / ", "/");
    let input = normalized.as_str();
    if input.contains(" /") || input.contains("/ ") {
        return None;
    }
    let (integer, frac) = if let Some((i, f)) = input.rsplit_once(' ') {
        if f.contains('/') && !i.ends_with('/') { (Some(i.trim()), f) } else { (None, input) }
    } else {
        (None, input)
    };
    let (a, b) = frac.split_once('/')?;
    let (a, wa) = number(a.trim())?;
    let (b, wb) = number(b.trim())?;
    let mut fields = negative.to_owned();
    let mut weight = wa + wb;
    if let Some(i) = integer {
        let (v, w) = number(i)?;
        fields.push_str(&format!("integer_part: \"{v}\" "));
        weight += w;
    }
    fields.push_str(&format!("numerator: \"{a}\" denominator: \"{b}\""));
    Some((format!("fraction {{ {fields} }}"), weight))
}

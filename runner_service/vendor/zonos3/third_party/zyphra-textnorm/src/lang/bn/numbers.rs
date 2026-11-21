//! Productive Bengali cardinal graphs.
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const PAIRS_BN: &str = include_str!("../../../data/bn/numbers/teens_and_ties.tsv");
const PAIRS_ASCII: &str = include_str!("../../../data/bn/numbers/teens_and_ties_en.tsv");

static PAIRS: LazyLock<[[Option<&'static str>; 100]; 2]> = LazyLock::new(|| {
    let mut out = [[None; 100]; 2];
    for (i, data) in [PAIRS_ASCII, PAIRS_BN].into_iter().enumerate() {
        for line in data.lines() {
            let Some((key, value)) = line.trim_end_matches('\r').split_once('\t') else { continue };
            let mut chars = key.chars();
            let (Some(a), Some(b), None) = (chars.next(), chars.next(), chars.next()) else { continue };
            let base = if i == 0 { '0' as u32 } else { '০' as u32 };
            let a = a as u32 - base;
            let b = b as u32 - base;
            if a < 10 && b < 10 {
                out[i][(10 * a + b) as usize] = Some(value);
            }
        }
    }
    out
});

pub fn digit(c: char) -> Option<&'static str> {
    let n = if c.is_ascii_digit() {
        c as u32 - '0' as u32
    } else if ('০'..='৯').contains(&c) {
        c as u32 - '০' as u32
    } else {
        return None;
    };
    Some(["শূন্য", "এক", "দুই", "তিন", "চার", "পাঁচ", "ছয়", "সাত", "আট", "নয়"][n as usize])
}

pub fn is_digit(c: char) -> bool {
    c.is_ascii_digit() || ('০'..='৯').contains(&c)
}

pub fn zero(c: char) -> bool {
    c == '0' || c == '০'
}

pub fn digitwise(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    if !super::lazy::building() {
        return s.chars().all(is_digit).then(String::new);
    }
    let mut out = String::new();
    for c in s.chars() {
        let word = digit(c)?;
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(word);
    }
    Some(out)
}

/// Centralized 1-3 vs 4+ digit logic from cardinal.py:
/// 1-3 digit groups read as cardinals, 4+ digits read digit-by-digit.
pub fn code_num(s: &str) -> Option<String> {
    let clean: String = s.chars().filter(|&c| c != ',').collect();
    if clean.is_empty() || !clean.chars().all(is_digit) {
        return None;
    }
    if clean.chars().count() >= 4 {
        digitwise(&clean)
    } else {
        let readings = graph(&clean);
        readings.into_iter().min_by_key(|r| r.1).map(|r| r.0)
    }
}

/// All readings from graph_without_leading_zeros and cardinal_with_leading_zeros / separators.
pub fn graph(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    for_each_graph(s, &mut |value, weight| out.push((value, weight)));
    out
}

pub(crate) fn for_each_graph(s: &str, emit: &mut dyn FnMut(String, Cost)) {
    if s.is_empty() {
        return;
    }
    // Check for comma separators:
    if s.contains(',') {
        if let Some(clean) = validate_and_strip_separators(s) {
            for_each_graph(&clean, emit);
        }
        return;
    }

    let mut chars = ['\0'; 14];
    let mut len = 0;
    for c in s.chars() {
        if len == chars.len() || !is_digit(c) {
            return;
        }
        chars[len] = c;
        len += 1;
    }
    let slice = &chars[..len];

    // Numbers with leading zeros read digit-by-digit (weight 0.5)
    if len > 1 && zero(slice[0]) {
        if let Some(dw) = digitwise(s) {
            emit(super::lazy::tag(|| dw), cost(0.5));
        }
        // Also 2-digit numbers like "০৫" have graph_leading_zero: zero + " " + single_digit (weight 0.5)
        return;
    }

    enumerate(slice, emit);
}

fn validate_and_strip_separators(s: &str) -> Option<String> {
    let parts: Vec<&str> = s.split(',').collect();
    if parts.len() < 2 {
        return None;
    }
    // Every part must be purely digits
    if !parts.iter().all(|p| !p.is_empty() && p.chars().all(is_digit)) {
        return None;
    }
    let first_len = parts[0].chars().count();
    let last_len = parts.last().unwrap().chars().count();
    if last_len != 3 {
        return None;
    }

    // Check Indian grouping: 1-2 leading digits, middle groups of 2, final group of 3
    let is_indian =
        (first_len == 1 || first_len == 2) && parts[1..parts.len() - 1].iter().all(|p| p.chars().count() == 2);

    // Check Western grouping: 1-3 leading digits, all subsequent groups of 3
    let is_western = (1..=3).contains(&first_len) && parts[1..].iter().all(|p| p.chars().count() == 3);

    if is_indian || is_western { Some(parts.concat()) } else { None }
}

fn small(x: &[char]) -> Option<(String, Cost)> {
    let (word, weight) = small_word(x)?;
    Some((super::lazy::tag(|| word.to_owned()), weight))
}

pub(crate) fn small_word(x: &[char]) -> Option<(&'static str, Cost)> {
    if x.len() == 1 {
        return digit(x[0]).map(|s| (s, 0));
    }
    if x.len() != 2 {
        return None;
    }
    let (script, base) = if x.iter().all(|c| c.is_ascii_digit()) {
        (0, '0' as u32)
    } else if x.iter().all(|c| ('০'..='৯').contains(c)) {
        (1, '০' as u32)
    } else {
        return None;
    };
    let index = ((x[0] as u32 - base) * 10 + (x[1] as u32 - base)) as usize;
    PAIRS[script][index].map(|s| (s, cost(-0.1)))
}

fn magnitude(len: usize) -> Option<(usize, &'static str, bool)> {
    match len {
        3 => Some((2, "শো", true)),
        4 | 5 => Some((3, "হাজার", false)),
        6 | 7 => Some((5, "লাখ", false)),
        8 | 9 => Some((7, "কোটি", false)),
        10 | 11 => Some((9, "আরব", false)),
        12 | 13 => Some((11, "খরব", false)),
        _ => None,
    }
}

/// Append the first reading with the requested graph weight.
pub(crate) fn append_selected(x: &[char], weight: Cost, out: &mut String) -> bool {
    if x.is_empty() || x.len() > 14 || !x.iter().all(|&c| is_digit(c)) {
        return false;
    }
    if x.len() > 1 && zero(x[0]) {
        if weight != cost(0.5) {
            return false;
        }
        for (i, &c) in x.iter().enumerate() {
            if i != 0 {
                out.push(' ');
            }
            out.push_str(digit(c).unwrap());
        }
        return true;
    }
    append_selected_inner(x, weight, out)
}

fn append_selected_inner(x: &[char], weight: Cost, out: &mut String) -> bool {
    if x.is_empty() || x.len() > 13 {
        return false;
    }
    if x.len() <= 2 {
        if let Some((word, actual)) = small_word(x) {
            if actual == weight {
                out.push_str(word);
                return true;
            }
        }
        return false;
    }
    let Some((power, suffix, glued)) = magnitude(x.len()) else { return false };
    let prefix_len = x.len() - power;
    if prefix_len == 1 && zero(x[0]) {
        return false;
    }
    let Some((prefix, prefix_cost)) = small_word(&x[..prefix_len]) else { return false };
    let rest = &x[prefix_len..];
    let start = out.len();
    out.push_str(prefix);
    if !glued {
        out.push(' ');
    }
    out.push_str(suffix);
    if rest.iter().all(|&c| zero(c)) && weight == prefix_cost + cost(-0.1) * rest.len() as Cost {
        return true;
    }
    out.push(' ');
    let sub_start = out.len();
    for skipped in 0..rest.len() {
        if !rest[..skipped].iter().all(|&c| zero(c)) {
            break;
        }
        let sub = &rest[skipped..];
        if sub.len() == 1 && zero(sub[0]) {
            continue;
        }
        let adjust = if sub.len() == 2 { cost(0.1) } else { 0 };
        if append_selected_inner(sub, weight - prefix_cost - adjust - cost(-0.1) * skipped as Cost, out) {
            return true;
        }
        out.truncate(sub_start);
    }
    out.truncate(start);
    false
}

fn enumerate(x: &[char], emit: &mut dyn FnMut(String, Cost)) {
    if x.is_empty() || x.len() > 13 {
        return;
    }
    if x.len() <= 2 {
        if let Some((value, weight)) = small(x) {
            emit(value, weight);
        }
        return;
    }
    let Some((power, suffix, glued)) = magnitude(x.len()) else { return };
    let prefix_len = x.len() - power;
    if prefix_len == 1 && zero(x[0]) {
        return;
    }
    let Some((prefix, prefix_cost)) = small(&x[..prefix_len]) else { return };
    let rest = &x[prefix_len..];
    if rest.iter().all(|c| zero(*c)) {
        emit(
            super::lazy::tag(|| if glued { format!("{prefix}{suffix}") } else { format!("{prefix} {suffix}") }),
            prefix_cost + cost(-0.1) * rest.len() as Cost,
        );
    }
    // Preserve each source subgraph, including its weight for omitted zeroes.
    for skipped in 0..rest.len() {
        if !rest[..skipped].iter().all(|c| zero(*c)) {
            break;
        }
        let sub = &rest[skipped..];
        if sub.len() == 1 && zero(sub[0]) {
            continue;
        }
        let adjust = if sub.len() == 2 { cost(0.1) } else { 0 };
        enumerate(sub, &mut |reading, reading_cost| {
            emit(
                super::lazy::tag(|| {
                    if glued { format!("{prefix}{suffix} {reading}") } else { format!("{prefix} {suffix} {reading}") }
                }),
                prefix_cost + reading_cost + adjust + cost(-0.1) * skipped as Cost,
            );
        });
    }
}

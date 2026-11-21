//! Productive Urdu cardinal grammar.

use super::Sink;
use crate::lang::ur::lazy;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const TEENS_TIES_DATA: &str = include_str!("../../../../data/ur/numbers/teens_and_ties.tsv");

static PAIRS: LazyLock<[Option<&'static str>; 100]> = LazyLock::new(|| {
    let mut out = [None; 100];
    for line in TEENS_TIES_DATA.lines() {
        let Some((key, value)) = line.trim_end_matches('\r').split_once('\t') else { continue };
        if let Ok(n) = key.parse::<usize>() {
            if n < 100 {
                out[n] = Some(value);
            }
        }
    }
    out
});

#[inline]
pub fn digit(c: char) -> Option<&'static str> {
    match c {
        '0' => Some("صفر"),
        '1' => Some("ایک"),
        '2' => Some("دو"),
        '3' => Some("تین"),
        '4' => Some("چار"),
        '5' => Some("پانچ"),
        '6' => Some("چھ"),
        '7' => Some("سات"),
        '8' => Some("آٹھ"),
        '9' => Some("نو"),
        _ => None,
    }
}

#[inline]
pub fn digitwise(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    if !lazy::building() {
        return s.chars().all(|c| digit(c).is_some()).then(String::new);
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

fn small_static(x: &[char]) -> Option<(&'static str, Cost)> {
    if x.len() == 1 {
        return digit(x[0]).map(|s| (s, 0));
    }
    if x.len() == 2 && x[0].is_ascii_digit() && x[1].is_ascii_digit() {
        let idx = (x[0] as usize - '0' as usize) * 10 + (x[1] as usize - '0' as usize);
        if idx >= 10 {
            return PAIRS[idx].map(|s| (s, cost(-0.1)));
        }
    }
    None
}

fn small(x: &[char]) -> Option<(String, Cost)> {
    small_static(x).map(|(value, weight)| (lazy::tag(|| value.to_owned()), weight))
}

/// Recursively enumerates source cardinal readings for up to 13 digits.
pub fn enumerate_without_leading_zeros(x: &[char], emit: &mut dyn FnMut(String, Cost)) {
    if x.is_empty() || x.len() > 13 || !x.iter().all(|c| c.is_ascii_digit()) {
        return;
    }
    if x.len() <= 2 {
        if let Some((value, weight)) = small(x) {
            emit(value, weight);
        }
        return;
    }
    let (power, suffix) = match x.len() {
        3 => (2, "سو"),
        4 | 5 => (3, "ہزار"),
        6 | 7 => (5, "لاکھ"),
        8 | 9 => (7, "کروڑ"),
        10 | 11 => (9, "ارب"),
        12 | 13 => (11, "کھرب"),
        _ => return,
    };
    let prefix_len = x.len() - power;
    if prefix_len == 1 && x[0] == '0' {
        return;
    }
    let Some((prefix, prefix_cost)) = small(&x[..prefix_len]) else { return };
    let rest = &x[prefix_len..];
    if rest.iter().all(|c| *c == '0') {
        emit(lazy::tag(|| format!("{prefix} {suffix}")), prefix_cost + cost(-0.1) * rest.len() as Cost);
    }
    for skipped in 0..rest.len() {
        if !rest[..skipped].iter().all(|c| *c == '0') {
            break;
        }
        let sub = &rest[skipped..];
        if sub.len() == 1 && sub[0] == '0' {
            continue;
        }
        let adjust = if sub.len() == 2 { cost(0.1) } else { 0 };
        enumerate_without_leading_zeros(sub, &mut |reading, reading_cost| {
            emit(
                lazy::tag(|| format!("{prefix} {suffix} {reading}")),
                prefix_cost + reading_cost + adjust + cost(-0.1) * skipped as Cost,
            );
        });
    }
}

/// 1-3 digits read as cardinal; 4+ digits read digit-by-digit.
/// Used by serial and address taggers.
pub fn code_num(s: &str) -> Option<(String, Cost)> {
    if s.is_empty() || !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    if s.len() <= 3 {
        let mut chars = ['\0'; 3];
        for (slot, byte) in chars.iter_mut().zip(s.bytes()) {
            *slot = byte as char;
        }
        let mut best: Option<(String, Cost)> = None;
        enumerate_without_leading_zeros(&chars[..s.len()], &mut |val, w| {
            if best.as_ref().map_or(true, |b| w < b.1) {
                best = Some((val, w));
            }
        });
        return best;
    }
    digitwise(s).map(|spoken| (spoken, 0))
}

/// Validates whether digits with commas match Indian or Western grouping.
fn valid_separators(s: &str) -> Option<String> {
    if !s.contains(',') {
        return None;
    }
    let parts: Vec<&str> = s.split(',').collect();
    if parts.len() < 2 || parts.iter().any(|p| p.is_empty() || !p.chars().all(|c| c.is_ascii_digit())) {
        return None;
    }
    // Final group must have exactly 3 digits
    if parts.last().unwrap().len() != 3 {
        return None;
    }
    // Check Western: all subsequent groups have 3 digits, first has 1..=3
    let is_western = (1..=3).contains(&parts[0].len()) && parts[1..].iter().all(|p| p.len() == 3);
    // Check Indian: first group 1..=2, middle groups 2 digits, last group 3 digits
    let is_indian = (1..=2).contains(&parts[0].len())
        && parts[1..parts.len() - 1].iter().all(|p| p.len() == 2)
        && parts.last().unwrap().len() == 3;

    if is_western || is_indian { Some(s.replace(',', "")) } else { None }
}

/// Enumerate the source `final_graph`, including leading zero and grouped paths.
pub fn enumerate_cardinal(x: &[char], emit: &mut dyn FnMut(String, Cost)) {
    if x.is_empty() {
        return;
    }
    if x.contains(&',') {
        let span: String = x.iter().collect();
        if let Some(stripped) = valid_separators(&span) {
            let chars: Vec<char> = stripped.chars().collect();
            enumerate_without_leading_zeros(&chars, emit);
        }
        return;
    }
    if !x.iter().all(|c| c.is_ascii_digit()) {
        return;
    }
    if x[0] == '0' && x.len() > 1 {
        let span: String = x.iter().collect();
        if let Some(value) = digitwise(&span) {
            emit(value, cost(0.5));
        }
    } else {
        enumerate_without_leading_zeros(x, emit);
    }
}

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() {
        return;
    }
    let (neg, num_start) = if text[start] == '-' { (true, start + 1) } else { (false, start) };
    if num_start >= text.len() || !text[num_start].is_ascii_digit() {
        return;
    }

    // Collect all consecutive digit and comma characters
    let mut end = num_start;
    let mut has_comma = false;
    while end < text.len() && (text[end].is_ascii_digit() || text[end] == ',') {
        if text[end] == ',' {
            has_comma = true;
        }
        end += 1;
    }

    // Materialization only needs readings ending at the selected endpoint.
    let (first_end, last_end) = if let Some((target_end, _, _)) = sink.target {
        (target_end.max(num_start + 1), target_end.min(end))
    } else {
        (num_start + 1, end)
    };
    for cur_end in first_end..=last_end {
        let chars = &text[num_start..cur_end];
        if chars.last() == Some(&',') {
            continue;
        }

        // 1. Without separators
        if !chars.contains(&',') {
            // Numbers with leading zeros: read digit-by-digit
            if chars.len() >= 2 && chars[0] == '0' {
                let span = lazy::tag(|| chars.iter().collect());
                if let Some(spoken) = if lazy::building() { digitwise(&span) } else { Some(String::new()) } {
                    let c = cost(1.1) + cost(0.5);
                    sink.push(cur_end, c, 0, || {
                        format!(
                            "cardinal {{ {}integer: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            spoken
                        )
                    });
                }
            } else if chars.len() <= 2 {
                if let Some((spoken, internal_cost)) = small_static(chars) {
                    sink.push(cur_end, cost(1.1) + internal_cost, 0, || {
                        format!(
                            "cardinal {{ {}integer: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            spoken
                        )
                    });
                }
            } else {
                enumerate_without_leading_zeros(chars, &mut |spoken, internal_cost| {
                    let c = cost(1.1) + internal_cost;
                    sink.push(cur_end, c, 0, || {
                        format!(
                            "cardinal {{ {}integer: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            spoken
                        )
                    });
                });
            }
        } else if has_comma {
            // 2. With separators
            let span: String = chars.iter().collect();
            if let Some(stripped) = valid_separators(&span) {
                let chars: Vec<char> = stripped.chars().collect();
                enumerate_without_leading_zeros(&chars, &mut |spoken, internal_cost| {
                    let c = cost(1.1) + internal_cost;
                    sink.push(cur_end, c, 0, || {
                        format!(
                            "cardinal {{ {}integer: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            spoken
                        )
                    });
                });
            }
        }
    }
}

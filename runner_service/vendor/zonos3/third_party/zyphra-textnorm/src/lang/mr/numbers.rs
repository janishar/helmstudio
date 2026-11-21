//! Productive Marathi cardinal graphs.
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const PAIRS_MR: &str = include_str!("../../../data/mr/numbers/teens_and_ties.tsv");
const PAIRS_ASCII: &str = include_str!("../../../data/mr/numbers/teens_and_ties_en.tsv");
const HUNDREDS_PREFIX: &str = include_str!("../../../data/mr/numbers/hundreds_prefix.tsv");

static PAIRS: LazyLock<[[Option<&'static str>; 100]; 2]> = LazyLock::new(|| {
    let mut out = [[None; 100]; 2];
    for (i, data) in [PAIRS_ASCII, PAIRS_MR].into_iter().enumerate() {
        for line in data.lines() {
            let Some((key, value)) = line.trim_end_matches('\r').split_once('\t') else { continue };
            let mut chars = key.chars();
            let (Some(a), Some(b), None) = (chars.next(), chars.next(), chars.next()) else { continue };
            let base = if i == 0 { '0' as u32 } else { '०' as u32 };
            let a = a as u32 - base;
            let b = b as u32 - base;
            if a < 10 && b < 10 {
                out[i][(10 * a + b) as usize] = Some(value);
            }
        }
    }
    out
});

static HUNDREDS: LazyLock<[Option<&'static str>; 10]> = LazyLock::new(|| {
    let mut out = [None; 10];
    for line in HUNDREDS_PREFIX.lines() {
        let Some((key, value)) = line.trim_end_matches('\r').split_once('\t') else { continue };
        if let Some(c) = key.chars().next() {
            if c.is_ascii_digit() {
                let d = c as usize - '0' as usize;
                if d < 10 {
                    out[d] = Some(value);
                }
            } else if ('१'..='९').contains(&c) {
                let d = c as usize - '०' as usize;
                if d < 10 {
                    out[d] = Some(value);
                }
            }
        }
    }
    out
});

pub fn digit(c: char) -> Option<&'static str> {
    let n = if c.is_ascii_digit() {
        c as u32 - '0' as u32
    } else if ('०'..='९').contains(&c) {
        c as u32 - '०' as u32
    } else {
        return None;
    };
    Some(["शून्य", "एक", "दोन", "तीन", "चार", "पाच", "सहा", "सात", "आठ", "नऊ"][n as usize])
}

pub fn digit_val(c: char) -> Option<u32> {
    if c.is_ascii_digit() {
        Some(c as u32 - '0' as u32)
    } else if ('०'..='९').contains(&c) {
        Some(c as u32 - '०' as u32)
    } else {
        None
    }
}

pub fn is_zero(c: char) -> bool {
    c == '0' || c == '०'
}

pub fn digitwise(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    if !super::lazy::building() {
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

/// All readings from graph_without_leading_zeros.
pub fn graph(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    for_each_graph(s, &mut |value, weight| out.push((value, weight)));
    out
}

pub(crate) fn for_each_graph(s: &str, emit: &mut dyn FnMut(String, Cost)) {
    let mut chars = ['\0'; 13];
    let mut len = 0;
    for c in s.chars() {
        if len == chars.len() || digit(c).is_none() {
            return;
        }
        chars[len] = c;
        len += 1;
    }
    enumerate(&chars[..len], emit);
}

fn pair_word(x: &[char]) -> Option<&'static str> {
    if x.len() != 2 {
        return None;
    }
    let (script, base) = if x.iter().all(|c| c.is_ascii_digit()) {
        (0, '0' as u32)
    } else if x.iter().all(|c| ('०'..='९').contains(c)) {
        (1, '०' as u32)
    } else {
        return None;
    };
    let index = ((x[0] as u32 - base) * 10 + (x[1] as u32 - base)) as usize;
    PAIRS[script][index]
}

/// A single short graph reading, split into borrowed words for direct tag construction.
pub(crate) fn borrowed_small(x: &[char]) -> Option<(&'static str, Option<&'static str>, Cost)> {
    match x {
        [c] => digit(*c).map(|word| (word, None, 0)),
        [first, _] if !is_zero(*first) => pair_word(x).map(|word| (word, None, cost(-0.1))),
        [first, middle, last] => {
            let d = digit_val(*first)?;
            if d == 0 {
                return None;
            }
            let middle_zero = is_zero(*middle);
            let last_zero = is_zero(*last);
            if d == 1 && middle_zero && last_zero {
                return Some(("शंभर", None, 0));
            }
            let prefix = if d == 1 { "एकशे" } else { HUNDREDS[d as usize]? };
            if middle_zero && last_zero {
                Some((prefix, None, cost(-0.2)))
            } else if middle_zero {
                Some((prefix, Some(digit(*last)?), cost(-0.1)))
            } else {
                Some((prefix, Some(pair_word(&x[1..])?), 0))
            }
        }
        _ => None,
    }
}

fn small(x: &[char]) -> Option<(String, Cost)> {
    if x.len() == 1 {
        return digit(x[0]).map(|s| (super::lazy::tag(|| s.to_owned()), 0));
    }
    if x.len() != 2 {
        return None;
    }
    if is_zero(x[0]) {
        // Leading zero in 2 digits, e.g. "05" -> "शून्य पाच", weight 0.5
        let d = digit(x[1])?;
        return Some((super::lazy::tag(|| format!("शून्य {d}")), cost(0.5)));
    }
    pair_word(x).map(|s| (super::lazy::tag(|| s.to_owned()), cost(-0.1)))
}

fn hundreds(x: &[char], emit: &mut dyn FnMut(String, Cost)) {
    if x.len() != 3 {
        return;
    }
    let Some(d) = digit_val(x[0]) else { return };
    if d == 0 {
        return;
    }
    let prefix = HUNDREDS[d as usize];
    let y_zero = is_zero(x[1]);
    let z_zero = is_zero(x[2]);

    if d == 1 {
        if y_zero && z_zero {
            // 100 -> शंभर (cost 0.0)
            emit(super::lazy::tag(|| "शंभर".to_owned()), 0);
            return;
        }
        if y_zero && !z_zero {
            // 10z -> एकशे z (cost -0.1)
            if let Some(z_word) = digit(x[2]) {
                emit(super::lazy::tag(|| format!("एकशे {z_word}")), cost(-0.1));
            }
            return;
        }
        // 1yz -> एकशे yz (cost 0.0 from teens_ties)
        if let Some(yz_word) = pair_word(&x[1..3]) {
            emit(super::lazy::tag(|| format!("एकशे {yz_word}")), 0);
        }
        return;
    }

    // d in 2..=9
    let Some(p) = prefix else { return };
    if y_zero && z_zero {
        // d00 -> p (cost -0.2)
        emit(super::lazy::tag(|| p.to_owned()), cost(-0.2));
    } else if y_zero && !z_zero {
        // d0z -> p z (cost -0.1)
        if let Some(z_word) = digit(x[2]) {
            emit(super::lazy::tag(|| format!("{p} {z_word}")), cost(-0.1));
        }
    } else if let Some(yz_word) = pair_word(&x[1..3]) {
        // dyz -> p yz (cost 0.0)
        emit(super::lazy::tag(|| format!("{p} {yz_word}")), 0);
    }
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
    if x.len() == 3 {
        hundreds(x, emit);
        return;
    }
    let (power, suffix) = match x.len() {
        4 | 5 => (3, "हजार"),
        6 | 7 => (5, "लाख"),
        8 | 9 => (7, "कोटी"),
        10 | 11 => (9, "अब्ज"),
        12 | 13 => (11, "खर्व"),
        _ => return,
    };
    let prefix_len = x.len() - power;
    if is_zero(x[0]) {
        return;
    }
    let Some((prefix, prefix_cost)) = small(&x[..prefix_len]) else { return };
    let rest = &x[prefix_len..];

    if rest.iter().all(|c| is_zero(*c)) {
        emit(super::lazy::tag(|| format!("{prefix} {suffix}")), prefix_cost + cost(-0.1) * power as Cost);
        return;
    }

    let zeros = rest.iter().take_while(|c| is_zero(**c)).count();
    let sub = &rest[zeros..];
    let zero_cost = cost(-0.1) * zeros as Cost;

    if sub.len() == 1 {
        if let Some(d) = digit(sub[0]) {
            emit(super::lazy::tag(|| format!("{prefix} {suffix} {d}")), prefix_cost + zero_cost);
        }
    } else if sub.len() == 2 {
        // Remainder 10..99 inside larger number uses teens_ties with weight 0.0
        if let Some(tt) = pair_word(sub) {
            emit(super::lazy::tag(|| format!("{prefix} {suffix} {tt}")), prefix_cost + zero_cost);
        }
    } else if sub.len() == 3 {
        hundreds(sub, &mut |h_str, h_cost| {
            emit(super::lazy::tag(|| format!("{prefix} {suffix} {h_str}")), prefix_cost + zero_cost + h_cost);
        });
    } else {
        enumerate(sub, &mut |sub_val, sub_cost| {
            emit(super::lazy::tag(|| format!("{prefix} {suffix} {sub_val}")), prefix_cost + zero_cost + sub_cost);
        });
    }
}

//! Productive Hindi cardinal graphs. The lexical tables are those used by the source FST.
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const PAIRS_HI: &str = include_str!("../../../data/hi/numbers/teens_and_ties.tsv");
const PAIRS_ASCII: &str = include_str!("../../../data/hi/numbers/teens_and_ties_en.tsv");

static PAIRS: LazyLock<[[Option<&'static str>; 100]; 2]> = LazyLock::new(|| {
    let mut out = [[None; 100]; 2];
    for (i, data) in [PAIRS_ASCII, PAIRS_HI].into_iter().enumerate() {
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

pub fn digit(c: char) -> Option<&'static str> {
    let n = if c.is_ascii_digit() {
        c as u32 - '0' as u32
    } else if ('०'..='९').contains(&c) {
        c as u32 - '०' as u32
    } else {
        return None;
    };
    Some(["शून्य", "एक", "दो", "तीन", "चार", "पाँच", "छह", "सात", "आठ", "नौ"][n as usize])
}

pub fn is_digit(c: char) -> bool {
    c.is_ascii_digit() || ('०'..='९').contains(&c)
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

pub fn zero(c: char) -> bool {
    c == '0' || c == '०'
}

pub fn non_zero_digit(c: char) -> bool {
    is_digit(c) && !zero(c)
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

pub(crate) fn small_word(x: &[char]) -> Option<(&'static str, Cost)> {
    if x.len() == 1 {
        return digit(x[0]).map(|s| (s, 0));
    }
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
    PAIRS[script][index].map(|s| (s, cost(-0.1)))
}

pub fn small(x: &[char]) -> Option<(String, Cost)> {
    small_word(x).map(|(value, weight)| (super::lazy::tag(|| value.to_owned()), weight))
}

/// Transducer for hundreds read as thousands, e.g. 1100 -> ग्यारह सौ, 1990 -> उन्नीस सौ नब्बे
pub fn hundreds_as_thousand(s: &str) -> Option<(String, Cost)> {
    let mut iter = s.chars();
    let chars = [iter.next()?, iter.next()?, iter.next()?, iter.next()?];
    if iter.next().is_some() || !chars.iter().all(|&c| is_digit(c)) {
        return None;
    }
    let (prefix, prefix_cost) = small(&chars[..2])?;
    let rest = &chars[2..];
    if zero(rest[0]) && zero(rest[1]) {
        Some((super::lazy::tag(|| format!("{prefix} सौ")), prefix_cost + cost(-0.2)))
    } else if zero(rest[0]) && !zero(rest[1]) {
        let d = digit(rest[1])?;
        Some((super::lazy::tag(|| format!("{prefix} सौ {d}")), prefix_cost + cost(-0.1)))
    } else {
        let (teens, _) = small(rest)?;
        Some((super::lazy::tag(|| format!("{prefix} सौ {teens}")), prefix_cost))
    }
}

/// All readings from graph_without_leading_zeros
pub fn graph(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    for_each_graph(s, &mut |value, weight| out.push((value, weight)));
    out
}

pub(crate) fn for_each_graph(s: &str, emit: &mut dyn FnMut(String, Cost)) {
    for_each_recipe(s, &mut |parts, weight| {
        emit(super::lazy::tag(|| parts.join(" ")), weight);
    });
}

pub(crate) fn for_each_recipe(s: &str, emit: &mut dyn FnMut(&[&'static str], Cost)) {
    let mut chars = ['\0'; 20];
    let mut len = 0;
    for c in s.chars() {
        if len == chars.len() || !is_digit(c) {
            return;
        }
        chars[len] = c;
        len += 1;
    }
    let mut parts = [""; 20];
    enumerate(&chars[..len], &mut parts, 0, 0, emit);
}

fn enumerate(
    x: &[char],
    parts: &mut [&'static str; 20],
    depth: usize,
    weight: Cost,
    emit: &mut dyn FnMut(&[&'static str], Cost),
) {
    if x.is_empty() || x.len() > 19 {
        return;
    }
    if x.len() <= 2 {
        if let Some((value, extra)) = small_word(x) {
            parts[depth] = value;
            emit(&parts[..depth + 1], weight + extra);
        }
        return;
    }
    let (power, suffix) = match x.len() {
        3 => (2, "सौ"),
        4 | 5 => (3, "हज़ार"),
        6 | 7 => (5, "लाख"),
        8 | 9 => (7, "करोड़"),
        10 | 11 => (9, "अरब"),
        12 | 13 => (11, "खरब"),
        14 | 15 => (13, "नील"),
        16 | 17 => (15, "पद्म"),
        18 | 19 => (17, "शंख"),
        _ => return,
    };
    let prefix_len = x.len() - power;
    if prefix_len == 1 && zero(x[0]) {
        return;
    }
    let Some((prefix, prefix_cost)) = small_word(&x[..prefix_len]) else { return };
    let rest = &x[prefix_len..];
    parts[depth] = prefix;
    parts[depth + 1] = suffix;
    let depth = depth + 2;
    let weight = weight + prefix_cost;

    if rest.iter().all(|c| zero(*c)) {
        emit(&parts[..depth], weight + cost(-0.1) * rest.len() as Cost);
        return;
    }

    if rest[..power - 1].iter().all(|c| zero(*c)) && !zero(rest[power - 1]) {
        if let Some(d) = digit(rest[power - 1]) {
            parts[depth] = d;
            emit(&parts[..depth + 1], weight + cost(-0.1) * (power - 1) as Cost);
        }
    }

    if power >= 2 && rest[..power - 2].iter().all(|c| zero(*c)) && !zero(rest[power - 2]) {
        if let Some((teens, _)) = small_word(&rest[power - 2..]) {
            parts[depth] = teens;
            emit(&parts[..depth + 1], weight + cost(-0.1) * (power - 2) as Cost);
        }
    }

    // Case 4: Lower subgraphs
    let lower_powers: &[usize] = match power {
        3 => &[2],
        5 => &[2, 3],
        7 => &[2, 3, 5],
        9 => &[2, 3, 5, 7],
        11 => &[2, 3, 5, 7, 9],
        13 => &[2, 3, 5, 7, 9, 11],
        15 => &[2, 3, 5, 7, 9, 11, 13],
        17 => &[2, 3, 5, 7, 9, 11, 13, 15],
        _ => &[],
    };

    for &lp in lower_powers {
        // Lower group can be 1 or 2 leader digits before lp zeros/lower group
        let lengths: &[usize] = if lp == 2 { &[3] } else { &[lp + 1, lp + 2] };
        for &sub_len in lengths {
            if sub_len > power {
                continue;
            }
            let zeros = power - sub_len;
            if rest[..zeros].iter().all(|c| zero(*c)) && !zero(rest[zeros]) {
                enumerate(&rest[zeros..], parts, depth, weight + cost(-0.1) * zeros as Cost, emit);
            }
        }
    }
}

/// Centralized 1-3 digits cardinal vs 4+ digits digit-by-digit logic for Address and Serial
pub fn code_num(s: &str) -> Vec<(String, Cost)> {
    let mut out = Vec::new();
    let mut chars = s.chars();
    let mut len = 0;
    while let Some(c) = chars.next() {
        if !is_digit(c) {
            return out;
        }
        len += 1;
    }
    if len == 0 {
        return out;
    }
    if len <= 3 {
        for_each_graph(s, &mut |val, c| out.push((val, c)));
    } else if let Some(dw) = digitwise(s) {
        out.push((dw, 0));
    }
    out
}

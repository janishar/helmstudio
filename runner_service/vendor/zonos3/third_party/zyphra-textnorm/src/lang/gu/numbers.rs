//! Productive Gujarati cardinal graphs. The lexical tables are those used by the source FST.
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const PAIRS_GU: &str = include_str!("../../../data/gu/numbers/teens_and_ties.tsv");
const PAIRS_ASCII: &str = include_str!("../../../data/gu/numbers/teens_and_ties_en.tsv");

static PAIRS: LazyLock<[[Option<&'static str>; 100]; 2]> = LazyLock::new(|| {
    let mut out = [[None; 100]; 2];
    for (i, data) in [PAIRS_ASCII, PAIRS_GU].into_iter().enumerate() {
        for line in data.lines() {
            let Some((key, value)) = line.trim_end_matches('\r').split_once('\t') else { continue };
            let mut chars = key.chars();
            let (Some(a), Some(b), None) = (chars.next(), chars.next(), chars.next()) else { continue };
            let base = if i == 0 { '0' as u32 } else { '૦' as u32 };
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
    } else if ('૦'..='૯').contains(&c) {
        c as u32 - '૦' as u32
    } else {
        return None;
    };
    Some(["શૂન્ય", "એક", "બે", "ત્રણ", "ચાર", "પાંચ", "છ", "સાત", "આઠ", "નવ"][n as usize])
}

fn zero(c: char) -> bool {
    c == '0' || c == '૦'
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

/// All readings from graph_without_leading_zeros, including the source's
/// two-digit 00–09 alternatives and the weights on deleted zeroes.
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

fn small(x: &[char]) -> Option<(String, Cost)> {
    if x.len() == 1 {
        return digit(x[0]).map(|s| (super::lazy::tag(|| s.to_owned()), 0));
    }
    if x.len() != 2 {
        return None;
    }
    let (script, base) = if x.iter().all(|c| c.is_ascii_digit()) {
        (0, '0' as u32)
    } else if x.iter().all(|c| ('૦'..='૯').contains(c)) {
        (1, '૦' as u32)
    } else {
        return None;
    };
    let index = ((x[0] as u32 - base) * 10 + (x[1] as u32 - base)) as usize;
    PAIRS[script][index].map(|s| (super::lazy::tag(|| s.to_owned()), cost(-0.1)))
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
    let (power, suffix) = match x.len() {
        3 => (2, "સો"),
        4 | 5 => (3, "હજાર"),
        6 | 7 => (5, "લાખ"),
        8 | 9 => (7, "કરોડ"),
        10 | 11 => (9, "અબજ"),
        12 | 13 => (11, "ખરબ"),
        _ => return,
    };
    let prefix_len = x.len() - power;
    if prefix_len == 1 && zero(x[0]) {
        return;
    }
    let Some((prefix, prefix_cost)) = small(&x[..prefix_len]) else { return };
    let rest = &x[prefix_len..];
    if rest.iter().all(|c| zero(*c)) {
        emit(super::lazy::tag(|| format!("{prefix} {suffix}")), prefix_cost + cost(-0.1) * rest.len() as Cost);
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
                super::lazy::tag(|| format!("{prefix} {suffix} {reading}")),
                prefix_cost + reading_cost + adjust + cost(-0.1) * skipped as Cost,
            );
        });
    }
}

//! Telugu Composite expressions (time ranges, en-dash cardinal ranges, glued symbols).

use super::lazy;
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

fn tag_name(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 16);
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}

fn short(s: &str) -> Option<(String, Cost)> {
    let count = s.chars().count();
    if !(1..=6).contains(&count) || !s.chars().all(numbers::is_digit) {
        return None;
    }
    if count > 1 && numbers::is_zero(s.chars().next()?) {
        Some((numbers::digitwise(s)?, cost(0.5)))
    } else {
        Some((numbers::cardinal(s)?, 0))
    }
}

fn spoken_clock(s: &str) -> Option<(String, Cost)> {
    let parts: Vec<_> = s.split(':').collect();
    if !(parts.len() == 2 || parts.len() == 3)
        || !(1..=2).contains(&parts[0].chars().count())
        || parts[1].chars().count() != 2
        || (parts.len() == 3 && parts[2].chars().count() != 2)
        || !parts.iter().all(|part| part.chars().all(numbers::is_digit))
    {
        return None;
    }
    let mut candidates = Vec::new();
    lazy::with_build(true, || super::time::classify(s, 0, &mut candidates));
    let candidate = candidates.into_iter().min_by_key(|c| c.cost)?;
    let tagged = format!("tokens {{ {} }}", candidate.tag);
    let parsed = crate::tag::parse(&tagged).ok()?;
    let [(_, crate::tag::Value::Dict(body))] = parsed.first()?.as_slice() else { return None };
    let spoken = super::verbalize::verbalize(body)?;
    Some((spoken, candidate.cost - cost(1.1)))
}

pub fn possible_start(first: char) -> bool {
    numbers::is_digit(first) || matches!(first, '©' | '§')
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let weight = cost(1.03);

    // 1. Glued copyright / section symbols: "©2024", "§12"
    for (sym, prefix_spoken) in [("©", "కాపీరైట్ "), ("§", "విభాగం ")] {
        if let Some(rest) = s.strip_prefix(sym) {
            if let Some((card, inner_cost)) = short(rest) {
                let spoken = format!("{prefix_spoken}{card}");
                let tag_str = tag_name(&spoken);
                out.push(Candidate { end, cost: weight + inner_cost, rank: 14, tag: lazy::tag(move || tag_str) });
            }
        }
    }

    // 2. Time ranges: "9:00-17:00", "09:00–17:00", "9:30-18:15"
    if s.contains(':') {
        for dash in ["-", "–", "—"] {
            if let Some((left, right)) = s.split_once(dash) {
                let left = left.strip_suffix(' ').unwrap_or(left);
                let right = right.strip_prefix(' ').unwrap_or(right);
                if let (Some((t1, w1)), Some((t2, w2))) = (spoken_clock(left), spoken_clock(right)) {
                    let spoken = format!("{t1} నుండి {t2}");
                    let tag_str = tag_name(&spoken);
                    out.push(Candidate { end, cost: weight + w1 + w2, rank: 14, tag: lazy::tag(move || tag_str) });
                }
            }
        }
    }

    // 3. En-dash cardinal ranges: "10–20" -> "పది నుండి ఇరవై"
    if let Some((left, right)) = s.split_once('–') {
        let left = left.strip_suffix(' ').unwrap_or(left);
        let right = right.strip_prefix(' ').unwrap_or(right);
        if let (Some((c1, w1)), Some((c2, w2))) = (short(left), short(right)) {
            let spoken = format!("{c1} నుండి {c2}");
            let tag_str = tag_name(&spoken);
            out.push(Candidate { end, cost: weight + w1 + w2, rank: 14, tag: lazy::tag(move || tag_str) });
        }
    }
}

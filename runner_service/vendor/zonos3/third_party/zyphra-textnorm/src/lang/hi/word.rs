//! Fallback word classifier for Hindi.
use crate::lattice::Candidate;
use crate::text::cost;

pub(crate) fn is_hindi_char(c: char) -> bool {
    matches!(c, '\u{0900}'..='\u{0903}' | '\u{0905}'..='\u{0939}' | '\u{093E}'..='\u{094D}')
}

pub(crate) fn classify_count(s: &str, end: usize, hindi_count: usize, out: &mut Vec<Candidate>) {
    let cost_offset = if hindi_count > 0 { -0.0001 * (hindi_count as f64) } else { 0.0 };

    out.push(Candidate {
        end,
        cost: cost(100.0 + cost_offset),
        rank: 13,
        tag: super::lazy::tag(|| format!("name: \"{s}\"")),
    });
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese cardinal classifier: plain and dot-grouped integers, optional negative sign.

use super::super::graph_utils::{
    WEIGHT_CARDINAL, cardinal_cost, dot_cost, scan_dot_grouped_numbers, spell_cardinal_digits, spell_dot_digits,
};
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Cardinal;

impl Cardinal {
    pub fn spell(digits: &[u8], negative: bool, out: &mut String) -> Option<Cost> {
        out.push_str("cardinal { ");
        if negative {
            out.push_str("negative: \"true\" ");
        }
        out.push_str("integer: \"");
        let cost_adj = spell_cardinal_digits(digits, out)?;
        out.push_str("\" }");
        Some(cost_adj)
    }

    pub fn spell_grouped(digits: &[u8], negative: bool, out: &mut String) -> Option<Cost> {
        out.push_str("cardinal { ");
        if negative {
            out.push_str("negative: \"true\" ");
        }
        out.push_str("integer: \"");
        let c = spell_dot_digits(digits, out)?;
        out.push_str("\" }");
        Some(c)
    }

    /// Read raw digits and optional negative sign, calling `f(end, digits, negative, internal_cost)`.
    pub fn scan_spans(text: &[char], start: usize, mut f: impl FnMut(usize, &[u8], bool, Cost)) {
        if start >= text.len() {
            return;
        }

        let (negative, num_start) = if text[start] == '-' && start + 1 < text.len() && text[start + 1].is_ascii_digit()
        {
            (true, start + 1)
        } else if text[start].is_ascii_digit() {
            (false, start)
        } else {
            return;
        };

        // 1. Dotted format (e.g. 1.000, 1.234.567)
        scan_dot_grouped_numbers(text, num_start, false, |end, digits| {
            if let Some(extra_cost) = dot_cost(digits) {
                f(end, digits, negative, extra_cost);
            }
        });

        let mut digits = [0u8; 21];
        let mut len = 0;
        let mut i = num_start;
        while i < text.len() && text[i].is_ascii_digit() && len < 21 {
            digits[len] = text[i] as u8;
            len += 1;
            i += 1;
            if let Some(extra_cost) = cardinal_cost(&digits[..len]) {
                f(i, &digits[..len], negative, extra_cost);
            }
        }
    }
}

impl Classifier for Cardinal {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan_spans(text, start, |end, _, _, internal_cost| {
            out.push(Candidate { end, cost: WEIGHT_CARDINAL + internal_cost, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let (negative, num_start) = if text[start] == '-' { (true, start + 1) } else { (false, start) };

        if text[num_start..candidate.end].contains(&'.') {
            scan_dot_grouped_numbers(text, num_start, false, |end, digits| {
                if end == candidate.end {
                    let mut tag = String::new();
                    if Self::spell_grouped(digits, negative, &mut tag).is_some() {
                        candidate.tag = tag;
                    }
                }
            });
            return;
        }

        let mut digits = Vec::new();
        for &c in &text[num_start..candidate.end] {
            if c.is_ascii_digit() {
                digits.push(c as u8);
            }
        }
        let mut tag = String::new();
        if Self::spell(&digits, negative, &mut tag).is_some() {
            candidate.tag = tag;
        }
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese range classifier: endpoint - endpoint -> name: "left đến right".

use super::super::graph_utils::WEIGHT_RANGE;
use super::super::verbalizers;
use super::cardinal::Cardinal;
use super::date::Date;
use super::decimal::Decimal;
use super::measure::Measure;
use super::money::Money;
use super::time::Time;
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Range;

fn verbalize_tagged(tag: &str) -> Option<String> {
    let mut wrapped = String::with_capacity(tag.len() + 16);
    wrapped.push_str("tokens { ");
    wrapped.push_str(tag);
    wrapped.push_str(" }");

    let tokens = crate::tag::parse(&wrapped).ok()?;
    if tokens.len() != 1 {
        return None;
    }
    let single_dict = &tokens[0];
    if single_dict.len() != 1 {
        return None;
    }
    let (key, val) = &single_dict[0];
    if key != "tokens" {
        return None;
    }
    let crate::tag::Value::Dict(body) = val else { return None };
    verbalizers::verbalize(body)
}

fn scan_any_endpoint(text: &[char], start: usize, out: &mut Vec<(usize, String, Cost)>) {
    if start >= text.len() {
        return;
    }

    // 1. Cardinal (positive only, as in source positive_cardinal_graph)
    if text[start].is_ascii_digit() {
        Cardinal::scan_spans(text, start, |end, digits, negative, cost| {
            let mut tag = String::new();
            let result = if text[start..end].contains(&'.') {
                Cardinal::spell_grouped(digits, negative, &mut tag)
            } else {
                Cardinal::spell(digits, negative, &mut tag)
            };
            if result.is_some() {
                if let Some(v) = verbalize_tagged(&tag) {
                    out.push((end, v, cost));
                }
            }
        });
    }

    // 2. Decimal (positive only)
    Decimal::scan_positive_spans(text, start, |d_end, d_body, cost| {
        let tag = format!("decimal {{ {} }}", d_body);
        if let Some(v) = verbalize_tagged(&tag) {
            out.push((d_end, v, cost));
        }
    });

    // 3. Money
    Money::scan_spans(text, start, |m_end, m_tag, cost| {
        if let Some(v) = verbalize_tagged(&m_tag) {
            out.push((m_end, v, cost));
        }
    });

    // 4. Measure
    Measure::scan_spans(text, start, |meas_end, meas_tag, cost| {
        if let Some(v) = verbalize_tagged(&meas_tag) {
            out.push((meas_end, v, cost));
        }
    });

    // 5. Time
    Time::scan_spans(text, start, |t_end, t_body| {
        let tag = format!("time {{ {} }}", t_body);
        if let Some(v) = verbalize_tagged(&tag) {
            out.push((t_end, v, 0));
        }
    });

    // 6. Date
    Date::scan_spans(text, start, |d_end, d_body, cost| {
        let tag = format!("date {{ {} }}", d_body);
        if let Some(v) = verbalize_tagged(&tag) {
            out.push((d_end, v, cost));
        }
    });
}

impl Classifier for Range {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() {
            return;
        }
        // Every range has a literal separator between its endpoints.
        if !text[start..].iter().any(|&c| c == '-' || c == '–') {
            return;
        }

        let mut left_endpoints = Vec::new();
        scan_any_endpoint(text, start, &mut left_endpoints);

        for (p1, left_v, left_cost) in left_endpoints {
            let mut dash_pos = p1;
            if dash_pos < text.len() && text[dash_pos] == ' ' {
                dash_pos += 1;
            }
            if dash_pos < text.len() && (text[dash_pos] == '-' || text[dash_pos] == '–') {
                let mut right_start = dash_pos + 1;
                if right_start < text.len() && text[right_start] == ' ' {
                    right_start += 1;
                }

                let mut right_endpoints = Vec::new();
                scan_any_endpoint(text, right_start, &mut right_endpoints);

                for (p2, right_v, right_cost) in right_endpoints {
                    let tag = format!("name: \"{} đến {}\"", left_v, right_v);
                    out.push(Candidate { end: p2, cost: WEIGHT_RANGE + left_cost + right_cost, rank: 0, tag });
                }
            }
        }
    }
}

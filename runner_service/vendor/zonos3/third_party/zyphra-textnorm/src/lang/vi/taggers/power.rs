// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese power classifier: 1..4 ASCII letters + "^" + exp.

use super::super::graph_utils::WEIGHT_POWER;
use super::cardinal::Cardinal;
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;

pub struct Power;

impl Classifier for Power {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start >= text.len() || !text[start].is_ascii_alphabetic() {
            return;
        }

        let mut a_len = 0;
        while start + a_len < text.len() && text[start + a_len].is_ascii_alphabetic() && a_len < 4 {
            a_len += 1;
            let p = start + a_len;
            if p < text.len() && text[p] == '^' {
                let alpha: String = text[start..p].iter().collect();
                let exp_start = p + 1;
                if exp_start >= text.len() {
                    continue;
                }

                // Check "2" -> "bình phương"
                if text[exp_start] == '2' {
                    out.push(Candidate {
                        end: exp_start + 1,
                        cost: WEIGHT_POWER,
                        rank: 0,
                        tag: format!("name: \"{} bình phương\"", alpha),
                    });
                }

                // Check "3" -> "lập phương"
                if text[exp_start] == '3' {
                    out.push(Candidate {
                        end: exp_start + 1,
                        cost: WEIGHT_POWER,
                        rank: 0,
                        tag: format!("name: \"{} lập phương\"", alpha),
                    });
                }

                // Check general cardinal exponent (weight +0.01): "mũ " + [âm ] + cardinal
                let (neg, num_start) = if text[exp_start] == '-' || text[exp_start] == '−' {
                    (true, exp_start + 1)
                } else {
                    (false, exp_start)
                };
                if !text.get(num_start).is_some_and(char::is_ascii_digit) {
                    continue;
                }

                Cardinal::scan_spans(text, num_start, |end, digits, _, weight| {
                    let mut tagged = String::new();
                    let c = if text[num_start..end].contains(&'.') {
                        Cardinal::spell_grouped(digits, false, &mut tagged)
                    } else {
                        Cardinal::spell(digits, false, &mut tagged)
                    };
                    if c.is_some() {
                        if let Some(spoken) =
                            tagged.strip_prefix("cardinal { integer: \"").and_then(|s| s.strip_suffix("\" }"))
                        {
                            let sign = if neg { "âm " } else { "" };
                            out.push(Candidate {
                                end,
                                cost: WEIGHT_POWER + cost(0.01) + weight,
                                rank: 0,
                                tag: format!("name: \"{} mũ {}{}\"", alpha, sign, spoken),
                            });
                        }
                    }
                });
            }
        }
    }
}

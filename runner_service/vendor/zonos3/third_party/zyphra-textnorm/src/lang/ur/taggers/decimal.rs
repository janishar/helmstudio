//! Productive Urdu decimal grammar.

use super::Sink;
use super::cardinal::{digitwise, enumerate_without_leading_zeros};
use crate::text::cost;

const QUANTITIES: &[&str] = &["ہزار", "لاکھ", "کروڑ", "ارب", "کھرب"];

fn prefix_len(text: &[char], key: &str) -> Option<usize> {
    let mut len = 0;
    for c in key.chars() {
        if text.get(len) != Some(&c) {
            return None;
        }
        len += 1;
    }
    Some(len)
}

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() {
        return;
    }
    let (neg, num_start) = if text[start] == '-' { (true, start + 1) } else { (false, start) };
    if num_start >= text.len() || !text[num_start].is_ascii_digit() {
        return;
    }

    // Find length of integer part digits
    let mut int_end = num_start;
    while int_end < text.len() && text[int_end].is_ascii_digit() {
        int_end += 1;
    }

    let int_chars = &text[num_start..int_end];

    // The separator in get_quantity is an output insertion, not input.
    {
        let after_space = int_end;
        for &qty in QUANTITIES {
            if let Some(len) = prefix_len(&text[after_space..], qty) {
                let cur_end = after_space + len;
                enumerate_without_leading_zeros(int_chars, &mut |int_spoken, internal_cost| {
                    let c = cost(1.1) + internal_cost;
                    sink.push(cur_end, c, 0, || {
                        format!(
                            "decimal {{ {}integer_part: \"{}\" quantity: \"{}\" }}",
                            if neg { "negative: \"true\" " } else { "" },
                            int_spoken,
                            qty
                        )
                    });
                });
            }
        }
    }

    // Branch 2: Decimal with period: "12.5", "0.5006", etc.
    if int_end < text.len() && text[int_end] == '.' {
        let frac_start = int_end + 1;
        let mut frac_end = frac_start;
        while frac_end < text.len() && text[frac_end].is_ascii_digit() {
            frac_end += 1;
        }
        if frac_end > frac_start {
            // Can end at frac_end, or continue with space + quantity
            let frac_spoken = crate::lang::ur::lazy::tag(|| {
                let frac_str: String = text[frac_start..frac_end].iter().collect();
                digitwise(&frac_str).unwrap()
            });

            // 2a. Decimal without quantity
            enumerate_without_leading_zeros(int_chars, &mut |int_spoken, internal_cost| {
                let c = cost(1.1) + internal_cost;
                sink.push(frac_end, c, 0, || {
                    format!(
                        "decimal {{ {}integer_part: \"{}\" fractional_part: \"{}\" }}",
                        if neg { "negative: \"true\" " } else { "" },
                        int_spoken,
                        frac_spoken
                    )
                });
            });

            // The separator before the quantity is inserted on the output side.
            {
                let after_space = frac_end;
                for &qty in QUANTITIES {
                    if let Some(len) = prefix_len(&text[after_space..], qty) {
                        let cur_end = after_space + len;
                        enumerate_without_leading_zeros(int_chars, &mut |int_spoken, internal_cost| {
                            let c = cost(1.1) + internal_cost;
                            sink.push(cur_end, c, 0, || {
                                format!(
                                    "decimal {{ {}integer_part: \"{}\" fractional_part: \"{}\" quantity: \"{}\" }}",
                                    if neg { "negative: \"true\" " } else { "" },
                                    int_spoken,
                                    frac_spoken,
                                    qty
                                )
                            });
                        });
                    }
                }
            }
        }
    }
}

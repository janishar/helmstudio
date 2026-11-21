//! Arabic decimal classifier and verbalizer.
//! Matching `ar/taggers/decimal.py` and `ar/verbalizers/decimal.py`.

use super::cardinal::{parse_cardinal_span, spell_cardinal, zero_suffix_cost};
use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::{cost, is_nemo_space};

const QUANTITIES: &[&str] = &["مليون", "مليار", "تريليون", "بليار"];

pub struct DecimalClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for DecimalClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }
        let mut idx = start;
        let mut neg = false;
        if idx < text.len() && (text[idx] == '-' || text[idx] == '−') {
            neg = true;
            idx += 1;
            if idx < text.len() && text[idx] == ' ' {
                idx += 1;
            }
        }
        if idx >= text.len() {
            return;
        }

        let int_start = idx;
        while idx < text.len() {
            let ch = char_at(text, idx);
            if ch != '.' && ch != ',' && !ch.is_ascii_digit() {
                break;
            }
            if ch == '.' || ch == ',' {
                let integer =
                    if idx == int_start { Some((0, 0.0)) } else { parse_cardinal_span(text, int_start, idx, false) };
                if let Some((_, integer_cost)) = integer {
                    let mut end = idx + 1;
                    let mut fractional = 0u64;
                    while end < text.len() && end - idx <= 4 && char_at(text, end).is_ascii_digit() {
                        fractional = fractional * 10 + (char_at(text, end) as u8 - b'0') as u64;
                        end += 1;
                        let base = 1.10 + integer_cost + zero_suffix_cost(fractional);
                        let empty = idx == int_start;
                        let mut emit = |candidate_end: usize, extra: f64, quantity: bool| {
                            if !token_end(text, candidate_end, self.punct) {
                                return;
                            }
                            let rank = (if neg { 1 } else { 0 }) | (if quantity { 2 } else { 0 });
                            out.push(Candidate {
                                end: candidate_end,
                                cost: cost(base + extra),
                                rank,
                                tag: String::new(),
                            });
                            if empty {
                                out.push(Candidate {
                                    end: candidate_end,
                                    cost: cost(base + extra + 0.001),
                                    rank: rank | 4,
                                    tag: String::new(),
                                });
                            }
                        };
                        emit(end, 0.0, false);
                        for (q_start, weight) in [(end, 0.0), (end + 1, -0.1)] {
                            if q_start > end && !text.get(end).is_some_and(|c| is_nemo_space(*c)) {
                                continue;
                            }
                            for &q in QUANTITIES {
                                let len = q.chars().count();
                                if q_start + len <= text.len()
                                    && text[q_start..q_start + len].iter().copied().eq(q.chars())
                                {
                                    emit(q_start + len, weight, true);
                                }
                            }
                        }
                    }
                }
            }
            idx += 1;
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut idx = start;
        let mut neg = false;
        if idx < text.len() && (text[idx] == '-' || text[idx] == '−') {
            neg = true;
            idx += 1;
            if idx < text.len() && text[idx] == ' ' {
                idx += 1;
            }
        }

        let int_start = idx;
        let mut frac_end = candidate.end;
        let mut q_str: Option<&'static str> = None;
        if candidate.rank & 2 != 0 {
            for &q in QUANTITIES {
                let len = q.chars().count();
                if len <= frac_end && text[frac_end - len..frac_end].iter().copied().eq(q.chars()) {
                    frac_end -= len;
                    q_str = Some(q);
                    if frac_end > int_start && is_nemo_space(text[frac_end - 1]) {
                        frac_end -= 1;
                    }
                    break;
                }
            }
        }
        let mut frac_start = frac_end;
        while frac_start > int_start && char_at(text, frac_start - 1).is_ascii_digit() {
            frac_start -= 1;
        }
        let sep_idx = frac_start - 1;

        let int_str: String = if sep_idx > int_start {
            let mut val: u64 = 0;
            for i in int_start..sep_idx {
                let c = char_at(text, i);
                if c.is_ascii_digit() {
                    val = val.saturating_mul(10).saturating_add((c as u8 - b'0') as u64);
                }
            }
            spell_cardinal(val)
        } else if candidate.rank & 4 != 0 {
            "0".to_string()
        } else {
            String::new()
        };

        let frac_start = sep_idx + 1;
        let frac_digits = frac_end - frac_start;
        let mut frac_val: u64 = 0;
        for i in frac_start..frac_end {
            frac_val = frac_val * 10 + (char_at(text, i) as u8 - b'0') as u64;
        }

        let frac_spoken = spell_cardinal(frac_val);
        let denom_suffix = match frac_digits {
            1 => "من عشرة",
            2 => "من مئة",
            3 => "من ألف",
            _ => "من عشرة آلاف",
        };

        let mut tag = String::with_capacity(64);
        tag.push_str("decimal { ");
        if neg {
            tag.push_str("negative: \"true\" ");
        }
        tag.push_str("integer_part: \"");
        tag.push_str(&int_str);
        tag.push_str("\" fractional_part: \"و");
        tag.push_str(&frac_spoken);
        tag.push(' ');
        tag.push_str(denom_suffix);
        tag.push('"');
        if let Some(q) = q_str {
            tag.push_str(" quantity: \"");
            tag.push_str(q);
            tag.push('"');
        }
        tag.push_str(" }");
        candidate.tag = tag;
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let neg = match f.opt("negative")? {
        Some("true") => true,
        None => false,
        _ => return None,
    };
    let int_part = f.take("integer_part")?;
    let frac_part = f.take("fractional_part")?;
    let quantity = f.take("quantity");
    if !f.done() || int_part.is_empty() || frac_part.is_empty() || quantity.is_some_and(str::is_empty) {
        return None;
    }
    let mut out = String::with_capacity(int_part.len() + frac_part.len() + 16);
    if neg {
        out.push_str("سالب ");
    }
    out.push_str(int_part);
    out.push(' ');
    out.push_str(frac_part);
    if let Some(q) = quantity {
        if !q.is_empty() {
            out.push(' ');
            out.push_str(q);
        }
    }
    Some(out)
}

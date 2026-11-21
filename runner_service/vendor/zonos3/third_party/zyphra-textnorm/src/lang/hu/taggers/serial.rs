// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use crate::lang::hu::graph_utils::{is_hu_alpha, is_hu_upper};
use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::taggers::ordinal;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};

pub const WEIGHT: Cost = cost(1.12);

const DIGITS: [&str; 10] = ["nulla", "egy", "kettő", "három", "négy", "öt", "hat", "hét", "nyolc", "kilenc"];

fn spell_serial_digits(s: &str) -> String {
    if s == "0" {
        return "nulla".to_string();
    }
    if s.starts_with('0') || s.len() >= 4 {
        let mut words = Vec::new();
        for b in s.bytes() {
            if b >= b'0' && b <= b'9' {
                words.push(DIGITS[(b - b'0') as usize]);
            }
        }
        return words.join(" ");
    }
    if let Ok(n) = s.parse::<u64>() {
        let mut sp = String::new();
        cardinal::spell(n, &mut sp);
        return sp;
    }
    s.to_string()
}

fn spell_alpha_part(s: &str) -> String {
    // 1-3 uppercase letters are spelled letter-by-letter
    let all_upper = s.chars().all(is_hu_upper);
    if all_upper && s.chars().count() <= 3 {
        let chars: Vec<String> = s.chars().map(|c| c.to_string()).collect();
        return chars.join(" ");
    }
    s.to_string()
}

fn versions(
    text: &[char],
    pos: usize,
    prefix: Option<char>,
    parts: &mut [(usize, usize); 4],
    count: usize,
    out: &mut Sink,
) {
    if pos >= text.len() || !text[pos].is_ascii_digit() {
        return;
    }
    let mut end = pos;
    while end < text.len() && text[end].is_ascii_digit() {
        end += 1;
        if text[pos] != '0' && end - pos > 3 {
            break;
        }
        parts[count] = (pos, end);
        let length = count + 1;
        if (prefix.is_some() && length >= 2) || (prefix.is_none() && length == 3) {
            let witness = *parts;
            let weight = if prefix.is_some() { WEIGHT - cost(0.05) } else { WEIGHT };
            out.push(end, weight, 0, || {
                let mut value = String::new();
                if let Some(c) = prefix {
                    value.push(c);
                    value.push('\u{a0}');
                }
                for (index, &(begin, finish)) in witness[..length].iter().enumerate() {
                    if index != 0 {
                        value.push_str("\u{a0}pont\u{a0}");
                    }
                    let digits: String = text[begin..finish].iter().collect();
                    value.push_str(&spell_serial_digits(&digits).replace(' ', "\u{a0}"));
                }
                format!("name: \"{value}\"")
            });
        }
        let max_parts = if prefix.is_some() { 4 } else { 3 };
        if length < max_parts && text.get(end) == Some(&'.') {
            versions(text, end + 1, prefix, parts, length, out);
        }
    }
}

pub struct Serial;

impl Lazy for Serial {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }

        // 1. Variable powers: "x^2", "y^3", "x^4"
        let c0 = text[start];
        if is_hu_alpha(c0) || matches!(c0, 'α' | 'β' | 'γ' | 'δ' | 'π' | 'θ' | 'λ' | 'μ' | 'σ' | 'ω') {
            let mut v_end = start + 1;
            while v_end < text.len() && is_hu_alpha(text[v_end]) && (v_end - start) <= 4 {
                v_end += 1;
            }
            if v_end < text.len() && text[v_end] == '^' {
                let exp_start = v_end + 1;
                let mut exp_end = exp_start;
                while exp_end < text.len() && text[exp_end].is_ascii_digit() {
                    exp_end += 1;
                }
                if exp_end > exp_start {
                    let digits = &text[exp_start..exp_end];
                    let exp_num =
                        digits.iter().try_fold(0u64, |n, c| n.checked_mul(10)?.checked_add(c.to_digit(10)? as u64));
                    if let Some(exp_num) = exp_num {
                        out.push(exp_end, WEIGHT - cost(0.05), 0, || {
                            let base: String = text[start..v_end].iter().collect();
                            if digits == ['2'] {
                                return format!("name: \"{base} a négyzeten\"");
                            }
                            if digits == ['3'] {
                                return format!("name: \"{base} a köbön\"");
                            }
                            let mut sp = String::new();
                            cardinal::spell(exp_num, &mut sp);
                            let ord = ordinal::cardinal_to_ordinal(&sp);
                            let article = if digits == ['1'] || digits == ['5'] || digits == ['1', '0', '0', '0'] {
                                "az"
                            } else {
                                "a"
                            };
                            format!("name: \"{base} {article} {ord} hatványon\"")
                        });
                        return;
                    }
                }
            }
        }

        // Version components are zero, up to three nonzero-leading digits,
        // or an arbitrary-length leading-zero digit sequence.
        let prefix = matches!(text[start], 'v' | 'V').then_some(text[start]);
        let ver_start = start + usize::from(prefix.is_some());
        let required_dots = if prefix.is_some() { 1 } else { 2 };
        let mut dots = 0;
        for &c in &text[ver_start..] {
            if c == '.' {
                dots += 1;
            } else if !c.is_ascii_digit() {
                break;
            }
            if dots == required_dots {
                break;
            }
        }
        if dots == required_dots {
            versions(text, ver_start, prefix, &mut [(0, 0); 4], 0, out);
        }

        // 3. Alphanumeric serials: "A4", "BA249", "COVID-19"
        // Either alpha then num, or num then alpha
        let first_is_alpha = is_hu_alpha(text[start]);
        let first_is_digit = text[start].is_ascii_digit();

        if first_is_alpha || first_is_digit {
            let mut p = start;
            let mut expecting_alpha = first_is_alpha;
            let mut step_count = 0;

            while p < text.len() {
                if expecting_alpha {
                    let a_start = p;
                    while p < text.len() && is_hu_alpha(text[p]) {
                        p += 1;
                    }
                    if p == a_start {
                        break;
                    }
                    step_count += 1;
                    expecting_alpha = false;
                } else {
                    let d_start = p;
                    while p < text.len() && text[p].is_ascii_digit() {
                        p += 1;
                    }
                    if p == d_start {
                        break;
                    }
                    step_count += 1;
                    expecting_alpha = true;
                }

                // Source separators are an inserted space or a consumed '-' or '/'.
                if p < text.len() && (text[p] == '-' || text[p] == '/') {
                    // Lookahead: next char must be alpha or digit
                    if p + 1 < text.len() && (is_hu_alpha(text[p + 1]) || text[p + 1].is_ascii_digit()) {
                        p += 1;
                    } else {
                        break;
                    }
                }
            }

            // Must contain at least one alpha and at least one digit
            if step_count >= 2 {
                let end_pos = p;
                out.push(end_pos, WEIGHT, 0, || {
                    let mut tokens = Vec::new();
                    let mut at = start;
                    let mut alpha = first_is_alpha;
                    while at < end_pos {
                        let begin = at;
                        while at < end_pos && if alpha { is_hu_alpha(text[at]) } else { text[at].is_ascii_digit() } {
                            at += 1;
                        }
                        if begin == at {
                            break;
                        }
                        let part: String = text[begin..at].iter().collect();
                        tokens.push(if alpha { spell_alpha_part(&part) } else { spell_serial_digits(&part) });
                        alpha = !alpha;
                        if at < end_pos && matches!(text[at], '-' | '/') {
                            at += 1;
                        }
                    }
                    format!("name: \"{}\"", tokens.join(" "))
                });
            }
        }
    }
}

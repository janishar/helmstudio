//! Arabic serial, version, power, and alphanumeric code classifier.
//! Matching `ar/taggers/serial.py`.

use super::cardinal::spell_cardinal;
use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;

const LETTERS_MAP: [&str; 26] = [
    "إيه",
    "بي",
    "سي",
    "دي",
    "إي",
    "إف",
    "جي",
    "إتش",
    "آي",
    "جيه",
    "كيه",
    "إل",
    "إم",
    "إن",
    "أو",
    "بي",
    "كيو",
    "آر",
    "إس",
    "تي",
    "يو",
    "في",
    "دبليو",
    "إكس",
    "واي",
    "زد",
];

const DIGIT_WORDS: [&str; 10] = ["صفر", "واحد", "اثنان", "ثلاثة", "أربعة", "خمسة", "ستة", "سبعة", "ثمانية", "تسعة"];

const POWER_VARS: &[char] = &['α', 'β', 'γ', 'δ', 'θ', 'λ', 'μ', 'π', 'σ', 'ω', 'س', 'ص', 'ع', 'ن'];

const UNIT_BASES: &[&str] = &["m", "cm", "mm", "km", "dm", "م", "سم", "مم", "كم"];

pub fn spell_alpha_seg(s: &str) -> String {
    if s.len() <= 3 && s.chars().all(|c| c.is_ascii_alphabetic()) {
        let mut parts = Vec::new();
        for c in s.chars() {
            let idx = (c.to_ascii_lowercase() as u8 - b'a') as usize;
            parts.push(LETTERS_MAP[idx]);
        }
        parts.join(" ")
    } else {
        s.to_string()
    }
}

pub fn spell_serial_num(s: &str) -> String {
    if s.starts_with('0') && s.len() > 1 {
        // leading zero: digit by digit
        let mut parts = Vec::new();
        for b in s.bytes() {
            if b.is_ascii_digit() {
                parts.push(DIGIT_WORDS[(b - b'0') as usize]);
            }
        }
        parts.join(" ")
    } else if s.len() >= 4 {
        // long num: digit by digit
        let mut parts = Vec::new();
        for b in s.bytes() {
            if b.is_ascii_digit() {
                parts.push(DIGIT_WORDS[(b - b'0') as usize]);
            }
        }
        parts.join(" ")
    } else {
        let v: u64 = s.parse().unwrap_or(0);
        spell_cardinal(v)
    }
}

pub struct SerialClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for SerialClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }
        let first = char_at(text, start);
        if !first.is_ascii_alphanumeric() && !matches!(first, '-' | '/') && !POWER_VARS.contains(&text[start]) {
            return;
        }

        // Branch 1: Version prefix "v" / "V" + number + dot + number...
        if (text[start] == 'v' || text[start] == 'V')
            && start + 1 < text.len()
            && char_at(text, start + 1).is_ascii_digit()
        {
            let mut idx = start + 1;
            let mut dots = 0;
            while idx < text.len() {
                let c = char_at(text, idx);
                if c.is_ascii_digit() {
                    idx += 1;
                } else if c == '.' && idx + 1 < text.len() && char_at(text, idx + 1).is_ascii_digit() {
                    dots += 1;
                    idx += 1;
                } else {
                    break;
                }
            }
            if dots >= 1 && dots <= 3 && token_end(text, idx, self.punct) {
                out.push(Candidate { end: idx, cost: cost(2.0), rank: 0, tag: String::new() });
                return;
            }
        }

        // Branch 2: Multi-dot version (e.g. 3.11.2) - at least 2 dots
        if char_at(text, start).is_ascii_digit() {
            let mut idx = start;
            let mut dots = 0;
            while idx < text.len() {
                let c = char_at(text, idx);
                if c.is_ascii_digit() {
                    idx += 1;
                } else if c == '.' && idx + 1 < text.len() && char_at(text, idx + 1).is_ascii_digit() {
                    dots += 1;
                    idx += 1;
                } else {
                    break;
                }
            }
            if dots >= 2 && dots <= 4 && token_end(text, idx, self.punct) {
                out.push(Candidate { end: idx, cost: cost(2.0), rank: 1, tag: String::new() });
                return;
            }
        }

        // Branch 3: Variable powers: x^2, y^3, x^4, x^-1, x², y³
        let mut v_end = start;
        while v_end < text.len() && text[v_end].is_ascii_alphabetic() && v_end - start <= 3 {
            v_end += 1;
        }
        let is_var_base =
            (v_end > start && v_end - start <= 3) || (start + 1 <= text.len() && POWER_VARS.contains(&text[start]));
        let base_end = if v_end > start {
            v_end
        } else if is_var_base {
            start + 1
        } else {
            start
        };

        if is_var_base && base_end < text.len() {
            // Check superscript ² / ³ without allocating a temporary base.
            if (text[base_end] == '²' || text[base_end] == '³')
                && !UNIT_BASES.iter().any(|unit| text[start..base_end].iter().copied().eq(unit.chars()))
            {
                let end = base_end + 1;
                if token_end(text, end, self.punct) {
                    out.push(Candidate { end, cost: cost(2.0), rank: 2, tag: String::new() });
                    return;
                }
            }

            // Check '^'
            if text[base_end] == '^' {
                let mut exp_idx = base_end + 1;
                let mut exp_neg = false;
                if exp_idx < text.len() && text[exp_idx] == '-' {
                    exp_neg = true;
                    exp_idx += 1;
                }
                let e_start = exp_idx;
                while exp_idx < text.len() && char_at(text, exp_idx).is_ascii_digit() && exp_idx - e_start <= 3 {
                    exp_idx += 1;
                }
                if exp_idx > e_start && token_end(text, exp_idx, self.punct) {
                    out.push(Candidate {
                        end: exp_idx,
                        cost: cost(2.0),
                        rank: if exp_neg { 4 } else { 3 },
                        tag: String::new(),
                    });
                    return;
                }
            }
        }

        // Branch 4: Alphanumeric code (A4, BA249, COVID-19)
        // Must contain both alpha and digit, separated by optional '-' or '/'
        let mut idx = start;
        let mut has_alpha = false;
        let mut has_digit = false;
        while idx < text.len() {
            let c = char_at(text, idx);
            if c.is_ascii_alphabetic() {
                has_alpha = true;
                idx += 1;
            } else if c.is_ascii_digit() {
                has_digit = true;
                idx += 1;
            } else if (c == '-' || c == '/') && idx + 1 < text.len() && (char_at(text, idx + 1).is_ascii_alphanumeric())
            {
                idx += 1;
            } else {
                break;
            }
        }
        if has_alpha && has_digit && token_end(text, idx, self.punct) {
            out.push(Candidate { end: idx, cost: cost(2.0), rank: 5, tag: String::new() });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        // Version prefix: "v2.0"
        if candidate.rank == 0 {
            let mut num_str = String::new();
            for i in start + 1..candidate.end {
                num_str.push(char_at(text, i));
            }
            let parts: Vec<&str> = num_str.split('.').collect();
            let mut spoken_parts: Vec<String> = Vec::new();
            for p in parts {
                spoken_parts.push(spell_serial_num(p));
            }
            let full_spoken = format!("في {}", spoken_parts.join(" نقطة ")).replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Multi-dot version: "3.11.2"
        if candidate.rank == 1 {
            let mut num_str = String::new();
            for i in start..candidate.end {
                num_str.push(char_at(text, i));
            }
            let parts: Vec<&str> = num_str.split('.').collect();
            let mut spoken_parts: Vec<String> = Vec::new();
            for p in parts {
                spoken_parts.push(spell_serial_num(p));
            }
            let full_spoken = spoken_parts.join(" نقطة ").replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Variable powers: x² / y³
        if candidate.rank == 2 {
            let base_str: String = text[start..candidate.end - 1].iter().collect();
            let sup = text[candidate.end - 1];
            let exp_word = if sup == '²' { "تربيع" } else { "تكعيب" };
            let full_spoken = format!("{base_str} {exp_word}").replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Variable powers with '^': x^2, x^3, x^4, x^-1
        if candidate.rank == 3 || candidate.rank == 4 {
            let s: String = (start..candidate.end).map(|i| char_at(text, i)).collect();
            let caret_pos = s.find('^').unwrap_or(1);
            let base_part = &s[..caret_pos];
            let exp_part = &s[caret_pos + 1..];

            let exp_spoken = if exp_part == "2" {
                "تربيع".to_string()
            } else if exp_part == "3" {
                "تكعيب".to_string()
            } else if exp_part.starts_with('-') {
                let v: u64 = exp_part[1..].parse().unwrap_or(1);
                format!("أس سالب {}", spell_cardinal(v))
            } else {
                let v: u64 = exp_part.parse().unwrap_or(1);
                format!("أس {}", spell_cardinal(v))
            };

            let full_spoken = format!("{base_part} {exp_spoken}").replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Alphanumeric code: A4, BA249, COVID-19
        let s: String = (start..candidate.end).map(|i| char_at(text, i)).collect();
        let mut pieces: Vec<String> = Vec::new();
        let mut cur = String::new();
        let mut cur_is_digit = false;
        let mut cur_is_alpha = false;

        for c in s.chars() {
            if c == '-' || c == '/' {
                if !cur.is_empty() {
                    if cur_is_digit {
                        pieces.push(spell_serial_num(&cur));
                    } else if cur_is_alpha {
                        pieces.push(spell_alpha_seg(&cur));
                    }
                    cur.clear();
                    cur_is_digit = false;
                    cur_is_alpha = false;
                }
                pieces.push(if c == '-' { " " } else { " شرطة مائلة " }.to_string());
            } else if c.is_ascii_digit() {
                if cur_is_alpha && !cur.is_empty() {
                    pieces.push(spell_alpha_seg(&cur));
                    cur.clear();
                    cur_is_alpha = false;
                }
                cur_is_digit = true;
                cur.push(c);
            } else if c.is_ascii_alphabetic() {
                if cur_is_digit && !cur.is_empty() {
                    pieces.push(spell_serial_num(&cur));
                    cur.clear();
                    cur_is_digit = false;
                }
                cur_is_alpha = true;
                cur.push(c);
            }
        }
        if !cur.is_empty() {
            if cur_is_digit {
                pieces.push(spell_serial_num(&cur));
            } else if cur_is_alpha {
                pieces.push(spell_alpha_seg(&cur));
            }
        }

        let joined = pieces.join(" ");
        let cleaned: String = joined.split_whitespace().collect::<Vec<&str>>().join(" ");
        let full_spoken = cleaned.replace(' ', "\u{a0}");
        candidate.tag = format!("name: \"{full_spoken}\"");
    }
}

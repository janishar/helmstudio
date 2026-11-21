//! Arabic telephone classifier and verbalizer.
//! Matching `ar/taggers/telephone.py` and `ar/verbalizers/telephone.py`.

use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;

const DIGIT_WORDS: [&str; 10] = ["صفر", "واحد", "اثنان", "ثلاثة", "أربعة", "خمسة", "ستة", "سبعة", "ثمانية", "تسعة"];

pub fn spell_digits(s: &str) -> String {
    let mut parts: Vec<&str> = Vec::with_capacity(s.len());
    for b in s.bytes() {
        if b.is_ascii_digit() {
            parts.push(DIGIT_WORDS[(b - b'0') as usize]);
        }
    }
    parts.join(" ")
}

pub struct TelephoneClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for TelephoneClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }

        // Branch 1: Country code starting with '+'
        if text[start] == '+' {
            let mut idx = start + 1;
            let cc_start = idx;
            while idx < text.len() && char_at(text, idx).is_ascii_digit() && idx - cc_start <= 3 {
                idx += 1;
            }
            let cc_len = idx - cc_start;
            if cc_len >= 1 && cc_len <= 3 && idx < text.len() && (text[idx] == ' ' || text[idx] == '-') {
                idx += 1;
                let mut d_count = 0;
                while idx < text.len() {
                    let c = char_at(text, idx);
                    if c.is_ascii_digit() {
                        d_count += 1;
                        idx += 1;
                    } else if (c == ' ' || c == '-' || c == '.')
                        && idx + 1 < text.len()
                        && char_at(text, idx + 1).is_ascii_digit()
                    {
                        idx += 1;
                    } else {
                        break;
                    }
                }
                if d_count >= 4 && d_count <= 14 && token_end(text, idx, self.punct) {
                    out.push(Candidate { end: idx, cost: cost(1.10), rank: 0, tag: String::new() });
                }
            }
        }

        // Branch 2: Local number starting with '0' (9-12 digits)
        if char_at(text, start) == '0' {
            let mut idx = start;
            let mut d_count = 0;
            while idx < text.len() {
                let c = char_at(text, idx);
                if c.is_ascii_digit() {
                    d_count += 1;
                    idx += 1;
                } else if (c == ' ' || c == '-' || c == '.')
                    && idx + 1 < text.len()
                    && char_at(text, idx + 1).is_ascii_digit()
                {
                    idx += 1;
                } else {
                    break;
                }
            }
            if d_count >= 9 && d_count <= 12 && token_end(text, idx, self.punct) {
                out.push(Candidate { end: idx, cost: cost(1.10), rank: 1, tag: String::new() });
            }
        }

        // Branch 3: Area code in parentheses: (021) 555-1234 or (212) 555-1234
        if text[start] == '(' {
            let mut idx = start + 1;
            let a_start = idx;
            while idx < text.len() && char_at(text, idx).is_ascii_digit() && idx - a_start <= 4 {
                idx += 1;
            }
            if idx < text.len() && text[idx] == ')' {
                idx += 1; // skip ')'
                if idx < text.len() && (text[idx] == ' ' || text[idx] == '-') {
                    idx += 1;
                }
                let mut d_count = 0;
                while idx < text.len() {
                    let c = char_at(text, idx);
                    if c.is_ascii_digit() {
                        d_count += 1;
                        idx += 1;
                    } else if (c == ' ' || c == '-' || c == '.')
                        && idx + 1 < text.len()
                        && char_at(text, idx + 1).is_ascii_digit()
                    {
                        idx += 1;
                    } else {
                        break;
                    }
                }
                if d_count >= 4 && d_count <= 8 && token_end(text, idx, self.punct) {
                    out.push(Candidate { end: idx, cost: cost(1.10), rank: 2, tag: String::new() });
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut tag = String::with_capacity(128);
        tag.push_str("telephone { ");

        if text[start] == '+' {
            let mut idx = start + 1;
            let mut cc_digits = String::new();
            while idx < candidate.end && char_at(text, idx).is_ascii_digit() && cc_digits.len() < 3 {
                cc_digits.push(char_at(text, idx));
                idx += 1;
            }
            if idx < candidate.end && (text[idx] == ' ' || text[idx] == '-') {
                idx += 1;
            }
            let mut num_digits = String::new();
            for i in idx..candidate.end {
                let c = char_at(text, i);
                if c.is_ascii_digit() {
                    num_digits.push(c);
                }
            }
            tag.push_str("country_code: \"زائد ");
            tag.push_str(&spell_digits(&cc_digits));
            tag.push_str("\" number_part: \"");
            tag.push_str(&spell_digits(&num_digits));
            tag.push_str("\" preserve_order: true }");
            candidate.tag = tag;
            return;
        }

        // Local or Parentheses
        let mut num_digits = String::new();
        for i in start..candidate.end {
            let c = char_at(text, i);
            if c.is_ascii_digit() {
                num_digits.push(c);
            }
        }
        tag.push_str("number_part: \"");
        tag.push_str(&spell_digits(&num_digits));
        tag.push_str("\" preserve_order: true }");
        candidate.tag = tag;
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let cc = f.take("country_code");
    let num = f.take("number_part")?;
    f.preserve_order();
    if !f.done() || num.is_empty() {
        return None;
    }

    let mut out = String::with_capacity(num.len() + 32);
    if let Some(c) = cc {
        if !c.is_empty() {
            out.push_str(c);
            out.push(' ');
        }
    }
    out.push_str(num);
    Some(out)
}

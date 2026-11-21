//! Arabic cardinal classifier and verbalizer.
//! Matching `ar/taggers/cardinal.py` and `ar/verbalizers/cardinal.py`.

use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;

const DIGITS: [&str; 10] = ["صفر", "واحد", "اثنان", "ثلاثة", "أربعة", "خمسة", "ستة", "سبعة", "ثمانية", "تسعة"];

const TEENS: [&str; 10] = [
    "عشرة",
    "احد عشر",
    "اثنا عشر",
    "ثلاثة عشر",
    "اربعة عشر",
    "خمسة عشر",
    "ستة عشر",
    "سبعة عشر",
    "ثمانية عشر",
    "تسعة عشر",
];

const TENS: [&str; 10] = ["", "", "عشرون", "ثلاثون", "أربعون", "خمسون", "ستون", "سبعون", "ثمانون", "تسعون"];

const HUNDRED_ROOTS: [&str; 10] = ["", "", "", "ثلاث", "أربع", "خمس", "ست", "سبع", "ثمان", "تسع"];

pub fn spell_cardinal(n: u64) -> String {
    if n == 0 {
        return "صفر".to_string();
    }
    let mut parts: Vec<String> = Vec::new();

    let billions = n / 1_000_000_000;
    let millions = (n / 1_000_000) % 1_000;
    let thousands = (n / 1_000) % 1_000;
    let remainder = n % 1_000;

    if billions > 0 {
        parts.push(spell_magnitude(billions, "مليار", "ملياران", "مليارات"));
    }
    if millions > 0 {
        parts.push(spell_magnitude(millions, "مليون", "مليونان", "ملايين"));
    }
    if thousands > 0 {
        parts.push(spell_magnitude(thousands, "ألف", "ألفان", "\u{0627}\u{064e}\u{0644}\u{0627}\u{0641}"));
    }
    if remainder > 0 {
        parts.push(spell_under_1000(remainder));
    }

    parts.join(" و")
}

fn spell_magnitude(count: u64, singular: &str, dual: &str, plural: &str) -> String {
    match count {
        1 => singular.to_string(),
        2 => dual.to_string(),
        3..=9 => format!("{} {}", DIGITS[count as usize], plural),
        10 => format!("عشرة {}", plural),
        200 => format!("مئتا {}", singular),
        _ => format!("{} {}", spell_under_1000(count), singular),
    }
}

pub fn spell_under_1000(n: u64) -> String {
    let mut parts: Vec<String> = Vec::new();
    let hundreds = n / 100;
    let rem = n % 100;

    if hundreds > 0 {
        match hundreds {
            1 => parts.push("مئة".to_string()),
            2 => parts.push("مئتان".to_string()),
            3..=9 => parts.push(format!("{} مئة", HUNDRED_ROOTS[hundreds as usize])),
            _ => {}
        }
    }

    if rem > 0 {
        if rem < 10 {
            parts.push(DIGITS[rem as usize].to_string());
        } else if rem < 20 {
            parts.push(TEENS[(rem - 10) as usize].to_string());
        } else {
            let t = rem / 10;
            let u = rem % 10;
            if u == 0 {
                parts.push(TENS[t as usize].to_string());
            } else {
                parts.push(format!("{} و{}", DIGITS[u as usize], TENS[t as usize]));
            }
        }
    }

    parts.join(" و")
}

// The source adds 0.001 when a hundreds or a small-thousands graph
// deletes its all-zero suffix. Magnitude count 200 has a separate path.
pub fn zero_suffix_cost(mut value: u64) -> f64 {
    let original = value;
    let mut groups = 0;
    let mut index = 0;
    while value != 0 {
        let group = value % 1000;
        if index == 0 {
            if group >= 100 && group % 100 == 0 {
                groups += 1;
            }
        } else if index == 1 && original < 10_000 {
            if group != 0 && original % 1000 == 0 {
                groups += 1;
            }
        } else if group >= 100 && group % 100 == 0 && group != 200 {
            groups += 1;
        }
        value /= 1000;
        index += 1;
    }
    groups as f64 * 0.001
}

pub fn parse_cardinal_span(text: &[char], start: usize, end: usize, leading_zeros: bool) -> Option<(u64, f64)> {
    if start >= end {
        return None;
    }
    let mut value = 0u64;
    let mut digits = 0usize;
    let mut leading = 0usize;
    let mut groups = 0usize;
    let mut group_len = 0usize;
    let mut first_group = 0usize;
    let mut first_zero_group = false;
    for i in start..end {
        let c = char_at(text, i);
        if c == ',' {
            if groups == 0 {
                if !(1..=3).contains(&group_len) || leading != 0 {
                    return None;
                }
                first_group = group_len;
            } else if group_len != 3 {
                return None;
            }
            groups += 1;
            group_len = 0;
        } else if c.is_ascii_digit() {
            if digits == leading && c == '0' {
                leading += 1;
            }
            digits += 1;
            group_len += 1;
            value = value.checked_mul(10)?.checked_add((c as u8 - b'0') as u64)?;
            if groups == 1 && group_len == 3 {
                first_zero_group = value % 1000 == 0;
            }
        } else {
            return None;
        }
    }
    if groups == 0 {
        if !leading_zeros && leading != 0 && digits != 1 {
            return None;
        }
        let significant = digits - leading;
        if significant > 12 || (leading != 0 && significant > 4) {
            return None;
        }
        return Some((value, zero_suffix_cost(value)));
    }
    if group_len != 3 || groups > 3 || first_group == 0 || (groups == 1 && !first_zero_group) {
        return None;
    }
    Some((value, zero_suffix_cost(value) - 0.01))
}

pub struct CardinalClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for CardinalClassifier<'_> {
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

        let num_start = idx;
        // Check for comma-grouped format:
        // lead_group (1..3 digits) + (',' + 3 digits)*
        let mut d_count = 0;
        let mut temp_idx = num_start;
        while temp_idx < text.len() && char_at(text, temp_idx).is_ascii_digit() {
            d_count += 1;
            temp_idx += 1;
        }
        if d_count >= 1
            && d_count <= 3
            && char_at(text, num_start) != '0'
            && temp_idx < text.len()
            && char_at(text, temp_idx) == ','
        {
            let mut g_idx = temp_idx;
            let mut groups = 0;
            while groups < 3 && g_idx < text.len() && char_at(text, g_idx) == ',' {
                g_idx += 1;
                let grp_start = g_idx;
                while g_idx < text.len() && char_at(text, g_idx).is_ascii_digit() && g_idx - grp_start < 3 {
                    g_idx += 1;
                }
                if g_idx - grp_start != 3 {
                    break;
                }
                groups += 1;
                let zero_group = char_at(text, grp_start) == '0'
                    && char_at(text, grp_start + 1) == '0'
                    && char_at(text, grp_start + 2) == '0';
                if (groups >= 2 || zero_group) && token_end(text, g_idx, self.punct) {
                    let rank = if neg { 1 } else { 0 };
                    let value = (num_start..g_idx)
                        .map(|i| char_at(text, i))
                        .filter(char::is_ascii_digit)
                        .fold(0u64, |n, c| n * 10 + (c as u8 - b'0') as u64);
                    out.push(Candidate {
                        end: g_idx,
                        cost: cost(1.10 - 0.01 + zero_suffix_cost(value)),
                        rank,
                        tag: String::new(),
                    });
                }
            }
        }

        {
            let mut significant = 0;
            let mut leading = 0;
            let mut value = 0u64;
            while idx < text.len() && char_at(text, idx).is_ascii_digit() {
                value = value.saturating_mul(10).saturating_add((char_at(text, idx) as u8 - b'0') as u64);
                if significant == 0 && char_at(text, idx) == '0' {
                    leading += 1;
                } else {
                    significant += 1;
                }
                idx += 1;
                if significant <= 12 && (leading == 0 || significant <= 4) && token_end(text, idx, self.punct) {
                    let rank = if neg { 1 } else { 0 };
                    out.push(Candidate {
                        end: idx,
                        cost: cost(1.10 + zero_suffix_cost(value)),
                        rank,
                        tag: String::new(),
                    });
                }
            }
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
        let mut val: u64 = 0;
        for i in idx..candidate.end {
            let c = char_at(text, i);
            if c.is_ascii_digit() {
                val = val.saturating_mul(10).saturating_add((c as u8 - b'0') as u64);
            }
        }
        let spoken = spell_cardinal(val);
        let mut tag = String::with_capacity(32 + spoken.len());
        tag.push_str("cardinal { ");
        if neg {
            tag.push_str("negative: \"true\" ");
        }
        tag.push_str("integer: \"");
        tag.push_str(&spoken);
        tag.push_str("\" }");
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
    let int_val = f.take("integer")?;
    if !f.done() || int_val.is_empty() {
        return None;
    }
    let mut out = String::with_capacity(int_val.len() + 8);
    if neg {
        out.push_str("سالب ");
    }
    out.push_str(int_val);
    Some(out)
}

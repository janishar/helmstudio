// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese contextual Roman numerals.

use super::super::graph_utils::{WEIGHT_ROMAN, cardinal_cost, spell_cardinal_digits};
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;
use std::collections::HashMap;
use std::sync::LazyLock;

pub struct Roman;
crate::labels!(KEY_WORD_LABELS, "vi/roman/key_word.tsv");

// Keep table and variant order, including duplicates, for equal-cost ties.
struct KeywordIndex {
    ascii: [Vec<String>; 128],
    other: HashMap<char, Vec<String>>,
}
static KEYWORDS: LazyLock<KeywordIndex> = LazyLock::new(|| {
    let mut index = KeywordIndex { ascii: std::array::from_fn(|_| Vec::new()), other: HashMap::new() };
    for row in KEY_WORD_LABELS.iter() {
        let Some(w) = row.first().map(|s| s.trim()).filter(|s| !s.is_empty()) else { continue };
        let mut chars = w.chars();
        let first = chars.next().unwrap();
        let title = first.to_uppercase().collect::<String>() + chars.as_str();
        for variant in [w.to_string(), title, w.to_uppercase()] {
            let first = variant.chars().next().unwrap();
            if first.is_ascii() {
                index.ascii[first as usize].push(variant);
            } else {
                index.other.entry(first).or_default().push(variant);
            }
        }
    }
    index
});

const ROMANS: [(u32, &str); 13] = [
    (1000, "M"),
    (900, "CM"),
    (500, "D"),
    (400, "CD"),
    (100, "C"),
    (90, "XC"),
    (50, "L"),
    (40, "XL"),
    (10, "X"),
    (9, "IX"),
    (5, "V"),
    (4, "IV"),
    (1, "I"),
];

fn parse_roman(bytes: &[u8]) -> Option<u32> {
    if bytes.is_empty() || bytes.len() > 15 {
        return None;
    }
    if bytes.iter().any(u8::is_ascii_uppercase) && bytes.iter().any(u8::is_ascii_lowercase) {
        return None;
    }
    let value = |b: u8| match b.to_ascii_uppercase() {
        b'I' => Some(1),
        b'V' => Some(5),
        b'X' => Some(10),
        b'L' => Some(50),
        b'C' => Some(100),
        b'D' => Some(500),
        b'M' => Some(1000),
        _ => None,
    };
    let mut total = 0;
    let mut i = 0;
    while i < bytes.len() {
        let a = value(bytes[i])?;
        if i + 1 < bytes.len() {
            let b = value(bytes[i + 1])?;
            if a < b {
                total += b - a;
                i += 2;
                continue;
            }
        }
        total += a;
        i += 1;
    }
    if total == 0 || total >= 4000 {
        return None;
    }
    let mut n = total;
    let mut at = 0;
    for &(val, sym) in &ROMANS {
        while n >= val {
            for b in sym.bytes() {
                if bytes.get(at).map(u8::to_ascii_uppercase) != Some(b) {
                    return None;
                }
                at += 1;
            }
            n -= val;
        }
    }
    (at == bytes.len()).then_some(total)
}

fn decimal_digits(mut n: u32, digits: &mut [u8; 4]) -> &[u8] {
    let mut pos = digits.len();
    while n > 0 {
        pos -= 1;
        digits[pos] = b'0' + (n % 10) as u8;
        n /= 10;
    }
    &digits[pos..]
}

impl Roman {
    fn scan(text: &[char], start: usize, mut f: impl FnMut(usize, &str, u32, Cost)) {
        let Some(&first) = text.get(start) else { return };
        let keywords = if first.is_ascii() {
            &KEYWORDS.ascii[first as usize]
        } else {
            let Some(rows) = KEYWORDS.other.get(&first) else { return };
            rows
        };
        for kw in keywords {
            let mut p = start;
            for c in kw.chars() {
                if text.get(p) != Some(&c) {
                    break;
                }
                p += 1;
            }
            if p - start != kw.chars().count() || text.get(p) != Some(&' ') {
                continue;
            }
            let mut roman = [0u8; 15];
            let mut len = 0;
            let mut k = p + 1;
            while let Some(&c) = text.get(k) {
                if !matches!(c, 'i' | 'v' | 'x' | 'l' | 'c' | 'd' | 'm' | 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M')
                    || len == roman.len()
                {
                    break;
                }
                roman[len] = c as u8;
                len += 1;
                k += 1;
                if let Some(value) = parse_roman(&roman[..len]) {
                    let mut digits = [0; 4];
                    if let Some(cost) = cardinal_cost(decimal_digits(value, &mut digits)) {
                        f(k, kw, value, cost);
                    }
                }
            }
        }
    }
}

impl Classifier for Roman {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan(text, start, |end, _, _, c| {
            out.push(Candidate { end, cost: WEIGHT_ROMAN + c, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        Self::scan(text, start, |end, kw, value, c| {
            if candidate.tag.is_empty() && end == candidate.end && WEIGHT_ROMAN + c == candidate.cost {
                let mut digits = [0; 4];
                let mut spoken = String::new();
                spell_cardinal_digits(decimal_digits(value, &mut digits), &mut spoken);
                candidate.tag = format!("roman {{ key_cardinal: \"{}\" integer: \"{}\" }}", kw, spoken);
            }
        });
    }
}

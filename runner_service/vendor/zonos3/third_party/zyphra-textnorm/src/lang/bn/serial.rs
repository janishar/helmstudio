//! Bengali Serial classifier.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const SPECIAL_SYMBOLS_DATA: &str = include_str!("../../../data/bn/serial/special_symbols.tsv");
const POWER_SPECIAL_DATA: &str = include_str!("../../../data/bn/serial/power_special.tsv");
const LETTERS_DATA: &str = include_str!("../../../data/bn/address/letters.tsv");
const MEASURE_UNITS_DATA: &str = include_str!("../../../data/bn/measure/unit.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static SPECIAL_SYMBOLS: LazyLock<std::collections::HashMap<char, &'static str>> = LazyLock::new(|| {
    let mut m = std::collections::HashMap::new();
    for (k, v) in rows(SPECIAL_SYMBOLS_DATA) {
        if let Some(c) = k.chars().next() {
            m.insert(c, v);
        }
    }
    m
});

static POWER_SPECIAL: LazyLock<Vec<(&'static str, &'static str)>> =
    LazyLock::new(|| rows(POWER_SPECIAL_DATA).collect());

static LATIN_LETTERS: LazyLock<std::collections::HashMap<char, &'static str>> = LazyLock::new(|| {
    let mut m = std::collections::HashMap::new();
    for (k, v) in rows(LETTERS_DATA) {
        if let Some(c) = k.chars().next() {
            m.insert(c.to_ascii_lowercase(), v);
            m.insert(c.to_ascii_uppercase(), v);
        }
    }
    m
});

static MEASURE_UNITS: LazyLock<Vec<&'static str>> = LazyLock::new(|| {
    let mut v: Vec<&'static str> = rows(MEASURE_UNITS_DATA).map(|(k, _)| k).collect();
    v.sort_by(|a, b| b.len().cmp(&a.len()));
    v
});

pub fn possible_prefix(input: &str) -> bool {
    !input.is_empty() && !input.starts_with(' ')
}

pub fn alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || is_bn_char(c)
}

pub fn is_bn_char(c: char) -> bool {
    ('\u{0980}'..='\u{09FF}').contains(&c) && !numbers::is_digit(c)
}

fn tag_name(s: &str) -> String {
    let mut tag = String::with_capacity(s.len() + 16);
    tag.push_str("name: \"");
    for c in s.chars() {
        tag.push(if c == ' ' { '\u{a0}' } else { c });
    }
    tag.push('"');
    tag
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let top_weight = cost(1.11);

    // 1. Powers: e.g. 2^2, 2^4, x^2
    for &(p_key, p_val) in POWER_SPECIAL.iter() {
        if let Some(base) = s.strip_suffix(p_key) {
            let base_spoken = if let Some(base_num) = numbers::code_num(base) {
                Some(base_num)
            } else if base.len() == 1 && base.chars().all(|c| c.is_ascii_alphabetic()) {
                let spelled: Vec<&str> = base.chars().filter_map(|c| LATIN_LETTERS.get(&c).copied()).collect();
                if spelled.len() == base.len() { Some(spelled.join(" ")) } else { None }
            } else {
                None
            };

            if let Some(base_w) = base_spoken {
                let full = format!("{base_w}{p_val}");
                out.push(Candidate {
                    end,
                    cost: top_weight + cost(-1.0),
                    rank: 11,
                    tag: super::lazy::tag(move || tag_name(&full)),
                });
                return;
            }
        }
    }

    if let Some((base, exp)) = s.split_once('^') {
        let base_spoken = if let Some(base_num) = numbers::code_num(base) {
            Some(base_num)
        } else if base.len() == 1 && base.chars().all(|c| c.is_ascii_alphabetic()) {
            let spelled: Vec<&str> = base.chars().filter_map(|c| LATIN_LETTERS.get(&c).copied()).collect();
            if spelled.len() == base.len() { Some(spelled.join(" ")) } else { None }
        } else {
            None
        };

        if let (Some(b_w), Some(e_w)) = (base_spoken, numbers::code_num(exp)) {
            let full = format!("{b_w} টু দ্য পাওয়ার {e_w}");
            out.push(Candidate {
                end,
                cost: top_weight + cost(1.0),
                rank: 11,
                tag: super::lazy::tag(move || tag_name(&full)),
            });
            return;
        }
    }

    // 2. Versions:
    // a) "v2.0", "V1.0.1" -> (v|V -> ভি) + " " + num + (" ডট " + num)+
    if (s.starts_with('v') || s.starts_with('V')) && s.len() > 1 && s[1..].contains('.') {
        let rest = &s[1..];
        let parts: Vec<&str> = rest.split('.').collect();
        if parts.len() >= 2 && parts.iter().all(|p| !p.is_empty() && p.chars().all(numbers::is_digit)) {
            let mut num_words = Vec::new();
            for p in &parts {
                if let Some(w) = numbers::graph(p).into_iter().next().map(|r| r.0) {
                    num_words.push(w);
                }
            }
            if num_words.len() == parts.len() {
                let joined = num_words.join(" ডট ");
                let full = format!("ভি {joined}");
                out.push(Candidate { end, cost: top_weight, rank: 11, tag: super::lazy::tag(move || tag_name(&full)) });
                return;
            }
        }
    }

    // b) Exactly three numeric components: "17.4.1" -> "সতেরো ডট চার ডট এক"
    let dot_parts: Vec<&str> = s.split('.').collect();
    if dot_parts.len() == 3 && dot_parts.iter().all(|p| !p.is_empty() && p.chars().all(numbers::is_digit)) {
        let mut num_words = Vec::new();
        for p in &dot_parts {
            if let Some(w) = numbers::graph(p).into_iter().next().map(|r| r.0) {
                num_words.push(w);
            }
        }
        if num_words.len() == 3 {
            let full = num_words.join(" ডট ");
            out.push(Candidate { end, cost: top_weight, rank: 11, tag: super::lazy::tag(move || tag_name(&full)) });
            return;
        }
    }

    // 3. Exclusions check:
    // If it's a measure pattern like "12kg" or "10-15%", let measure handle it!
    for &u in MEASURE_UNITS.iter() {
        if s.ends_with(u) {
            let stem = &s[..s.len() - u.len()].trim_end_matches(' ');
            if !stem.is_empty() && stem.chars().any(numbers::is_digit) {
                return;
            }
        }
    }
    // If it's a pure Latin word, or pure word with slash, let word handle it
    if s.chars().all(|c| c.is_ascii_alphabetic() || c == '-' || c == '/') {
        return;
    }
    // Dimension pattern "10x20"
    if let Some((l, r)) = s.split_once('x').or_else(|| s.split_once('X')) {
        if l.chars().all(numbers::is_digit) && r.chars().all(numbers::is_digit) {
            return;
        }
    }

    // 4. Glued or delimited alphanumeric codes:
    // Insert spaces between alpha and digit, or digit and alpha
    let mut expanded = String::new();
    let chars: Vec<char> = s.chars().collect();
    let mut has_digit = false;
    let mut has_letter = false;

    for i in 0..chars.len() {
        let c = chars[i];
        if numbers::is_digit(c) {
            has_digit = true;
        }
        if alpha(c) {
            has_letter = true;
        }
        if i > 0 {
            let prev = chars[i - 1];
            if (alpha(prev) && numbers::is_digit(c)) || (numbers::is_digit(prev) && alpha(c)) {
                expanded.push(' ');
            }
        }
        expanded.push(c);
    }

    if !has_digit || !has_letter {
        // Serial must have both a letter and a digit (mixed code), unless handled by power or version above
        return;
    }

    // Now tokenize by delimiter ('-', '/', ' ')
    // Note: delimiters in serial_core are retained in the output, while spaces between glued alpha/digit are spaces.
    let mut result = String::new();
    let mut i = 0;
    let exp_chars: Vec<char> = expanded.chars().collect();

    while i < exp_chars.len() {
        let c = exp_chars[i];
        if matches!(c, '-' | '/' | ' ') {
            result.push(c);
            i += 1;
            continue;
        }

        // Check if starting a digit run:
        if numbers::is_digit(c) {
            let start = i;
            while i < exp_chars.len() && numbers::is_digit(exp_chars[i]) {
                i += 1;
            }
            let digit_str: String = exp_chars[start..i].iter().collect();
            if let Some(spoken_num) = numbers::code_num(&digit_str) {
                result.push_str(&spoken_num);
            } else {
                return;
            }
            continue;
        }

        // Check Latin letter run:
        if c.is_ascii_alphabetic() {
            let start = i;
            while i < exp_chars.len() && exp_chars[i].is_ascii_alphabetic() {
                i += 1;
            }
            let alpha_str: String = exp_chars[start..i].iter().collect();
            let mut spelled = Vec::new();
            for ac in alpha_str.chars() {
                if let Some(&w) = LATIN_LETTERS.get(&ac) {
                    spelled.push(w);
                } else {
                    return;
                }
            }
            result.push_str(&spelled.join(" "));
            continue;
        }

        // Check special symbols:
        if let Some(&sym_w) = SPECIAL_SYMBOLS.get(&c) {
            result.push_str(sym_w);
            i += 1;
            continue;
        }

        // Bengali word run:
        if is_bn_char(c) {
            let start = i;
            while i < exp_chars.len() && is_bn_char(exp_chars[i]) {
                i += 1;
            }
            let bn_word: String = exp_chars[start..i].iter().collect();
            result.push_str(&bn_word);
            continue;
        }

        return;
    }

    if !result.is_empty() {
        out.push(Candidate { end, cost: top_weight, rank: 11, tag: super::lazy::tag(move || tag_name(&result)) });
    }
}

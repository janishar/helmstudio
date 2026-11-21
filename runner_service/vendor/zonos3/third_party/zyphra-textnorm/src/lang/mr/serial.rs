//! Marathi serial numbers, alphanumeric codes, powers and versions.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const LETTERS: &str = include_str!("../../../data/mr/address/letters.tsv");
const CHARS: &str = include_str!("../../../data/mr/serial/chars.tsv");
const SYMBOL: &str = include_str!("../../../data/mr/serial/special_symbols.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static SYMBOLS_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(SYMBOL) {
        out.insert(k, v);
    }
    out
});

pub(crate) fn mr(c: char) -> bool {
    const FIRST: u32 = 0x0900;
    const LAST: u32 = 0x097f;
    static CHARS_INDEX: LazyLock<[bool; (LAST - FIRST + 1) as usize]> = LazyLock::new(|| {
        let mut index = [false; (LAST - FIRST + 1) as usize];
        for key in CHARS.lines().map(|line| line.trim_end_matches('\r')) {
            if let Some(c) = key.chars().next() {
                let cp = c as u32;
                if (FIRST..=LAST).contains(&cp) {
                    index[(cp - FIRST) as usize] = true;
                }
            }
        }
        index
    });
    let cp = c as u32;
    (FIRST..=LAST).contains(&cp) && CHARS_INDEX[(cp - FIRST) as usize]
}

pub(crate) fn alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || mr(c)
}

pub(crate) fn possible_prefix(input: &str) -> bool {
    let prefixed = input.starts_with(['v', 'V']);
    let mut dots = 0;
    let mut previous_alpha = false;
    let mut previous_digit = false;
    for c in input.chars().take_while(|c| !c.is_whitespace()) {
        if matches!(c, '-' | '/' | '^' | '²' | '³') {
            return true;
        }
        if c == '.' {
            dots += 1;
            if dots >= if prefixed { 1 } else { 2 } {
                return true;
            }
        }
        let is_alpha = alpha(c);
        let is_digit = numbers::digit(c).is_some();
        if (previous_alpha && is_digit) || (previous_digit && is_alpha) {
            return true;
        }
        previous_alpha = is_alpha;
        previous_digit = is_digit;
    }
    false
}

fn letter(c: char) -> Option<&'static str> {
    static LETTER_INDEX: LazyLock<[Option<&'static str>; 26]> = LazyLock::new(|| {
        let mut index = [None; 26];
        for (key, value) in rows(LETTERS) {
            if key.len() == 1 {
                let b = key.as_bytes()[0].to_ascii_uppercase();
                if b.is_ascii_uppercase() {
                    index[(b - b'A') as usize] = Some(value);
                }
            }
        }
        index
    });
    if c.is_ascii_alphabetic() {
        let b = c.to_ascii_uppercase() as u8;
        LETTER_INDEX[(b - b'A') as usize]
    } else {
        None
    }
}

fn num(s: &str) -> Vec<(String, Cost)> {
    let n = s.chars().count();
    if n >= 4 { numbers::digitwise(s).map(|x| vec![(x, 0)]).unwrap_or_default() } else { numbers::graph(s) }
}

fn emit(out: &mut Vec<Candidate>, end: usize, value: String, c: Cost) {
    out.push(Candidate {
        end,
        cost: cost(1.11) + c,
        rank: 11,
        tag: super::lazy::tag(|| format!("name: \"{}\"", value.replace(' ', "\u{a0}"))),
    });
}

fn segment(s: &str) -> Vec<(String, Cost)> {
    if s.is_empty() {
        return Vec::new();
    }
    let mut out = Vec::new();
    if s.chars().all(|c| numbers::digit(c).is_some()) {
        out.extend(num(s));
    }
    if s.chars().all(|c| c.is_ascii_alphabetic()) && s.chars().all(|c| letter(c).is_some()) {
        out.push((super::lazy::tag(|| s.chars().filter_map(letter).collect::<Vec<_>>().join(" ")), 0));
    }
    if s.chars().count() >= 2 && s.chars().all(mr) {
        out.push((super::lazy::tag(|| s.to_owned()), 0));
    }
    if let Some(&v) = SYMBOLS_MAP.get(s) {
        out.push((super::lazy::tag(|| v.to_owned()), 0));
    }
    out
}

fn power_base_word(s: &str) -> Vec<(String, Cost)> {
    if !s.is_empty() && s.chars().all(|c| numbers::digit(c).is_some()) {
        return num(s);
    }
    let mut chars = s.chars();
    match (chars.next(), chars.next()) {
        (Some(c), None) if letter(c).is_some() => {
            vec![(super::lazy::tag(|| letter(c).unwrap().to_owned()), 0)]
        }
        (Some(c), None) if mr(c) => vec![(super::lazy::tag(|| s.to_owned()), 0)],
        _ => Vec::new(),
    }
}

fn is_pure_latin_word(s: &str) -> bool {
    let mut has_alpha = false;
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            has_alpha = true;
        } else if !matches!(c, '-' | '/') {
            return false;
        }
    }
    has_alpha
}

fn is_dimension(s: &str) -> bool {
    let mut parts = s.split(['x', 'X']);
    match (parts.next(), parts.next(), parts.next()) {
        (Some(a), Some(b), None) => {
            [a, b].iter().all(|p| !p.is_empty() && p.chars().all(|c| numbers::digit(c).is_some()))
        }
        _ => false,
    }
}

fn is_ip(s: &str) -> bool {
    let mut parts = s.split('.');
    match (parts.next(), parts.next(), parts.next(), parts.next(), parts.next()) {
        (Some(a), Some(b), Some(c), Some(d), None) => {
            [a, b, c, d].iter().all(|p| (1..=3).contains(&p.len()) && p.chars().all(|c| c.is_ascii_digit()))
        }
        _ => false,
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.chars().count() < 2 {
        return;
    }
    let mut pieces = s.split(|c| matches!(c, '-' | '/' | '.'));
    if let (Some(a), Some(b), Some(c), None) = (pieces.next(), pieces.next(), pieces.next(), pieces.next()) {
        if (1..=4).contains(&a.chars().count())
            && (1..=2).contains(&b.chars().count())
            && (1..=4).contains(&c.chars().count())
            && [a, b, c].iter().all(|part| part.chars().all(|ch| numbers::digit(ch).is_some()))
        {
            return;
        }
    }
    if is_pure_latin_word(s) || is_dimension(s) || is_ip(s) {
        return;
    }

    // 1. Version strings ("v2.0", "17.4.1", "v1.2.3")
    if let Some(rest) = s.strip_prefix(['v', 'V']) {
        let parts: Vec<&str> = rest.split('.').collect();
        if parts.len() >= 2 && parts.iter().all(|p| !p.is_empty() && p.chars().all(|c| numbers::digit(c).is_some())) {
            let mut rendered_parts = Vec::new();
            for p in &parts {
                if let Some((v, _)) =
                    numbers::graph(p).into_iter().next().or_else(|| numbers::digitwise(p).map(|x| (x, 0)))
                {
                    rendered_parts.push(v);
                } else {
                    return;
                }
            }
            emit(out, end, format!("व्ही {}", rendered_parts.join(" डॉट ")), 0);
            return;
        }
    }

    let dot_parts: Vec<&str> = s.split('.').collect();
    if dot_parts.len() >= 3 && dot_parts.iter().all(|p| !p.is_empty() && p.chars().all(|c| numbers::digit(c).is_some()))
    {
        let mut rendered_parts = Vec::new();
        for p in &dot_parts {
            if let Some((v, _)) = numbers::graph(p).into_iter().next().or_else(|| numbers::digitwise(p).map(|x| (x, 0)))
            {
                rendered_parts.push(v);
            } else {
                return;
            }
        }
        emit(out, end, rendered_parts.join(" डॉट "), 0);
        return;
    }

    // 2. Power strings: "^2", "^3", "²", "³" (weight -1.0) or "^<exp>" (weight 1.0)
    for (suffix, w) in [
        ("²", cost(-1.0)),
        ("³", cost(-1.0)),
        ("^2", cost(-1.0)),
        ("^२", cost(-1.0)),
        ("^3", cost(-1.0)),
        ("^३", cost(-1.0)),
    ] {
        if let Some(base) = s.strip_suffix(suffix) {
            let spoken_suffix = match suffix {
                "²" | "^2" | "^२" => "वर्ग",
                _ => "घन",
            };
            for (b_word, c) in power_base_word(base) {
                emit(out, end, super::lazy::tag(|| format!("{b_word} {spoken_suffix}")), c + w);
            }
        }
    }

    if let Some((base, exp)) = s.split_once('^') {
        let (exp_core, opt_neg) = if let Some(r) = exp.strip_prefix('-').or_else(|| exp.strip_prefix('−')) {
            (r, "उणे ")
        } else {
            (exp, "")
        };
        for (b_word, cb) in power_base_word(base) {
            for (e_word, ce) in num(exp_core) {
                emit(out, end, super::lazy::tag(|| format!("{b_word} घात {opt_neg}{e_word}")), cb + ce + cost(1.0));
            }
        }
    }

    // 3. Serial with delimiters: "-", "/", " "
    let mut has_delim = false;
    for delim in ['-', '/', ' '] {
        if s.contains(delim) {
            has_delim = true;
            let parts: Vec<&str> = s.split(delim).collect();
            if parts.len() >= 2 && parts.iter().all(|p| !p.is_empty()) {
                let mut segment_options: Vec<Vec<(String, Cost)>> = Vec::new();
                for p in parts {
                    let segs = segment(p);
                    if segs.is_empty() {
                        segment_options.clear();
                        break;
                    }
                    segment_options.push(segs);
                }
                if !segment_options.is_empty() {
                    let mut combos = vec![(Vec::new(), 0)];
                    for opts in segment_options {
                        let mut next = Vec::new();
                        for (prev_v, prev_c) in combos {
                            for (cur_v, cur_c) in &opts {
                                let mut v = prev_v.clone();
                                v.push(cur_v.clone());
                                next.push((v, prev_c + *cur_c));
                            }
                        }
                        combos = next;
                    }
                    for (combo, c) in combos {
                        let joiner = match delim {
                            '-' => "-",
                            '/' => "/",
                            _ => " ",
                        };
                        emit(out, end, combo.join(joiner), c);
                    }
                }
            }
        }
    }

    // 4. Glued serial (e.g. 5जी -> "पाच जी", B-60 -> "बी-साठ", etc.)
    if !has_delim {
        // Insert space between transition of alpha and digit
        let mut chunks = Vec::new();
        let mut cur = String::new();
        let mut last_type = None; // 0 for alpha, 1 for digit
        for c in s.chars() {
            let t = if alpha(c) {
                Some(0)
            } else if numbers::digit(c).is_some() {
                Some(1)
            } else {
                None
            };
            if let Some(t_val) = t {
                if let Some(last_val) = last_type {
                    if last_val != t_val {
                        chunks.push(cur);
                        cur = String::new();
                    }
                }
                cur.push(c);
                last_type = Some(t_val);
            } else {
                return;
            }
        }
        if !cur.is_empty() {
            chunks.push(cur);
        }
        if chunks.len() >= 2 {
            let mut segment_options = Vec::new();
            for chunk in chunks {
                let segs = segment(&chunk);
                if segs.is_empty() {
                    return;
                }
                segment_options.push(segs);
            }
            let mut combos = vec![(Vec::new(), 0)];
            for opts in segment_options {
                let mut next = Vec::new();
                for (prev_v, prev_c) in combos {
                    for (cur_v, cur_c) in &opts {
                        let mut v = prev_v.clone();
                        v.push(cur_v.clone());
                        next.push((v, prev_c + *cur_c));
                    }
                }
                combos = next;
            }
            for (combo, c) in combos {
                emit(out, end, combo.join(" "), c);
            }
        }
    }
}

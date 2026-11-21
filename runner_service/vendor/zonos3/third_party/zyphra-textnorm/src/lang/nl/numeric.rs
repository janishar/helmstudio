//! Weighted cardinal and decimal paths.
use super::cardinal;

pub(super) type Reading = (String, f64);

pub(super) fn integer(s: &str) -> Vec<Reading> {
    if !s.bytes().any(|b| matches!(b, b'.' | b' ')) {
        return cardinal::paths(s);
    }
    let mut groups = s.split(['.', ' ']);
    let Some(first) = groups.next() else { return Vec::new() };
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return Vec::new();
    }
    let mut digits = [0u8; 12];
    let mut len = first.len();
    digits[..len].copy_from_slice(first.as_bytes());
    for group in groups {
        if group.len() != 3 || len + 3 > digits.len() || !group.bytes().all(|b| b.is_ascii_digit()) {
            return Vec::new();
        }
        digits[len..len + 3].copy_from_slice(group.as_bytes());
        len += 3;
    }
    // Every copied byte has already been checked to be an ASCII digit.
    cardinal::paths(std::str::from_utf8(&digits[..len]).unwrap())
}

pub(super) fn decimal(s: &str) -> Vec<Reading> {
    let (negative, s) =
        if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { (true, r) } else { (false, s) };
    let mut result = Vec::new();
    for (idx, sep) in s.char_indices().filter(|(_, c)| *c == ',' || *c == '.') {
        let part = &s[idx + 1..];
        if part.is_empty() || !part.bytes().all(|b| b.is_ascii_digit()) {
            continue;
        }
        for (whole, cw) in integer(&s[..idx]) {
            let mut fractions = Vec::new();
            if part.len() == 1 {
                fractions.push((cardinal::digit(part.chars().next().unwrap()).unwrap().to_owned(), 1.0));
                if part != "0" {
                    fractions.push((cardinal::digit(part.chars().next().unwrap()).unwrap().to_owned(), 0.98));
                }
            } else {
                let digits = part.chars().map(|c| cardinal::digit(c).unwrap()).collect::<Vec<_>>().join(" ");
                fractions.push((digits, 1.05));
                if part.len() == 2 {
                    if part.starts_with('0') {
                        fractions
                            .push((format!("nul {}", cardinal::digit(part.chars().nth(1).unwrap()).unwrap()), 0.98));
                    } else if let Some(v) = cardinal::raw(part) {
                        fractions.push((v, 0.98));
                    }
                }
            }
            for (fraction, fw) in fractions {
                result.push((
                    format!("{}{} komma {}", if negative { "min " } else { "" }, whole, fraction),
                    cw + fw + if negative { 0.99 } else { 1.0 } + if sep == '.' { 0.1 } else { 0.0 },
                ));
            }
        }
    }
    result
}

pub(super) fn cardinal(s: &str) -> Vec<Reading> {
    let mut result = Vec::new();
    for (word, w) in integer(s) {
        result.push((word, 1.0 + w));
    }
    if let Some(rest) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        for (word, w) in integer(rest) {
            result.push((format!("min {word}"), 1.0 + w));
        }
    }
    if s.len() >= 2 && s.bytes().all(|b| b.is_ascii_digit()) {
        let digits = s.chars().map(|c| cardinal::digit(c).unwrap()).collect::<Vec<_>>().join(" ");
        result.push((digits, 1.2));
        let zeros = s.bytes().take_while(|b| *b == b'0').count();
        if zeros > 0 && zeros < s.len() && s.len() - zeros <= 2 {
            if let Some(rest) = cardinal::raw(&s[zeros..]) {
                result.push((format!("{}{}", "nul ".repeat(zeros), rest), 1.05));
            }
        }
    }
    let list: Vec<_> = s.split(',').collect();
    if list.len() >= 3 {
        let mut readings = vec![(String::new(), 0.95)];
        for part in list {
            let mut next = Vec::new();
            for (a, aw) in &readings {
                for (b, bw) in integer(part) {
                    next.push((if a.is_empty() { b } else { format!("{a}, {b}") }, aw + bw));
                }
            }
            readings = next;
        }
        result.extend(readings);
    }
    for (i, c) in s.char_indices().filter(|(_, c)| matches!(c, '-' | '–' | '—')) {
        let left = s[..i].trim_end_matches(crate::text::is_nemo_space);
        let right = s[i + c.len_utf8()..].trim_start_matches(crate::text::is_nemo_space);
        for a in integer(left) {
            for b in integer(right) {
                result.push((format!("{} tot {}", a.0, b.0), 0.98 + a.1 + b.1));
            }
        }
    }
    result
}

//! Serial numbers, alphanumeric codes, powers and versions.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
const LETTERS: &str = include_str!("../../../data/gu/address/letters.tsv");
const CHARS: &str = include_str!("../../../data/gu/serial/chars.tsv");
const SYMBOL: &str = include_str!("../../../data/gu/serial/special_symbols.tsv");
const POWER: &str = include_str!("../../../data/gu/serial/power_special.tsv");
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
fn table(data: &'static str, s: &str) -> Option<&'static str> {
    use std::collections::HashMap;
    type Index = HashMap<&'static str, &'static str>;
    fn index(data: &'static str) -> Index {
        let mut result = Index::new();
        for (key, value) in rows(data) {
            result.entry(key).or_insert(value);
        }
        result
    }
    static SYMBOLS: std::sync::LazyLock<Index> = std::sync::LazyLock::new(|| index(SYMBOL));
    static POWERS: std::sync::LazyLock<Index> = std::sync::LazyLock::new(|| index(POWER));
    if std::ptr::eq(data, SYMBOL) {
        SYMBOLS.get(s).copied()
    } else if std::ptr::eq(data, POWER) {
        POWERS.get(s).copied()
    } else {
        rows(data).find_map(|(k, v)| (k == s).then_some(v))
    }
}
pub(crate) fn gu(c: char) -> bool {
    const FIRST: u32 = 0x0a81;
    const LAST: u32 = 0x0aff;
    static CHARS_INDEX: std::sync::LazyLock<[bool; (LAST - FIRST + 1) as usize]> = std::sync::LazyLock::new(|| {
        let mut index = [false; (LAST - FIRST + 1) as usize];
        for (key, _) in rows(CHARS) {
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
    c.is_ascii_alphabetic() || gu(c)
}
/// Necessary syntax for any serial prefix before the first whitespace.
/// Core readings need a delimiter or an alpha/digit boundary; powers need an
/// exponent marker, and versions need two dots (one after an initial v/V).
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
    static LETTER_INDEX: std::sync::LazyLock<[Option<&'static str>; 26]> = std::sync::LazyLock::new(|| {
        let mut index = [None; 26];
        for (key, value) in rows(LETTERS) {
            if key.len() == 1 && key.as_bytes()[0].is_ascii_uppercase() {
                index[(key.as_bytes()[0] - b'A') as usize] = Some(value);
            }
        }
        index
    });
    if c.is_ascii_uppercase() { LETTER_INDEX[(c as u8 - b'A') as usize] } else { None }
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
    if s.chars().count() >= 2 && s.chars().all(gu) {
        out.push((super::lazy::tag(|| s.to_owned()), 0));
    }
    if let Some(v) = table(SYMBOL, s) {
        out.push((super::lazy::tag(|| v.to_owned()), 0));
    }
    out
}
// `serial_core`: one segment followed by one or more copied delimiters and
// segments. The input can be ambiguous (e.g. a delimiter may itself be a
// symbol segment), so preserve every valid split and its weight.
fn core(chars: &[char], pos: usize, count: usize, output: &str, weight: Cost, found: &mut Vec<(String, Cost)>) {
    let mut part = String::new();
    for end in pos + 1..=chars.len() {
        part.push(chars[end - 1]);
        for (spoken, cost_part) in segment(&part) {
            let mut next = super::lazy::tag(|| {
                let mut value = String::with_capacity(output.len() + spoken.len() + 1);
                value.push_str(output);
                value.push_str(&spoken);
                value
            });
            if end == chars.len() {
                if count >= 1 {
                    found.push((next, weight + cost_part));
                }
            } else if matches!(chars[end], '-' | '/' | ' ') {
                if super::lazy::building() {
                    next.push(chars[end]);
                }
                core(chars, end + 1, count + 1, &next, weight + cost_part, found);
            }
        }
    }
}
fn core_readings(s: &str) -> Vec<(String, Cost)> {
    let mut previous = None;
    let mut has_boundary = false;
    for c in s.chars() {
        has_boundary |= matches!(c, '-' | '/' | ' ');
        if let Some(p) = previous {
            has_boundary |= (alpha(p) && numbers::digit(c).is_some()) || (numbers::digit(p).is_some() && alpha(c));
        }
        previous = Some(c);
    }
    if !has_boundary {
        return Vec::new();
    }
    let input: Vec<char> = s.chars().collect();
    let mut found = Vec::new();
    core(&input, 0, 0, "", 0, &mut found);
    let mut glued = Vec::with_capacity(input.len() * 2);
    for (i, c) in input.iter().copied().enumerate() {
        if i > 0
            && ((alpha(input[i - 1]) && numbers::digit(c).is_some())
                || (numbers::digit(input[i - 1]).is_some() && alpha(c)))
        {
            glued.push(' ');
        }
        glued.push(c);
    }
    if glued != input {
        core(&glued, 0, 0, "", 0, &mut found);
    }
    found
}
pub(crate) fn mixed_readings(s: &str) -> Vec<(String, Cost)> {
    if !s.chars().all(|c| alpha(c) || numbers::digit(c).is_some() || matches!(c, '-' | '/'))
        || !s.chars().any(alpha)
        || !s.chars().any(|c| numbers::digit(c).is_some())
    {
        return Vec::new();
    }
    core_readings(s)
}
pub(crate) fn code_number(s: &str) -> Vec<(String, Cost)> {
    num(s)
}
fn excluded(s: &str) -> bool {
    if s.chars().all(|c| c.is_ascii_alphabetic() || matches!(c, '-' | '/'))
        && s.chars().any(|c| c.is_ascii_alphabetic())
    {
        return true;
    }
    if let Some(i) = s.find(|c| c == 'x' || c == 'X') {
        let (a, b) = s.split_at(i);
        let b = &b[1..];
        if !a.is_empty()
            && !b.is_empty()
            && a.chars().all(|c| numbers::digit(c).is_some())
            && b.chars().all(|c| numbers::digit(c).is_some())
        {
            return true;
        }
    }
    for (suffix, _) in rows(include_str!("../../../data/gu/ordinal/suffixes.tsv")) {
        if let Some(n) = s.strip_suffix(suffix) {
            if !n.is_empty() && n.chars().all(|c| numbers::digit(c).is_some()) {
                return true;
            }
        }
    }
    static EXCEPTIONS: std::sync::LazyLock<std::collections::HashSet<&'static str>> = std::sync::LazyLock::new(|| {
        rows(include_str!("../../../data/gu/ordinal/exceptions.tsv")).map(|(key, _)| key).collect()
    });
    if EXCEPTIONS.contains(s) {
        return true;
    }
    let mut dotted = s.split('.');
    if (0..4)
        .all(|_| dotted.next().is_some_and(|x| !x.is_empty() && x.len() <= 3 && x.bytes().all(|b| b.is_ascii_digit())))
        && dotted.next().is_none()
    {
        return true;
    }
    for a in ['-', '/', '.'] {
        for b in ['-', '/', '.'] {
            if let Some((x, r)) = s.split_once(a) {
                if let Some((y, z)) = r.split_once(b) {
                    if (1..=4).contains(&x.chars().count())
                        && (1..=2).contains(&y.chars().count())
                        && (1..=4).contains(&z.chars().count())
                        && [x, y, z].iter().all(|v| v.chars().all(|c| numbers::digit(c).is_some()))
                    {
                        return true;
                    }
                }
            }
        }
    }
    false
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return;
    }
    let mut previous = None;
    let mut possible = false;
    for c in s.chars() {
        possible |= matches!(c, '-' | '/' | '.' | '^' | '²' | '³');
        if let Some(p) = previous {
            possible |= (alpha(p) && numbers::digit(c).is_some()) || (numbers::digit(p).is_some() && alpha(c));
        }
        previous = Some(c);
    }
    if !possible {
        return;
    }
    if s.chars().count() < 2 || s.chars().any(char::is_whitespace) || excluded(s) {
        return;
    }
    let power = s.char_indices().find(|(_, c)| matches!(c, '^' | '²' | '³'));
    if let Some((idx, op)) = power.filter(|(idx, op)| *op == '^' || *idx + op.len_utf8() == s.len()) {
        let base = &s[..idx];
        let tail = &s[idx + op.len_utf8()..];
        let mut bases = num(base);
        if base.chars().count() == 1 {
            let c = base.chars().next().unwrap();
            if let Some(v) = letter(c) {
                bases.push((v.to_owned(), 0));
            }
            if gu(c) || c.is_ascii_alphabetic() {
                bases.push((c.to_string(), 0));
            }
        }
        let exp = if op == '^' {
            tail
        } else if op == '²' {
            "²"
        } else {
            "³"
        };
        for (b, bc) in bases {
            if let Some(w) = table(POWER, exp) {
                emit(out, end, format!("{b} {w}"), bc + cost(-1.0));
            }
            if op == '^' {
                let (exp, neg) = if let Some(e) = exp.strip_prefix('-').or_else(|| exp.strip_prefix('−')) {
                    (e, "નકારાત્મક ")
                } else {
                    (exp, "")
                };
                for (v, c) in num(exp) {
                    emit(out, end, format!("{b} ઘાત {neg}{v}"), bc + c + cost(1.0));
                }
            }
        }
    }
    if s.contains('.') {
        let (ver, prefixed) =
            if let Some(v) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) { (v, true) } else { (s, false) };
        let p: Vec<_> = ver.split('.').collect();
        if (prefixed && p.len() >= 2) || (!prefixed && p.len() >= 3) {
            let mut states = vec![(if prefixed { "વી ".to_owned() } else { String::new() }, 0)];
            for (i, part) in p.iter().enumerate() {
                let mut vals = numbers::graph(part);
                if let Some(v) = numbers::digitwise(part) {
                    vals.push((v, 0));
                }
                let mut next = Vec::new();
                for (x, c) in states {
                    for (v, w) in &vals {
                        next.push((format!("{x}{}{v}", if i == 0 { "" } else { " ડૉટ " }), c + *w));
                    }
                }
                states = next;
            }
            for (v, c) in states {
                emit(out, end, v, c);
            }
        }
    }
    for (value, cost) in core_readings(s) {
        emit(out, end, value, cost);
    }
}

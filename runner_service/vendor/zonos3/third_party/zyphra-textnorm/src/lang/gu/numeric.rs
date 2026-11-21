//! Cardinal, decimal, ordinal and fractional classifier branches.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
fn table(data: &'static str, key: &str) -> Option<&'static str> {
    use std::collections::HashMap;
    use std::sync::LazyLock;
    fn index(data: &'static str) -> HashMap<&'static str, &'static str> {
        let mut out = HashMap::new();
        for (key, value) in rows(data) {
            out.entry(key).or_insert(value);
        }
        out
    }
    static EXCEPTION_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(EXCEPTIONS));
    static COMMON_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(COMMON));
    static PAUNE_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(PAUNE));
    if std::ptr::eq(data, EXCEPTIONS) {
        EXCEPTION_INDEX.get(key).copied()
    } else if std::ptr::eq(data, COMMON) {
        COMMON_INDEX.get(key).copied()
    } else if std::ptr::eq(data, PAUNE) {
        PAUNE_INDEX.get(key).copied()
    } else {
        rows(data).find_map(|(k, v)| (k == key).then_some(v))
    }
}
const SUFFIX: &str = include_str!("../../../data/gu/ordinal/suffixes.tsv");
const EXCEPTIONS: &str = include_str!("../../../data/gu/ordinal/exceptions.tsv");
const COMMON: &str = include_str!("../../../data/gu/fraction/common_fractions.tsv");
const PAUNE: &str = include_str!("../../../data/gu/whitelist/paune_mappings.tsv");
const QUANTITIES: &str = include_str!("../../../data/gu/numbers/thousands.tsv");
// Once a non-structural character appears, the only possible continuations
// are an ordinal/quantity suffix or an exact lexical exception/fraction.
// Return the furthest such source-key ending in the available input.
pub(crate) fn last_lexical_end(input: &str, tail_start: usize) -> Option<usize> {
    static SUFFIXES: std::sync::LazyLock<super::address::KeywordIndex> = std::sync::LazyLock::new(|| {
        let mut index = super::address::KeywordIndex::new();
        for (key, _) in rows(SUFFIX).chain(rows(QUANTITIES)) {
            index.insert(key);
        }
        index
    });
    static EXACT: std::sync::LazyLock<super::address::KeywordIndex> = std::sync::LazyLock::new(|| {
        let mut index = super::address::KeywordIndex::new();
        for (key, _) in rows(EXCEPTIONS).chain(rows(COMMON)) {
            index.insert(key);
        }
        index
    });
    let tail = &input[tail_start..];
    let suffix_end = tail
        .chars()
        .next()
        .into_iter()
        .flat_map(|c| SUFFIXES.get(c).iter())
        .filter(|key| tail.starts_with(**key))
        .map(|key| tail_start + key.len())
        .max();
    let exact_end = input
        .chars()
        .next()
        .into_iter()
        .flat_map(|c| EXACT.get(c).iter())
        .filter(|key| input.starts_with(**key))
        .map(|key| key.len())
        .max();
    suffix_end.into_iter().chain(exact_end).max()
}

// A numeric branch can contain non-structural letters only in one of these
// source lexicons. Grouping by final character keeps rejection independent
// of the size of the preceding sentence.
pub(crate) fn lexical_ending(s: &str) -> bool {
    type Endings = std::collections::HashMap<char, Vec<&'static str>>;
    static ENDINGS: std::sync::LazyLock<Endings> = std::sync::LazyLock::new(|| {
        let mut index = Endings::new();
        for (key, _) in rows(SUFFIX).chain(rows(QUANTITIES)).chain(rows(EXCEPTIONS)).chain(rows(COMMON)) {
            if let Some(last) = key.chars().last() {
                index.entry(last).or_default().push(key);
            }
        }
        index
    });
    s.chars()
        .last()
        .and_then(|last| ENDINGS.get(&last))
        .is_some_and(|endings| endings.iter().any(|ending| s.ends_with(*ending)))
}

pub(crate) fn structural_char(c: char) -> bool {
    numbers::digit(c).is_some()
        || matches!(
            c,
            '.' | ','
                | '/'
                | '-'
                | '−'
                | '–'
                | '~'
                | '±'
                | ' '
                | '½'
                | '¼'
                | '¾'
                | '⅓'
                | '⅔'
                | '⅕'
                | '⅖'
                | '⅗'
                | '⅘'
                | '⅙'
                | '⅚'
                | '⅐'
                | '⅛'
                | '⅜'
                | '⅝'
                | '⅞'
                | '⅑'
                | '⅒'
        )
}

fn digit(c: char) -> bool {
    numbers::digit(c).is_some()
}
fn is_zero(c: char) -> bool {
    c == '0' || c == '૦'
}

fn grouped(s: &str) -> Option<String> {
    if !s.contains(',') {
        return None;
    }
    let parts: Vec<&str> = s.split(',').collect();
    if parts.len() < 2 || parts.iter().any(|x| x.is_empty() || !x.chars().all(digit)) {
        return None;
    }
    let lens: Vec<usize> = parts.iter().map(|x| x.chars().count()).collect();
    let western = (1..=3).contains(&lens[0]) && lens[1..].iter().all(|&n| n == 3);
    let indian = (1..=2).contains(&lens[0]) && *lens.last()? == 3 && lens[1..lens.len() - 1].iter().all(|&n| n == 2);
    (western || indian).then(|| parts.concat())
}

pub fn cardinal(s: &str) -> Vec<(String, Cost)> {
    if let Some(n) = grouped(s) {
        return numbers::graph(&n);
    }
    if s.is_empty() || !s.chars().all(digit) {
        return Vec::new();
    }
    let mut out = numbers::graph(s);
    if s.chars().next().is_some_and(is_zero) {
        if let Some(spoken) = numbers::digitwise(s) {
            out.push((spoken, cost(0.5)));
        }
    }
    out
}
fn sign(s: &str) -> (&str, Option<&'static str>) {
    if let Some(r) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (r, Some("true"))
    } else if let Some(r) = s.strip_prefix('~') {
        (r.strip_prefix(' ').unwrap_or(r), Some("આશરે"))
    } else if let Some(r) = s.strip_prefix('±') {
        (r.strip_prefix(' ').unwrap_or(r), Some("વત્તા કે ઓછા"))
    } else {
        (s, None)
    }
}
fn signed(s: Option<&str>) -> String {
    s.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
}
fn push(
    out: &mut Vec<Candidate>,
    end: usize,
    weight: f64,
    c: Cost,
    rank: i32,
    class: &str,
    fields: impl FnOnce() -> String,
) {
    out.push(Candidate {
        end,
        cost: cost(weight) + c,
        rank,
        tag: super::lazy::tag(|| format!("{class} {{ {} }}", fields())),
    });
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.chars().any(|c| {
        numbers::digit(c).is_none()
            && !('\u{a81}'..='\u{aff}').contains(&c)
            && !matches!(
                c,
                '.' | ','
                    | '/'
                    | '-'
                    | '−'
                    | '–'
                    | '~'
                    | '±'
                    | ' '
                    | 's'
                    | 't'
                    | 'n'
                    | 'd'
                    | 'r'
                    | 'h'
                    | '½'
                    | '¼'
                    | '¾'
                    | '⅓'
                    | '⅔'
                    | '⅕'
                    | '⅖'
                    | '⅗'
                    | '⅘'
                    | '⅙'
                    | '⅚'
                    | '⅐'
                    | '⅛'
                    | '⅜'
                    | '⅝'
                    | '⅞'
                    | '⅑'
                    | '⅒'
            )
    }) {
        return;
    }
    let (unsigned, neg) = sign(s);
    if !unsigned.is_empty() && unsigned.chars().all(digit) {
        numbers::for_each_graph(unsigned, &mut |n, c| {
            push(out, end, 1.1, c, 0, "cardinal", || format!("{}integer: \"{n}\"", signed(neg)));
        });
        if unsigned.chars().next().is_some_and(is_zero) {
            if let Some(n) = numbers::digitwise(unsigned) {
                push(out, end, 1.1, cost(0.5), 0, "cardinal", || format!("{}integer: \"{n}\"", signed(neg)));
            }
        }
        return;
    }
    for (n, c) in cardinal(unsigned) {
        push(out, end, 1.1, c, 0, "cardinal", || format!("{}integer: \"{n}\"", signed(neg)));
    }
    if neg.is_none() {
        if let Some((a, b)) = s.split_once('–') {
            for (x, cx) in numbers::graph(a.strip_suffix(' ').unwrap_or(a)) {
                for (y, cy) in numbers::graph(b.strip_prefix(' ').unwrap_or(b)) {
                    push(out, end, 1.1, cx + cy, 0, "cardinal", || format!("integer: \"{x} થી {y}\""));
                }
            }
        }
    }
    let mut quantity_options = Vec::new();
    if unsigned.contains('.') {
        quantity_options.push((unsigned, None));
    }
    for (k, v) in rows(QUANTITIES) {
        if let Some(prefix) = unsigned.strip_suffix(k) {
            quantity_options.push((prefix.strip_suffix(' ').unwrap_or(prefix), Some(v)));
        }
    }
    for (n, q) in quantity_options {
        let (int, frac) = match n.split_once('.') {
            Some((a, b)) => (a, Some(b)),
            None => (n, None),
        };
        if frac.is_none() && q.is_none() {
            continue;
        }
        let f = match frac {
            Some(x) if !x.is_empty() => numbers::digitwise(x),
            Some(_) => None,
            None => Some(String::new()),
        };
        let Some(f) = f else { continue };
        for (v, c) in numbers::graph(int) {
            push(out, end, 1.1, c, 1, "decimal", || {
                let mut fields = format!("{}integer_part: \"{v}\"", signed(neg));
                if frac.is_some() {
                    fields.push_str(&format!(" fractional_part: \"{f}\""));
                }
                if let Some(q) = q {
                    fields.push_str(&format!(" quantity: \"{q}\""));
                }
                fields
            });
        }
    }
    if neg.is_none() {
        if let Some(v) = table(EXCEPTIONS, s) {
            push(out, end, 1.1, cost(-0.1), 9, "ordinal", || format!("integer: \"{v}\""));
        }
        for (k, v) in rows(SUFFIX) {
            if let Some(n) = s.strip_suffix(k) {
                let parts: Vec<_> = n.split(',').collect();
                let valid_grouping = match parts.as_slice() {
                    [a, b] => (1..=2).contains(&a.chars().count()) && b.chars().count() == 3,
                    [a, b, c] => {
                        (1..=2).contains(&a.chars().count()) && b.chars().count() == 2 && c.chars().count() == 3
                    }
                    _ => false,
                };
                let clean = if valid_grouping { parts.concat() } else { n.to_owned() };
                let length = clean.chars().count();
                if (!n.contains(',') && (2..=5).contains(&length)) || (valid_grouping && (2..=7).contains(&length)) {
                    for (spoken, c) in numbers::graph(&clean) {
                        push(out, end, 1.1, c, 9, "ordinal", || format!("integer: \"{spoken}{v}\""));
                    }
                }
            }
        }
    }
    if let Some(v) = table(COMMON, unsigned) {
        push(out, end, 1.05, cost(-0.3), 2, "fraction", || format!("{}morphosyntactic_features: \"{v}\"", signed(neg)));
    }
    let unicode = [
        ('½', "1/2"),
        ('¼', "1/4"),
        ('¾', "3/4"),
        ('⅓', "1/3"),
        ('⅔', "2/3"),
        ('⅕', "1/5"),
        ('⅖', "2/5"),
        ('⅗', "3/5"),
        ('⅘', "4/5"),
        ('⅙', "1/6"),
        ('⅚', "5/6"),
        ('⅐', "1/7"),
        ('⅛', "1/8"),
        ('⅜', "3/8"),
        ('⅝', "5/8"),
        ('⅞', "7/8"),
        ('⅑', "1/9"),
        ('⅒', "1/10"),
    ];
    let mut forms: Vec<(String, String)> = Vec::new();
    if let Some((a, b)) = unsigned.split_once('/') {
        match (a.strip_suffix(' '), b.strip_prefix(' ')) {
            (Some(a), Some(b)) => forms.push((a.to_owned(), b.to_owned())),
            (None, None) => forms.push((a.to_owned(), b.to_owned())),
            _ => {}
        }
    }
    if let Some(c) = unsigned.chars().last() {
        if let Some((_, f)) = unicode.iter().find(|(u, _)| *u == c) {
            let prefix = &unsigned[..unsigned.len() - c.len_utf8()];
            let (a, b) = f.split_once('/').unwrap();
            forms.push((
                format!(
                    "{}{a}",
                    prefix.strip_suffix(' ').unwrap_or(prefix).to_owned() + if prefix.is_empty() { "" } else { " " }
                ),
                b.to_owned(),
            ));
        }
    }
    for (a, b) in forms {
        let a = a.as_str();
        let b = b.as_str();
        let (whole, num) = a.rsplit_once(' ').map_or((None, a), |(w, n)| (Some(w), n));
        let wholes = whole.map(cardinal).unwrap_or_else(|| vec![(String::new(), 0)]);
        for (w, cw) in wholes {
            for (n, cn) in cardinal(num) {
                for (d, cd) in cardinal(b) {
                    push(out, end, 1.05, cw + cn + cd, 2, "fraction", || {
                        let mut fields = signed(neg);
                        if whole.is_some() {
                            fields.push_str(&format!("integer_part: \"{w}\" "));
                        }
                        fields.push_str(&format!("numerator: \"{n}\" denominator: \"{d}\""));
                        fields
                    });
                }
            }
        }
    }
    for (fraction, prefix, weight) in [
        ("1/4", "સવા", -0.1),
        ("૧/૪", "સવા", -0.1),
        ("¼", "સવા", -0.1),
        ("1/2", "સાડા", -0.1),
        ("૧/૨", "સાડા", -0.1),
        ("½", "સાડા", -0.1),
        ("3/4", "પોણા", -0.2),
        ("૩/૪", "પોણા", -0.2),
        ("¾", "પોણા", -0.2),
    ] {
        let Some(raw) = unsigned.strip_suffix(fraction) else { continue };
        let num = raw.strip_suffix(' ').unwrap_or(raw);
        if num.is_empty()
            || (!fraction.chars().next().unwrap().is_ascii() && fraction.chars().count() > 1 && !raw.ends_with(' '))
            || (fraction.contains('/') && !raw.ends_with(' '))
        {
            continue;
        }
        if prefix == "પોણા" {
            if let Some(v) = table(PAUNE, num) {
                push(out, end, 1.05, cost(weight), 2, "fraction", || {
                    format!("{}morphosyntactic_features: \"{prefix} {v}\"", signed(neg))
                });
            }
        } else {
            for (n, c) in cardinal(num) {
                push(out, end, 1.05, c + cost(weight), 2, "fraction", || {
                    format!("{}morphosyntactic_features: \"{prefix} {n}\"", signed(neg))
                });
            }
            if prefix == "સાડા" && matches!(num, "1" | "૧" | "2" | "૨") {
                let v = if matches!(num, "1" | "૧") { "દોઢ" } else { "અઢી" };
                push(out, end, 1.05, cost(-0.2), 2, "fraction", || {
                    format!("{}morphosyntactic_features: \"{v}\"", signed(neg))
                });
            }
        }
    }
}

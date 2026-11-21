//! Date, era, year range and decade graphs.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
const DAYS: &str = include_str!("../../../data/gu/date/days.tsv");
const MONTHS: &str = include_str!("../../../data/gu/date/months.tsv");
const ABBR: &str = include_str!("../../../data/gu/date/month_abbr.tsv");
const ERAS: &str = include_str!("../../../data/gu/date/year_suffix.tsv");
static WORDS: std::sync::LazyLock<super::address::KeywordIndex> = std::sync::LazyLock::new(|| {
    let mut out = super::address::KeywordIndex::new();
    for word in
        rows(ABBR).map(|(key, _)| key).chain(rows(MONTHS).map(|(_, value)| value)).chain(rows(ERAS).map(|(key, _)| key))
    {
        out.insert(word);
    }
    out
});
pub(crate) fn possible_start(s: &str) -> bool {
    let Some(first) = s.chars().next() else { return false };
    numbers::digit(first).is_some() || first == '\'' || WORDS.get(first).iter().any(|word| s.starts_with(*word))
}
pub(crate) fn possible_char(c: char) -> bool {
    struct Alphabet {
        ascii: [bool; 128],
        gujarati: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static ALPHABET: std::sync::LazyLock<Alphabet> = std::sync::LazyLock::new(|| {
        let mut a = Alphabet { ascii: [false; 128], gujarati: [false; 128], other: Default::default() };
        let words = rows(DAYS)
            .map(|(key, _)| key)
            .chain(rows(MONTHS).flat_map(|(key, value)| [key, value]))
            .chain(rows(ABBR).map(|(key, _)| key))
            .chain(rows(ERAS).map(|(key, _)| key))
            .chain(["'s", "નો દાયકો", "ના દાયકામાં", "-/.– ,"]);
        for c in words.flat_map(str::chars) {
            let n = c as usize;
            if n < 128 {
                a.ascii[n] = true;
            } else if (0x0a80..=0x0aff).contains(&n) {
                a.gujarati[n - 0x0a80] = true;
            } else {
                a.other.insert(c);
            }
        }
        a
    });
    if numbers::digit(c).is_some() {
        return true;
    }
    let n = c as usize;
    if n < 128 {
        ALPHABET.ascii[n]
    } else if (0x0a80..=0x0aff).contains(&n) {
        ALPHABET.gujarati[n - 0x0a80]
    } else {
        ALPHABET.other.contains(&c)
    }
}
pub(crate) fn might_match(s: &str) -> bool {
    if s.ends_with('s') || s.ends_with("નો દાયકો") || s.ends_with("ના દાયકામાં") {
        return true;
    }
    let digit_end = s.char_indices().find_map(|(i, c)| numbers::digit(c).is_none().then_some(i)).unwrap_or(s.len());
    if digit_end > 0 {
        let tail = &s[digit_end..];
        let tail = tail.strip_prefix(' ').unwrap_or(tail);
        if tail.starts_with(|c| matches!(c, '-' | '/' | '.' | '–')) {
            return true;
        }
    }
    let tail = if digit_end == 0 { s } else { s[digit_end..].strip_prefix(' ').unwrap_or(&s[digit_end..]) };
    tail.chars().next().is_some_and(|first| WORDS.get(first).iter().any(|word| tail.starts_with(*word)))
}
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
static ERA_ROWS: std::sync::LazyLock<Vec<(&'static str, &'static str)>> =
    std::sync::LazyLock::new(|| rows(ERAS).collect());
fn lookup(data: &'static str, s: &str) -> Option<&'static str> {
    use std::collections::HashMap;
    use std::sync::LazyLock;
    fn index(data: &'static str) -> HashMap<&'static str, &'static str> {
        let mut out = HashMap::new();
        for (key, value) in rows(data) {
            out.entry(key).or_insert(value);
        }
        out
    }
    static DAYS_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(DAYS));
    static MONTHS_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(MONTHS));
    static ABBR_INDEX: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| index(ABBR));
    if std::ptr::eq(data, DAYS) {
        DAYS_INDEX.get(s).copied()
    } else if std::ptr::eq(data, MONTHS) {
        MONTHS_INDEX.get(s).copied()
    } else if std::ptr::eq(data, ABBR) {
        ABBR_INDEX.get(s).copied()
    } else {
        rows(data).find_map(|(k, v)| (k == s).then_some(v))
    }
}
fn month_name(s: &str) -> Option<&'static str> {
    use std::collections::HashMap;
    use std::sync::LazyLock;
    static NAMES: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| {
        let mut out = HashMap::new();
        for (_, value) in rows(MONTHS) {
            out.entry(value).or_insert(value);
        }
        out
    });
    lookup(ABBR, s).or_else(|| NAMES.get(s).copied())
}
fn year(s: &str) -> Vec<(String, Cost)> {
    if s.chars().count() != 4 || !matches!(s.chars().next(), Some('1' | '2' | '૧' | '૨')) {
        return Vec::new();
    }
    numbers::graph(s)
}
fn era_year(s: &str) -> Vec<(String, Cost)> {
    if s.is_empty() || s.chars().count() > 4 || matches!(s.chars().next(), Some('0' | '૦')) {
        return Vec::new();
    }
    numbers::graph(s)
}
fn emit(out: &mut Vec<Candidate>, end: usize, fields: impl FnOnce() -> String, c: Cost) {
    out.push(Candidate {
        end,
        cost: cost(1.1) + c,
        rank: 4,
        tag: super::lazy::tag(|| format!("date {{ {} }}", fields())),
    });
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.chars().any(|c| matches!(c, ':' | '\t' | '\n' | '\r' | '\u{a0}')) {
        return;
    }
    for sep in ['-', '/', '.'] {
        let mut parts = s.split(sep);
        let first = parts.next().unwrap_or("");
        let Some(second) = parts.next() else { continue };
        let third = parts.next();
        if parts.next().is_some() {
            continue;
        }
        if let Some(third) = third {
            if let (Some(d), Some(m)) = (lookup(DAYS, first), lookup(MONTHS, second)) {
                for (y, c) in year(third) {
                    emit(out, end, || format!("day: \"{d}\" month: \"{m}\" year: \"{y}\""), c + cost(-0.001));
                }
            }
            if sep == '-' {
                if let (Some(m), Some(d)) = (lookup(MONTHS, second), lookup(DAYS, third)) {
                    for (y, c) in year(first) {
                        emit(out, end, || format!("year: \"{y}\" month: \"{m}\" day: \"{d}\""), c + cost(-0.001));
                    }
                }
            }
        } else if sep == '-' {
            if let (Some(d), Some(m)) = (lookup(DAYS, first), lookup(MONTHS, second)) {
                emit(out, end, || format!("day: \"{d}\" month: \"{m}\""), cost(-0.001));
            }
        }
    }
    for sep in ['-', '–'] {
        if let Some((a, b)) = s.split_once(sep) {
            let a = a.strip_suffix(' ').unwrap_or(a);
            let b = b.strip_prefix(' ').unwrap_or(b);
            for (x, cx) in year(a) {
                for (y, cy) in year(b) {
                    emit(out, end, || format!("year: \"{x} થી {y}\""), cx + cy + cost(-0.005));
                }
            }
        }
    }
    for &(k, v) in ERA_ROWS.iter() {
        if let Some(n) = s.strip_suffix(k).and_then(|x| x.strip_suffix(' ')) {
            for (y, c) in era_year(n) {
                emit(out, end, || format!("year: \"{y}\" era: \"{v}\""), c + cost(-0.005));
            }
        }
        if let Some(n) = s.strip_prefix(k).and_then(|x| x.strip_prefix(' ')) {
            for (y, c) in era_year(n) {
                emit(out, end, || format!("era: \"{v}\" year: \"{y}\" preserve_order: true"), c + cost(-0.005));
            }
        }
    }
    let mut decades = Vec::new();
    for suffix in ["'s", "s"] {
        if let Some(n) = s.strip_suffix(suffix) {
            decades.push((n.strip_prefix('\'').unwrap_or(n), "નો દાયકો"));
        }
    }
    for suffix in ["નો દાયકો", "ના દાયકામાં"] {
        if let Some(n) = s.strip_suffix(suffix) {
            decades.push((n.strip_suffix(' ').unwrap_or(n), suffix));
        }
    }
    for (n, suffix) in decades {
        if !matches!(n.chars().last(), Some('0' | '૦')) {
            continue;
        }
        let readings = if n.chars().count() == 4 {
            year(n)
        } else if n.chars().count() == 2 && !matches!(n.chars().next(), Some('0' | '૦')) {
            numbers::graph(n)
        } else {
            Vec::new()
        };
        for (y, c) in readings {
            emit(out, end, || format!("year: \"{y} {suffix}\""), c + cost(-0.01));
        }
    }
    // Name-based forms: source allows one optional comma and one optional space before the year.
    for (i, ch) in s.char_indices() {
        if ch != ' ' {
            continue;
        }
        let left = &s[..i];
        let rest = &s[i + 1..];
        if let Some(d) = lookup(DAYS, left) {
            if let Some(m) = month_name(rest) {
                emit(out, end, || format!("day: \"{d}\" month: \"{m}\""), cost(-0.001));
            }
            for (j, _) in rest.char_indices().skip(1) {
                if let Some(m) = month_name(&rest[..j]) {
                    let tail = &rest[j..];
                    let tail = tail.strip_prefix(',').unwrap_or(tail);
                    let tail = tail.strip_prefix(' ').unwrap_or(tail);
                    let mut variants = vec![(tail, None)];
                    for &(k, v) in ERA_ROWS.iter() {
                        if let Some(y) = tail.strip_suffix(k).and_then(|x| x.strip_suffix(' ')) {
                            variants.push((y, Some(v)));
                        }
                    }
                    for (t, e) in variants {
                        for (y, c) in year(t) {
                            emit(
                                out,
                                end,
                                || {
                                    format!(
                                        "day: \"{d}\" month: \"{m}\" year: \"{y}\"{}",
                                        e.map(|v| format!(" era: \"{v}\"")).unwrap_or_default()
                                    )
                                },
                                c + cost(-0.003),
                            );
                        }
                    }
                }
            }
        }
    }
    for (i, _) in s.char_indices().skip(1) {
        if let Some(m) = month_name(&s[..i]) {
            let t = &s[i..];
            let t = t.strip_prefix(',').unwrap_or(t);
            let t = t.strip_prefix(' ').unwrap_or(t);
            let mut variants = vec![(t, None)];
            for &(k, v) in ERA_ROWS.iter() {
                if let Some(y) = t.strip_suffix(k).and_then(|x| x.strip_suffix(' ')) {
                    variants.push((y, Some(v)));
                }
            }
            for (t, e) in variants {
                for (y, c) in year(t) {
                    emit(
                        out,
                        end,
                        || {
                            format!(
                                "month: \"{m}\" year: \"{y}\"{}",
                                e.map(|v| format!(" era: \"{v}\"")).unwrap_or_default()
                            )
                        },
                        c + cost(-0.2),
                    );
                }
            }
        }
    }
}

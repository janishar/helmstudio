//! `es/taggers/ordinal.py`.

use super::cardinal;
use crate::lang::es::graph_utils::{roman_to_int, strip_accent};
use crate::lang::es::{Lazy, Sink, ok_end, prefix_static};
use crate::text::{Cost, Table, cost};

crate::tsv!(ORD_DIGIT, "es/ordinals/digit.tsv");
crate::tsv!(ORD_TEEN, "es/ordinals/teen.tsv");
crate::tsv!(ORD_TWENTIES, "es/ordinals/twenties.tsv");
crate::tsv!(ORD_TIES, "es/ordinals/ties.tsv");
crate::tsv!(ORD_HUNDREDS, "es/ordinals/hundreds.tsv");
crate::tsv!(GENDER_SUFFIX, "es/ordinals/gender_suffix.tsv");
crate::tsv!(ROMAN_EXCEPTIONS, "es/ordinals/roman_exceptions.tsv");

/// Inverted table lookup: cardinal word → ordinal word.
fn inv(t: &'static Table, w: &str) -> Option<&'static str> {
    t.rows().iter().find(|r| r.value == w).map(|r| r.key.as_str())
}

/// `graph_tens_component` on a whole string.
fn tens_component(s: &str) -> Option<String> {
    if let Some(o) = inv(&ORD_TEEN, s).or_else(|| inv(&ORD_TWENTIES, s)) {
        return Some(o.to_string());
    }
    if let Some(o) = inv(&ORD_TIES, s) {
        return Some(o.to_string());
    }
    let (t, d) = s.split_once(" y ")?;
    Some(format!("{} {}", inv(&ORD_TIES, t)?, inv(&ORD_DIGIT, d)?))
}

/// `graph_hundred_component` on a whole string.
fn hundred_component(s: &str) -> Option<String> {
    if let Some(o) = tens_component(s).or_else(|| inv(&ORD_DIGIT, s).map(str::to_string)) {
        return Some(o);
    }
    if let Some(o) = inv(&ORD_HUNDREDS, s) {
        return Some(o.to_string());
    }
    let (h, rest) = s.split_once(' ')?;
    let h = inv(&ORD_HUNDREDS, h)?;
    let r = tens_component(rest).or_else(|| inv(&ORD_DIGIT, rest).map(str::to_string))?;
    Some(format!("{h} {r}"))
}

static ONE_TO_THOUSAND: std::sync::LazyLock<std::collections::HashSet<String>> =
    std::sync::LazyLock::new(|| (1..1000u32).filter_map(|n| cardinal::number(&n.to_string()).map(|x| x.0)).collect());

/// `one_to_one_thousand` membership: the cardinal output of 1..999.
pub fn one_to_thousand(s: &str) -> bool {
    ONE_TO_THOUSAND.contains(s)
}

/// `graph_thousands` (before the optional hundred component) on a cardinal string.
fn thousands(s: &str) -> Option<String> {
    if s == "mil" {
        return Some("milésimo".into());
    }
    let pre = s.strip_suffix(" mil")?;
    if !one_to_thousand(pre) {
        return None;
    }
    let mut p = strip_accent(pre);
    // cdrewrite(delete_space, "", "milésimo"): the whitespace right before "milésimo" goes
    p.push_str("milésimo");
    Some(p)
}

/// `ordinal_graph` (cardinal words → ordinal words) including the "o" + "octavo" deletion.
pub fn from_cardinal(s: &str) -> Option<String> {
    let mut res = hundred_component(s).or_else(|| thousands(s));
    if res.is_none() {
        // thousands + " " + hundred component: split at each " " after "mil"
        for (i, _) in s.match_indices(" ") {
            let (a, b) = (&s[..i], &s[i + 1..]);
            if a.ends_with("mil") {
                if let (Some(t), Some(h)) = (thousands(a), hundred_component(b)) {
                    res = Some(format!("{t} {h}"));
                    break;
                }
            }
        }
    }
    let r = res?;
    Some(delete_o_before_octavo(&r))
}

fn delete_o_before_octavo(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut i = 0;
    while i < s.len() {
        if s[i..].starts_with("ooctavo") {
            i += 1;
            continue;
        }
        let c = s[i..].chars().next().unwrap();
        out.push(c);
        i += c.len_utf8();
    }
    out
}

/// `OrdinalFst.graph` on digits (after `filter_punctuation`), with its cost.
pub fn graph_digits(d: &str) -> Option<(String, Cost)> {
    let (w, c) = cardinal::number(d)?;
    Some((from_cardinal(&w)?, c))
}

/// `multiples_of_thousand`: digits → "Xmilésimo".
pub fn multiples_of_thousand(d: &str) -> Option<(String, Cost)> {
    let (w, c) = cardinal::number(d)?;
    Some((thousands(&w)?, c))
}

static GENDER_FIRST: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let mut v: Vec<char> = GENDER_SUFFIX.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.push('.');
    v
});

pub struct Ordinal;

impl Lazy for Ordinal {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        for end in cardinal::number_ends(text, start) {
            if !text.get(end).is_some_and(|c| GENDER_FIRST.contains(c)) {
                continue;
            }
            let mut ends = Vec::new();
            for (row, e) in prefix_static(&GENDER_SUFFIX, text, end) {
                ends.push((row, e));
            }
            if text.get(end) == Some(&'.') {
                for (row, e) in prefix_static(&GENDER_SUFFIX, text, end + 1) {
                    ends.push((row, e));
                }
            }
            ends.retain(|&(_, e)| !(e == end || (e == end + 1 && text[end] == '.')));
            if ends.is_empty() {
                continue;
            }
            let Some(d) = cardinal::filter_punctuation(&text[start..end]) else {
                continue;
            };
            let Some((w, c)) = graph_digits(&d) else {
                continue;
            };
            for (row, e) in ends {
                out.push(e, cost(1.1) + c + row.cost, 0, || {
                    format!("ordinal {{ integer: \"{w}\" morphosyntactic_features: \"{}\" }}", row.value)
                });
            }
        }
        // roman numerals
        let mut end = start;
        while end < text.len() && "ivxlcdmIVXLCDM".contains(text[end]) {
            end += 1;
            if !ok_end(text, end) {
                continue;
            }
            let s: String = text[start..end].iter().collect();
            if ROMAN_EXCEPTIONS.contains_key(&s) {
                continue;
            }
            for (digits, rc) in roman_to_int(&s) {
                let Some((w, c)) = graph_digits(&digits) else {
                    continue;
                };
                for (rank, g) in [(1, "gender_masc"), (2, "gender_fem")] {
                    out.push(end, cost(1.1) + c + rc, rank, || {
                        format!("ordinal {{ integer: \"{w}\" morphosyntactic_features: \"{g}\" }}")
                    });
                }
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::lang::es::test_util::tags;

    #[test]
    fn roman_ordinal_is_masculine() {
        // alternatives: gender_masc (rank 1) vs gender_fem (rank 2)
        assert_eq!(
            tags("Felipe II"),
            "tokens { name: \"Felipe\" } tokens { ordinal { integer: \"segundo\" morphosyntactic_features: \"gender_masc\" } }"
        );
    }

    use super::*;

    #[test]
    fn ordinals() {
        assert_eq!(graph_digits("21").unwrap().0, "vigesimoprimero");
        assert_eq!(graph_digits("18").unwrap().0, "decimoctavo");
        assert_eq!(graph_digits("2000").unwrap().0, "dosmilésimo");
        assert_eq!(graph_digits("1105").unwrap().0, "milésimo centésimo quinto");
        assert_eq!(graph_digits("35").unwrap().0, "trigésimo quinto");
    }
}

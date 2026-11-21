//! `pt/taggers/ordinal.py`: 1-3 digits, optional `.`, then `º`/`°` (masculine) or `ª` (feminine); the cardinal
//! words are mapped to ordinal words (`ordinal_rewrite`).

use std::sync::LazyLock;

use super::cardinal;
use crate::lang::pt::graph_utils::SpanTagger;
use crate::text::{Cost, Table, is_digit};

crate::labels!(DIGIT, "pt/ordinals/digit.tsv");
crate::labels!(TEEN, "pt/ordinals/teen.tsv");
crate::labels!(TIES, "pt/ordinals/ties.tsv");
crate::labels!(HUNDREDS, "pt/ordinals/hundreds.tsv");
crate::labels!(SPECIALS, "pt/ordinals/specials.tsv");

/// `string_map([(r[1], r[0]) …])`: cardinal word → ordinal word.
fn inverted(rows: &[Vec<String>]) -> Table {
    Table::from_pairs(rows.iter().filter(|r| r.len() >= 2).map(|r| (r[1].clone(), r[0].clone())))
}

pub static ORD_DIGIT: LazyLock<Table> = LazyLock::new(|| inverted(&DIGIT));
static ORD_TEEN: LazyLock<Table> = LazyLock::new(|| inverted(&TEEN));
static ORD_TIES: LazyLock<Table> = LazyLock::new(|| inverted(&TIES));
pub static ORD_HUNDREDS: LazyLock<Table> = LazyLock::new(|| inverted(&HUNDREDS));
static CONN: LazyLock<(String, String)> = LazyLock::new(|| {
    let get = |k: &str, d: &str| {
        SPECIALS.iter().find(|r| r.len() >= 2 && r[0] == k).map(|r| r[1].clone()).unwrap_or_else(|| d.to_string())
    };
    (get("connector_in", " e "), get("connector_out", " "))
});

fn map1(t: &Table, k: &str) -> Option<String> {
    t.exact(k).next().map(|r| r.value.clone())
}

/// `graph_tens | graph_digit` on cardinal words.
fn tens_or_digit(s: &str) -> Option<String> {
    let (conn_in, conn_out) = &*CONN;
    if let Some(v) = map1(&ORD_TEEN, s).or_else(|| map1(&ORD_DIGIT, s)).or_else(|| map1(&ORD_TIES, s)) {
        return Some(v);
    }
    let (a, b) = s.split_once(conn_in.as_str())?;
    Some(format!("{}{}{}", map1(&ORD_TIES, a)?, conn_out, map1(&ORD_DIGIT, b)?))
}

/// `ordinal_rewrite` (`graph_hundred_component`) on the cardinal words of 1-3 digits.
pub fn rewrite(s: &str) -> Option<String> {
    let (conn_in, conn_out) = &*CONN;
    if let Some(v) = map1(&ORD_HUNDREDS, s) {
        return Some(v);
    }
    if let Some((h, rest)) = s.split_once(conn_in.as_str()) {
        if let Some(hv) = map1(&ORD_HUNDREDS, h) {
            return Some(format!("{hv}{conn_out}{}", tens_or_digit(rest)?));
        }
    }
    tens_or_digit(s)
}

pub struct Ordinal;

impl SpanTagger for Ordinal {
    fn max_words(&self, c: char) -> usize {
        usize::from(is_digit(c))
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        let nd = span.iter().take_while(|c| is_digit(**c)).count();
        if !(1..=3).contains(&nd) {
            return None;
        }
        let mut rest = &span[nd..];
        if rest.first() == Some(&'.') {
            rest = &rest[1..];
        }
        let gender = match rest {
            ['º' | '°'] => "gender_masc",
            ['ª'] => "gender_fem",
            _ => return None,
        };
        let (words, c) = cardinal::graph_digits(&span[..nd])?;
        let ord = rewrite(&words)?;
        Some((c, format!("ordinal {{ integer: \"{ord}\" morphosyntactic_features: \"{gender}\" }}")))
    }
}

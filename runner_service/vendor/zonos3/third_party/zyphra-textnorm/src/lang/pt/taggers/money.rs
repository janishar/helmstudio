//! `pt/taggers/money.py`.

use std::sync::LazyLock;

use super::{cardinal, decimal};
use crate::lang::pt::graph_utils::{SpanTagger, is_numeric_word, words_while};
use crate::text::{Cost, cost, is_alpha, is_digit, is_nemo_space};

crate::tsv!(MAJ, "pt/money/currency_major.tsv");
crate::tsv!(MAJ_PLURAL, "pt/money/currency_major_plural.tsv");
crate::tsv!(MIN, "pt/money/currency_minor.tsv");
crate::tsv!(MIN_PLURAL, "pt/money/currency_minor_plural.tsv");

struct Currency {
    symbol: Vec<char>,
    maj: String,
    maj_plural: Option<String>,
    min: Option<String>,
    min_plural: Option<String>,
}

static CURRENCIES: LazyLock<Vec<Currency>> = LazyLock::new(|| {
    let first = |t: &crate::text::Table, k: &str| t.exact(k).next().map(|r| r.value.clone());
    MAJ.rows()
        .iter()
        .map(|r| {
            let min = first(&MIN, &r.key);
            Currency {
                symbol: r.key.chars().collect(),
                maj: r.value.clone(),
                maj_plural: first(&MAJ_PLURAL, &r.value),
                min_plural: min.as_deref().and_then(|m| first(&MIN_PLURAL, m)),
                min,
            }
        })
        .collect()
});

fn starts(s: &[char], cur: &Currency) -> bool {
    s.first() == Some(&cur.symbol[0]) && s.starts_with(&cur.symbol)
}

fn ends(s: &[char], cur: &Currency) -> bool {
    s.last() == cur.symbol.last() && s.ends_with(&cur.symbol)
}

/// Whether some currency symbol starts or ends `span` (every money branch keeps one at an end).
fn has_symbol_end(span: &[char]) -> bool {
    CURRENCIES.iter().any(|cur| starts(span, cur) || ends(span, cur))
}

/// Skip `closure(delete_space, 0, 1)` (any whitespace run).
fn skip_ws(s: &[char], mut i: usize) -> usize {
    while i < s.len() && is_nemo_space(s[i]) {
        i += 1;
    }
    i
}

fn trim_ws_end(s: &[char]) -> &[char] {
    let mut e = s.len();
    while e > 0 && is_nemo_space(s[e - 1]) {
        e -= 1;
    }
    &s[..e]
}

/// `two_digits_fractional_part`: the 1-2 significant digits after dropping trailing zeros.
fn two_digits(f: &[char]) -> Option<(u8, u8)> {
    if f.is_empty() || !f.iter().all(|c| is_digit(*c)) {
        return None;
    }
    let mut e = f.len();
    while e > 0 && f[e - 1] == '0' {
        e -= 1;
    }
    let d = |c: char| c as u8 - b'0';
    match &f[..e] {
        [x] => Some((d(*x), 0)),
        ['0', x] => Some((0, d(*x))),
        [x, y] => Some((d(*x), d(*y))),
        _ => None,
    }
}

/// `fractional_plus_min` for one currency.
fn fractional_plus_min(f: &[char], cur: &Currency, out: &mut String) -> Option<()> {
    let (a, b) = two_digits(f)?;
    out.push_str("fractional_part: \"");
    if (a, b) == (0, 1) {
        out.push_str("um\" currency_min: \"");
        out.push_str(cur.min.as_deref()?);
    } else {
        let num = &cardinal::NUM;
        if a == 0 {
            out.push_str(&num.digit[b as usize]);
        } else if !num.tens2(a, b, out) {
            return None;
        }
        out.push_str("\" currency_min: \"");
        out.push_str(cur.min_plural.as_deref()?);
    }
    out.push('"');
    Some(())
}

/// `decimal_graph_with_minor_curr` before the symbol handling.
fn with_minor(s: &[char], cur: &Currency) -> Option<(Cost, String)> {
    let comma = s.iter().position(|&c| c == ',')?;
    let (int, frac) = (&s[..comma], &s[comma + 1..]);
    let mut out = String::new();
    if int == ['0'] {
        fractional_plus_min(frac, cur, &mut out)?;
        return Some((0, out));
    }
    if !int.iter().all(|c| is_digit(*c)) {
        return None;
    }
    let (w, c) = cardinal::graph_digits(int)?;
    out.push_str("integer_part: \"");
    out.push_str(&w);
    out.push_str("\" currency_maj: \"");
    if int == ['1'] {
        out.push_str(&cur.maj);
    } else {
        out.push_str(cur.maj_plural.as_deref()?);
    }
    out.push_str("\" ");
    fractional_plus_min(frac, cur, &mut out)?;
    Some((c, out))
}

fn minor_branch(s: &[char]) -> Option<(Cost, String)> {
    for cur in CURRENCIES.iter() {
        let n = cur.symbol.len();
        let found = if starts(s, cur) {
            with_minor(&s[skip_ws(s, n)..], cur)
        } else if ends(s, cur) {
            with_minor(trim_ws_end(&s[..s.len() - n]), cur)
        } else {
            None
        };
        if let Some((c, body)) = found {
            return Some((c + cost(-0.001), format!("{body} preserve_order: true")));
        }
    }
    None
}

/// The amount of `graph_integer_only | graph_decimal_singular | graph_decimal_plural`: `(cost, fields)`.
/// The decimal branches restrict the **whole** money input: singular needs it to start `1,`, plural needs a
/// comma whose prefix is not `1`; with the symbol first the input never starts `1`.
fn amount(num: &[char], singular: bool, symbol_first: bool) -> Option<(Cost, String)> {
    if let Some(p) = num.iter().position(|&c| c == ',') {
        let starts_one = !symbol_first && &num[..p] == ['1'];
        // plural: some comma with a prefix ≠ "1" (a later comma's prefix is never "1")
        let plural_ok = !starts_one || num[p + 1..].contains(&',');
        if (singular && !starts_one) || (!singular && !plural_ok) {
            return None;
        }
        return decimal::body_wo_negative(num);
    }
    if singular {
        return (num == ['1']).then(|| (0, "integer_part: \"um\"".to_string()));
    }
    if num == ['1'] {
        return None;
    }
    let (w, c) = cardinal::graph(num)?;
    Some((c, format!("integer_part: \"{w}\"")))
}

fn major_branch(s: &[char]) -> Option<(Cost, String)> {
    for cur in CURRENCIES.iter() {
        let n = cur.symbol.len();
        if starts(s, cur) {
            let num = &s[skip_ws(s, n)..];
            // symbol first: singular, plural (integer/decimal), plural + decimal_with_quantity
            if let Some((c, a)) = amount(num, true, true) {
                return Some((c, format!("currency_maj: \"{}\" {a}", cur.maj)));
            }
            let plural = cur.maj_plural.as_deref()?;
            if let Some((c, a)) = amount(num, false, true) {
                return Some((c, format!("currency_maj: \"{plural}\" {a}")));
            }
            if num.last().is_some_and(|c| is_alpha(*c)) {
                if let Some((c, a)) = decimal::body_wo_negative(num) {
                    return Some((c, format!("currency_maj: \"{plural}\" {a}")));
                }
            }
        }
        if ends(s, cur) {
            let num = trim_ws_end(&s[..s.len() - n]);
            if let Some((c, a)) = amount(num, true, false) {
                return Some((c, format!("{a} currency_maj: \"{}\"", cur.maj)));
            }
            let Some(plural) = cur.maj_plural.as_deref() else { continue };
            if let Some((c, a)) = amount(num, false, false) {
                return Some((c, format!("{a} currency_maj: \"{plural}\"")));
            }
        }
    }
    None
}

pub struct Money;

impl SpanTagger for Money {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) || CURRENCIES.iter().any(|cur| cur.symbol[0] == c) { 14 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        // symbol / number words, then a quantity word and a trailing symbol
        if self.max_words(text[start]) == 0 {
            return 0;
        }
        let symbolic =
            |w: &[char]| is_numeric_word(w) || w.iter().all(|c| !c.is_alphabetic() || c.is_ascii_uppercase());
        words_while(text, start, 12, symbolic) + 2
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        if !has_symbol_end(span) || !span.iter().any(|c| is_digit(*c)) {
            return None;
        }
        let (c, body) = minor_branch(span).or_else(|| major_branch(span))?;
        Some((c, format!("money {{ {body} }}")))
    }
}

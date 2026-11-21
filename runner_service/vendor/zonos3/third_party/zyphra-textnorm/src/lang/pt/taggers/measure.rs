//! `pt/taggers/measure.py`: a decimal, cardinal (`1` → singular unit) or fraction token, optional whitespace, a
//! unit. Units are `convert_space`d (NBSP inside the value).

use std::sync::LazyLock;

use super::{cardinal, decimal, fraction};
use crate::lang::pt::graph_utils::{SpanTagger, is_numeric_word, words_while};
use crate::text::{Cost, Row, Table, is_digit, is_nemo_space};

crate::tsv!(SINGULAR, "pt/measure/measurements_singular.tsv");
crate::tsv!(PLURAL, "pt/measure/measurements_plural.tsv");

/// The cheapest unit row whose key is exactly `span[at..]` (`string_file` union: first file row among equal-cost
/// rows).
fn unit<'a>(t: &'a Table, span: &'a [char], at: usize) -> Option<&'a Row> {
    let mut best: Option<&Row> = None;
    for (r, e) in t.prefix_matches(span, at) {
        if e == span.len() && best.is_none_or(|b| r.cost < b.cost) {
            best = Some(r);
        }
    }
    best
}

/// The longest unit key, in chars.
static MAX_UNIT: LazyLock<usize> =
    LazyLock::new(|| SINGULAR.rows().iter().chain(PLURAL.rows()).map(|r| r.key.chars().count()).max().unwrap_or(0));

/// The last chars of the unit keys, sorted.
static UNIT_LAST: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = SINGULAR.rows().iter().chain(PLURAL.rows()).filter_map(|r| r.key.chars().last()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

pub struct Measure;

impl Measure {
    /// Every `(number, ws, unit)` split of the span, cheapest result. The unit is a suffix of the span, so only
    /// the few suffix starts that are a unit key are tried. The three number readings accept disjoint inputs
    /// (fraction needs a slash, decimal a comma or a quantity word, cardinal neither), so at most one tag is built
    /// per split.
    fn best(span: &[char]) -> Option<(Cost, String)> {
        let mut best: Option<(Cost, String)> = None;
        let lo = span.len().saturating_sub(*MAX_UNIT).max(1);
        for us in lo..span.len() {
            let plural = unit(&PLURAL, span, us);
            let singular = unit(&SINGULAR, span, us);
            if plural.is_none() && singular.is_none() {
                continue;
            }
            let mut ne = us;
            while ne > 0 && is_nemo_space(span[ne - 1]) {
                ne -= 1;
            }
            if ne == 0 {
                continue;
            }
            let ws = us - ne;
            let num = &span[..ne];
            let last = num[num.len() - 1];
            let found = if num.iter().any(|&c| matches!(c, '/' | '\u{2044}' | '\u{2215}')) {
                // subgraph_fraction: fraction.fst + insert_space + delete_space? + unit_plural
                plural.filter(|_| is_digit(last)).and_then(|u| Some((u, fraction::tag(num)?)))
            } else if num.iter().any(|&c| c == ',' || c.is_alphabetic()) {
                // subgraph_decimal: decimal.fst + insert_space + " "? + unit_plural (only one plain space)
                let ok = ws == 0 || (ws == 1 && span[ne] == ' ');
                plural.filter(|_| ok).and_then(|u| Some((u, decimal::tag(num)?)))
            } else {
                // subgraph_cardinal: (-? (Σ* − "1")) @ cardinal.fst … unit_plural | (-? "1") … unit_singular
                let digits = if num.first() == Some(&'-') { &num[1..] } else { num };
                let uv = if digits == ['1'] { singular } else { plural };
                uv.filter(|_| is_digit(last)).and_then(|u| Some((u, cardinal::tag(num)?)))
            };
            if let Some((u, (c, t))) = found {
                if best.as_ref().is_none_or(|(bc, _)| c < *bc) {
                    let mut tag = String::with_capacity(t.len() + u.value.len() + 24);
                    tag.push_str("measure { ");
                    tag.push_str(&t);
                    tag.push_str(" units: \"");
                    tag.extend(u.value.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
                    tag.push_str("\" }");
                    best = Some((c, tag));
                }
            }
        }
        best
    }
}

impl SpanTagger for Measure {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) || c == '-' { 24 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        // number words, then a quantity word and/or the unit
        if self.max_words(text[start]) == 0 { 0 } else { words_while(text, start, 22, is_numeric_word) + 2 }
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        // the span ends in a unit
        if !span.last().is_some_and(|c| UNIT_LAST.binary_search(c).is_ok()) {
            return None;
        }
        Self::best(span)
    }
}

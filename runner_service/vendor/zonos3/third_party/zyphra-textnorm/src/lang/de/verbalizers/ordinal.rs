//! `de/verbalizers/ordinal.py`: `cdrewrite(stem? + insert(suffix, 0.01), "", EOS)`.
//!
//! The five suffixes tie at 0.01; the oracle writes `te` (`Der 50. Geburtstag.` → `fünfzigste`).

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::{Cost, cost};

crate::tsv!(DIGIT, "de/ordinals/digit.tsv");
crate::tsv!(TIES, "de/ordinals/ties.tsv");
crate::tsv!(THOUSANDS, "de/ordinals/thousands.tsv");

/// `ordinal_stem` inverted: `(cardinal word, stem)`, longest first.
static STEMS: LazyLock<Vec<(String, String)>> = LazyLock::new(|| {
    let mut v: Vec<(String, String)> = [&*DIGIT, &*TIES, &*THOUSANDS]
        .iter()
        .flat_map(|t| t.rows().iter().map(|r| (r.value.clone(), r.key.clone())))
        .collect();
    v.sort_by_key(|(k, _)| std::cmp::Reverse(k.len()));
    v
});

/// `cdrewrite(ordinal_stem, "", EOS)`: the stem replaces the longest matching word ending; `None` when none matches.
pub fn stem(s: &str) -> Option<String> {
    STEMS.iter().find_map(|(k, v)| s.strip_suffix(k.as_str()).map(|p| format!("{p}{v}")))
}

const SUFFIX: Cost = cost(0.01);

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let i = f.take("integer")?;
    if !f.done() {
        return None;
    }
    let mut s = stem(i).unwrap_or_else(|| i.to_string());
    s.push_str("te");
    Some((s, SUFFIX))
}

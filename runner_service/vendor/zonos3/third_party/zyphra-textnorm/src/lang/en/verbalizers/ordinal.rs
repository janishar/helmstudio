//! `en/verbalizers/ordinal.py`: the `suffix` rewrite at EOS.

use crate::lang::common::Fields;
use crate::tag::Dict;

crate::tsv!(ORD_DIGIT, "en/ordinal/digit.tsv");
crate::tsv!(ORD_TEEN, "en/ordinal/teen.tsv");

/// `suffix`: cardinal words → ordinal words (`one`→`first`, `twelve`→`twelfth`, `ty`→`tieth`, else `+th`).
pub fn suffix(s: &str) -> String {
    for t in [&*ORD_DIGIT, &*ORD_TEEN] {
        for r in t.rows() {
            if let Some(p) = s.strip_suffix(r.value.as_str()) {
                return format!("{p}{}", r.key);
            }
        }
    }
    if let Some(p) = s.strip_suffix("ty") {
        return format!("{p}tieth");
    }
    format!("{s}th")
}

pub fn ordinal(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let i = f.take("integer")?;
    if !f.done() || i.is_empty() {
        return None;
    }
    Some(suffix(i))
}

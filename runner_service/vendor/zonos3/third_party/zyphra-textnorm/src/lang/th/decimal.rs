//! Thai decimal primitives corresponding to `th/taggers/decimal.py`.

use super::number::{cardinal, digit_words};

/// Positive decimals use a cardinal integer and individually spoken fractional digits.
pub(crate) fn positive(input: &str) -> Option<String> {
    let (integer, fractional) = input.split_once('.')?;
    let mut out = cardinal(integer)?;
    out.push_str("จุด");
    out.push_str(&digit_words(fractional)?);
    Some(out)
}

pub(crate) fn decimal(input: &str) -> Option<String> {
    if let Some(rest) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(format!("ลบ{}", positive(rest)?))
    } else {
        positive(input)
    }
}

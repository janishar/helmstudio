//! `it/verbalizers/time.py`. The 15/30 alternatives ("quindici minuti" vs "un quarto", "trenta minuti" vs
//! " mezza") have equal weight; the oracle picks the phrase (golden `05:15` → "cinque e un quarto", `12:30` →
//! "dodici e mezza"), which this port always emits.

use crate::lang::common::Fields;
use crate::lang::it::taggers::cardinal::{WORDS, two_digit_no_zero};
use crate::tag::Dict;

/// `graph_zero | two_digit_no_zero` over a digit string.
fn number(s: &str) -> Option<String> {
    if !s.bytes().all(|b| b.is_ascii_digit()) || s.is_empty() {
        return None;
    }
    if s == "0" {
        return Some(WORDS.zero.clone());
    }
    let mut out = String::new();
    let mut c = 0;
    two_digit_no_zero(s.as_bytes(), &mut out, &mut c).then_some(out)
}

fn minutes(s: &str) -> Option<String> {
    match s {
        "15" => Some("un quarto".into()),
        "30" => Some(" mezza".into()),
        _ => number(s).map(|n| format!("{n} minuti")),
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let mut out = number(f.take("hours")?)?;
    if let Some(m) = f.opt("minutes")? {
        out.push_str(" e ");
        out.push_str(&minutes(m)?);
        if let Some(s) = f.opt("seconds")? {
            out.push_str(" e ");
            out.push_str(&number(s)?);
            out.push_str(" secondi");
        }
    }
    super::end(&mut f).then_some(out)
}

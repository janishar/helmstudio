//! `de/verbalizers/time.py`.
//!
//! The cheapest branch is always `graph_h`/`graph_hm`/`graph_hms` (0); the `nach`/`halb`/`vor` readings carry
//! +0.0001 and are never chosen, so they are not built.

use std::sync::LazyLock;

use crate::lang::common::Fields;
use crate::lang::de::taggers::cardinal::{W, two_digit};
use crate::tag::Dict;
use crate::text::{Cost, nbsp_to_space};

crate::labels!(ZONE_ROWS, "de/time/time_zone.tsv");

/// The verbalized zone values (`convert_space` inverted: the tag carries NBSP).
static ZONES: LazyLock<Vec<String>> =
    LazyLock::new(|| ZONE_ROWS.iter().filter_map(|r| r.get(1)).map(|v| v.replace(' ', "\u{a0}")).collect());

/// `number_verbalization`: `zero | two_digit_non_zero` on a digit string.
fn number(s: &str) -> Option<String> {
    if !s.bytes().all(|b| b.is_ascii_digit()) || s.is_empty() {
        return None;
    }
    if s == "0" {
        return Some(W.zero.clone());
    }
    two_digit(&s.chars().collect::<Vec<_>>()).map(|x| x.0)
}

fn hour(s: &str) -> Option<String> {
    let w = number(s)?;
    Some(if w == "eins" { "ein uhr".to_string() } else { format!("{w} uhr") })
}

fn zone(f: &mut Fields) -> Option<Option<String>> {
    match f.opt("zone")? {
        Some(z) => ZONES.iter().any(|x| x == z).then(|| Some(nbsp_to_space(z))),
        None => Some(None),
    }
}

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let mut out = hour(f.take("hours")?)?;
    if let Some(m) = f.opt("minutes")? {
        let mw = number(m)?;
        if let Some(sec) = f.opt("seconds")? {
            let sw = number(sec)?;
            let mm = if mw == "eins" { "eine minute".to_string() } else { format!("{mw} minuten") };
            let ss = if sw == "eins" { "eine sekunde".to_string() } else { format!("{sw} sekunden") };
            out = format!("{out} {mm} {ss}");
        } else {
            out = format!("{out} {mw}");
        }
    }
    // optional_zone inside graph_hms, then the outer optional_zone: at most two zones
    for _ in 0..2 {
        if let Some(z) = zone(&mut f)? {
            out.push(' ');
            out.push_str(&z);
        }
    }
    while f.preserve_order() {}
    f.done().then_some((out, 0))
}

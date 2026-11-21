//! `en/taggers/time.py`.

use std::sync::LazyLock;

use crate::lang::en::graph_utils::row_index;
use crate::lang::en::lazy::{Reading, building};
use crate::lang::en::taggers::cardinal::graph;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Table, cost, is_digit, is_nemo_space, match_str};

crate::labels!(SUFFIX_LABELS, "en/time/suffix.tsv");
crate::tsv!(ZONE, "en/time/zone.tsv");

/// `suffix.tsv` + `augment_labels_with_punct_at_end`.
pub static SUFFIX: LazyLock<Table> = LazyLock::new(|| {
    let mut v: Vec<(String, String)> = SUFFIX_LABELS.iter().map(|r| (r[0].clone(), r[1].clone())).collect();
    for r in SUFFIX_LABELS.iter() {
        if r[0].ends_with('.') && !r[1].ends_with('.') {
            v.push((r[0].clone(), format!("{}.", r[1])));
        }
    }
    Table::from_pairs(v)
});

fn ws_end(text: &[char], mut i: usize) -> usize {
    while i < text.len() && is_nemo_space(text[i]) {
        i += 1;
    }
    i
}

/// One `TimeFst` reading: the hour value and the hour digits' end `he`; minutes and seconds (0: absent, 1:
/// "zero", 2: read by [`minutes`] at `he + 1` / `he + 4`); the `SUFFIX` and `zone.tsv` rows.
#[derive(Clone, Copy, Debug)]
pub struct TimeR {
    hour: u8,
    he: u32,
    min: u8,
    sec: u8,
    suffix: Option<u16>,
    zone: Option<u16>,
}

/// The `TimeFst` fields of reading `r` (without the class wrapper).
pub fn fields(text: &[char], r: TimeR) -> String {
    let mut s = String::with_capacity(64);
    let field = |s: &mut String, k: &str, v: &str| {
        s.push_str(k);
        s.push_str(": \"");
        s.push_str(v);
        s.push('"');
    };
    field(&mut s, "hours", &graph(&r.hour.to_string()).unwrap_or_default());
    let he = r.he as usize;
    for (key, kind, pos) in [(" minutes", r.min, he + 1), (" seconds", r.sec, he + 4)] {
        match kind {
            0 => {}
            1 => field(&mut s, key, "zero"),
            _ => field(&mut s, key, &minutes(text, pos).unwrap_or_default()),
        }
    }
    if let Some(i) = r.suffix {
        field(&mut s, " suffix", &SUFFIX.rows()[i as usize].value.replace(' ', "\u{a0}"));
    }
    if let Some(i) = r.zone {
        field(&mut s, " zone", &ZONE.rows()[i as usize].value.replace(' ', "\u{a0}"));
    }
    s
}

/// hours: `(d d | "0"? d) @ ("0".."23")`: `f(end, value)`.
fn hours(text: &[char], start: usize, f: &mut dyn FnMut(usize, u8)) {
    let d = |i: usize| text.get(i).filter(|c| is_digit(**c)).map(|c| *c as u8 - b'0');
    let Some(a) = d(start) else { return };
    f(start + 1, a);
    if let Some(b) = d(start + 1) {
        if a == 0 {
            f(start + 2, b);
        } else if a * 10 + b <= 23 {
            f(start + 2, a * 10 + b);
        }
    }
}

/// Whether [`minutes`] accepts the two chars at `start`.
fn minutes_ok(text: &[char], start: usize) -> bool {
    match (text.get(start), text.get(start + 1)) {
        (Some(&a), Some(&b)) => is_digit(a) && is_digit(b) && (a == '0' && b != '0' || ('1'..='5').contains(&a)),
        _ => false,
    }
}

/// minutes/seconds: `0d` (d≠0) → "o <d>" or 10..59.
fn minutes(text: &[char], start: usize) -> Option<String> {
    let a = *text.get(start)?;
    let b = *text.get(start + 1)?;
    if !is_digit(a) || !is_digit(b) {
        return None;
    }
    if a == '0' {
        if b == '0' {
            return None;
        }
        return Some(format!("o {}", graph(&b.to_string())?));
    }
    if a <= '5' { graph(&format!("{a}{b}")) } else { None }
}

/// `final_time_zone_optional`, then emit.
fn with_zone(text: &[char], pos: usize, r: TimeR, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, TimeR)) {
    if keep(pos) {
        f(pos, r);
    }
    let p = ws_end(text, pos);
    for (row, e) in ZONE.prefix_matches(text, p) {
        if keep(e) {
            f(e, TimeR { zone: Some(row_index(&ZONE, row)), ..r });
        }
    }
}

fn with_suffix(
    text: &[char],
    pos: usize,
    r: TimeR,
    optional: bool,
    keep: &dyn Fn(usize) -> bool,
    f: &mut dyn FnMut(usize, TimeR),
) {
    if optional {
        with_zone(text, pos, r, keep, f);
    }
    let p = ws_end(text, pos);
    for (row, e) in SUFFIX.prefix_matches(text, p) {
        with_zone(text, e, TimeR { suffix: Some(row_index(&SUFFIX, row)), ..r }, keep, f);
    }
}

/// Every `(end, reading)` of `TimeFst` (fields by [`fields`]).
pub fn time_spans(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, TimeR)) {
    // every branch: 1-2 digit hour, then ":"/"." or (spaces and) a suffix
    let mut d = start;
    while d < text.len() && d - start < 2 && is_digit(text[d]) {
        d += 1;
    }
    if d == start {
        return;
    }
    let shape_ok = |he: usize| {
        matches!(text.get(he), Some(':' | '.')) || SUFFIX.prefix_matches(text, ws_end(text, he)).next().is_some()
    };
    if !(start + 1..=d).any(shape_ok) {
        return;
    }
    let mut hs = [(0usize, 0u8); 2];
    let mut nh = 0;
    hours(text, start, &mut |e, n| {
        hs[nh] = (e, n);
        nh += 1;
    });
    for &(he, hour) in &hs[..nh] {
        let h = TimeR { hour, he: he as u32, min: 0, sec: 0, suffix: None, zone: None };
        // graph_h
        with_suffix(text, he, h, false, keep, f);
        for (sep, hm2) in [(':', false), ('.', true)] {
            if text.get(he) != Some(&sep) {
                continue;
            }
            let m0 = he + 1;
            let zeros = match_str(text, m0, "00").is_some();
            // "00" (no minutes field) | minutes; `minutes` rejects "00", so at most one applies
            if zeros || minutes_ok(text, m0) {
                let body = if zeros { h } else { TimeR { min: 2, ..h } };
                with_suffix(text, m0 + 2, body, !hm2, keep, f);
            }
            if sep == ':' && (zeros || minutes_ok(text, m0)) && text.get(m0 + 2) == Some(&':') {
                // graph_hms
                let body = TimeR { min: if zeros { 1 } else { 2 }, ..h };
                let s0 = m0 + 3;
                if match_str(text, s0, "00").is_some() {
                    with_suffix(text, s0 + 2, TimeR { sec: 1, ..body }, true, keep, f);
                } else if minutes_ok(text, s0) {
                    with_suffix(text, s0 + 2, TimeR { sec: 2, ..body }, true, keep, f);
                }
            }
        }
    }
}

pub struct Time;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 7;

fn tag(text: &[char], r: TimeR) -> String {
    format!("time {{ {} }}", fields(text, r))
}

impl Classifier for Time {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        time_spans(text, start, &|e| crate::lang::en::lazy::keep_end(text, e), &mut |e, r| {
            crate::lang::en::lazy::record(MEMBER, start, e, cost(1.1), 0, Reading::Time(r));
            let tag = if building() { tag(text, r) } else { String::new() };
            out.push(Candidate { end: e, cost: cost(1.1), rank: 0, tag });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Time(r) => Some(tag(text, r)),
            _ => None,
        })
    }
}

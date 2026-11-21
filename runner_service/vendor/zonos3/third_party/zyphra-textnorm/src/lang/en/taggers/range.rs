//! `en/taggers/range.py` (deterministic) with `time_final = time @ vTime`, `date_final = date @ vDate`.

use crate::lang::en::graph_utils::Outs;
use crate::lang::en::lazy::{lz, with_build};
use crate::lang::en::taggers::cardinal::digit_comma_end;
use crate::lang::en::taggers::{date, time};
use crate::lang::en::verbalizers;
use crate::lattice::{Candidate, Classifier};
use crate::tag::{self, Dict, Value};
use crate::text::{Cost, cost, is_digit, match_str};

/// Run a class verbalizer on the tagger's own field text (composition: no permutation).
fn verbalize_fields(class: &str, fields: &str) -> Option<String> {
    let s = format!("{class} {{ {fields} }} ");
    let parsed = tag::parse(&s).ok()?;
    let [d] = parsed.as_slice() else { return None };
    let [(k, Value::Dict(inner))] = d.as_slice() else { return None };
    let inner: &Dict = inner;
    match k.as_ref() {
        "time" => verbalizers::time::time(inner),
        "date" => verbalizers::date::date(inner),
        _ => None,
    }
}

/// `time @ vTime`: the verbalizer decides acceptance, so the time fields are always built.
fn time_final(text: &[char], p: usize) -> Vec<(usize, String)> {
    let mut v = Vec::new();
    with_build(|| {
        time::time_spans(text, p, &|_| true, &mut |e, r| {
            if let Some(o) = verbalize_fields("time", &time::fields(text, r)) {
                v.push((e, o));
            }
        })
    });
    v
}

/// `(DIGIT^n "s"?) @ date_final`.
fn date_years(text: &[char], p: usize, n: usize) -> Vec<(usize, String, Cost)> {
    let mut v = Vec::new();
    if !(0..n).all(|k| text.get(p + k).is_some_and(|&c| is_digit(c))) {
        return v;
    }
    let mut o = Outs::default();
    let s_ok = |e: usize| e == p + n || (e == p + n + 1 && text[p + n] == 's');
    // `date @ vDate`: the verbalizer decides acceptance, so the date fields are always built
    with_build(|| date::date_spans(text, p, &s_ok, &mut o));
    for (e, c, s) in o.into_outputs() {
        if e == p + n || (e == p + n + 1 && text[p + n] == 's') {
            if let Some(w) = verbalize_fields("date", &s) {
                v.push((e, w, c));
            }
        }
    }
    v
}

fn with_and(text: &[char], p: usize) -> Vec<(usize, String, Cost)> {
    let mut v = Vec::new();
    if p >= text.len() || !is_digit(text[p]) {
        return v;
    }
    // followed by "+", "...", "-" or the token end: only the maximal digit run can be on a path
    let end = digit_comma_end(text, p);
    let mut buf = String::new();
    for e in end..=end {
        buf.clear();
        buf.extend(&text[p..e]);
        if let Some((w, c)) = crate::lang::en::taggers::cardinal::graph_with_and_best(&buf) {
            v.push((e, w, c));
        }
    }
    v
}

/// `delete_space + "-"→" to " + delete_space` (each `delete_space` = 0/1 ASCII space): next positions.
fn dash(text: &[char], p: usize) -> impl Iterator<Item = usize> + Clone {
    let q = if text.get(p) == Some(&' ') { p + 1 } else { p };
    // the dash at p (no leading space) or at p + 1 (after one space); the space only moves the dash
    let mut v = [usize::MAX; 2];
    if text.get(q) == Some(&'-') {
        v[0] = q + 1;
        if text.get(q + 1) == Some(&' ') {
            v[1] = q + 2;
        }
    }
    v.into_iter().filter(|&x| x != usize::MAX)
}

pub fn range_spans(text: &[char], start: usize, out: &mut Outs) {
    let c0 = text[start];
    // time/year ranges need a "-" (times may carry spaced suffixes and zones first: look a little further)
    let window = &text[start..text.len().min(start + 48)];
    let has_dash = window.contains(&'-');
    // cardinal ranges need "+" or "..." right after the first number (optionally spaced)
    let e0 = digit_comma_end(text, start);
    let plus_or_dots = ["+", " + ", "...", " ... "].iter().any(|sep| match_str(text, e0, sep).is_some());
    if is_digit(c0) && has_dash {
        // `time_final` and `date_years` verbalize what they accept; both are only used when a dash follows the
        // first part, so that is checked on the unverbalized spans first
        let mut time_dash = false;
        time::time_spans(text, start, &|_| true, &mut |e, _| time_dash |= dash(text, e).next().is_some());
        if time_dash {
            for (e1, t1) in time_final(text, start) {
                for q in dash(text, e1) {
                    for (e2, t2) in time_final(text, q) {
                        out.add(e2, 0, lz!("{t1} to {t2}"));
                    }
                }
            }
        }
        let year_dash = [start + 4, start + 5]
            .iter()
            .any(|&e| (e == start + 4 || text.get(start + 4) == Some(&'s')) && dash(text, e).next().is_some());
        if year_dash {
            for (e1, y1, c1) in date_years(text, start, 4) {
                for q in dash(text, e1) {
                    for (e2, y2, c2) in date_years(text, q, 4).into_iter().chain(date_years(text, q, 2)) {
                        out.add(e2, c1 + c2, lz!("{y1} to {y2}"));
                    }
                    if text.get(q..q + 2).is_some_and(|s| s.iter().all(|&c| is_digit(c))) {
                        let s: String = text[q..q + 2].iter().collect();
                        if let Some((w, c)) = crate::lang::en::taggers::cardinal::graph_with_and_best(&s) {
                            out.add(q + 2, c1 + c, lz!("{y1} to {w}"));
                        }
                    }
                }
            }
        }
    }
    if is_digit(c0) && plus_or_dots {
        for (e1, w1, c1) in with_and(text, start) {
            // + chains
            for (sep, word) in [("+", " plus "), (" + ", " plus ")] {
                let mut frontier = vec![(e1, w1.clone(), c1)];
                while let Some((p, acc, c)) = frontier.pop() {
                    let Some(l) = match_str(text, p, sep) else { continue };
                    for (e2, w2, c2) in with_and(text, p + l) {
                        let s = lz!("{acc}{word}{w2}");
                        out.add(e2, c + c2, s.clone());
                        frontier.push((e2, s, c + c2));
                    }
                }
            }
            for (sep, word) in [("...", " ... "), (" ... ", " ... ")] {
                if let Some(l) = match_str(text, e1, sep) {
                    for (e2, w2, c2) in with_and(text, e1 + l) {
                        out.add(e2, c1 + c2, lz!("{w1}{word}{w2}"));
                    }
                }
            }
        }
    }
    if c0 == '~' {
        for (e, t) in time_final(text, start + 1) {
            out.add(e, 0, lz!("approximately{t}"));
        }
        for (e, w, c) in with_and(text, start + 1) {
            out.add(e, c, lz!("approximately{w}"));
        }
    }
    if match_str(text, start, "mid-").is_some() {
        for (e, y, c) in date_years(text, start + 4, 4).into_iter().chain(date_years(text, start + 4, 2)) {
            out.add(e, c, lz!("mid {y}"));
        }
    }
}

pub struct Range;

impl Classifier for Range {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let c = text[start];
        if !(is_digit(c) || c == '~' || c == 'm') {
            return;
        }
        // every digit-initial branch joins its parts with "-", "+" or "..." within a short window
        if is_digit(c) {
            let w = &text[start..text.len().min(start + 48)];
            if !w.iter().any(|&ch| ch == '-' || ch == '+') && !w.windows(3).any(|x| x == ['.', '.', '.']) {
                return;
            }
        }
        let mut o = Outs::new();
        range_spans(text, start, &mut o);
        for (e, c, s) in o.into_outputs() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            let tag = lz!("name: \"{}\"", s.replace(' ', "\u{a0}"));
            out.push(Candidate { end: e, cost: cost(1.1) + c, rank: 0, tag });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize(self, text, start, candidate)
    }
}

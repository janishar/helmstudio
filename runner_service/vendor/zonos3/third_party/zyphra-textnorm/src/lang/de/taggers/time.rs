//! `de/taggers/time.py`: `TimeFst` (`graph_hm | graph_h | graph_hms`).

use super::cardinal::WEIGHT;
use crate::lang::de::{Sink, Spans};
use crate::text::{match_str, space_to_nbsp};

crate::tsv!(TIME_ZONE, "de/time/time_zone.tsv");

fn num(text: &[char], s: usize, n: usize) -> Option<u32> {
    let d = text.get(s..s + n)?;
    d.iter().try_fold(0u32, |a, c| c.to_digit(10).filter(|_| c.is_ascii_digit()).map(|v| a * 10 + v))
}

/// `final_graph_hour` (leading zero optional-deleted): `(end, hour)`.
fn hours(text: &[char], s: usize) -> [Option<(usize, u32)>; 2] {
    let one = num(text, s, 1).map(|h| (s + 1, h));
    let two = num(text, s, 2).and_then(|h| {
        let v = if text[s] == '0' { h % 10 } else { h };
        (v <= 24).then_some((s + 2, v))
    });
    [one, two]
}

/// `final_graph_minute` / seconds: two digits, `0x` → `x` (1–9), `10`–`59`.
fn minute(text: &[char], s: usize) -> Option<u32> {
    let v = num(text, s, 2)?;
    if text[s] == '0' { (1..=9).contains(&v).then_some(v) } else { (10..=59).contains(&v).then_some(v) }
}

/// `final_suffix`: `" Uhr"` or `"uhr"`.
fn suffix(text: &[char], s: usize) -> Option<usize> {
    match_str(text, s, " Uhr").or_else(|| match_str(text, s, "uhr")).map(|n| s + n)
}

/// The time fields before the zone: hours, then minutes and seconds when present.
#[derive(Clone, Copy)]
struct Body {
    h: u32,
    m: Option<u32>,
    s: Option<u32>,
}

impl Body {
    fn tag(self, zone: Option<&str>) -> String {
        let mut t = format!("time {{ hours: \"{}\"", self.h);
        if let Some(m) = self.m {
            t.push_str(&format!(" minutes: \"{m}\""));
        }
        if let Some(s) = self.s {
            t.push_str(&format!(" seconds: \"{s}\""));
        }
        if let Some(z) = zone {
            t.push_str(&format!(" zone: \"{}\"", space_to_nbsp(z)));
        }
        if self.s.is_some() {
            t.push_str(" preserve_order: true");
        }
        t.push_str(" }");
        t
    }
}

fn zones(text: &[char], s: usize, body: Body, out: &mut Sink) {
    out.push(s, WEIGHT, || body.tag(None));
    if text.get(s) == Some(&' ') {
        for (r, e) in TIME_ZONE.prefix_matches(text, s + 1) {
            if e > s + 1 {
                out.push(e, WEIGHT, || body.tag(Some(&r.value)));
            }
        }
    }
}

pub struct Time;

impl Spans for Time {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit()
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if !text[start].is_ascii_digit() {
            return;
        }
        // graph_h: hour verbatim (0–24, no zero deletion)
        for n in [1, 2] {
            if let Some(h) = num(text, start, n) {
                if h <= 24 && (n == 1 || text[start] != '0') {
                    if let Some(e) = suffix(text, start + n) {
                        zones(text, e, Body { h, m: None, s: None }, out);
                    }
                }
            }
        }
        for (he, h) in hours(text, start).into_iter().flatten() {
            let Some(&sep) = text.get(he) else { continue };
            // graph_hm
            if sep == ':' || sep == '.' {
                let m = he + 1;
                let body = if match_str(text, m, "00").is_some() {
                    Some(Body { h, m: None, s: None })
                } else {
                    minute(text, m).map(|mm| Body { h, m: Some(mm), s: None })
                };
                if let Some(body) = body {
                    if let Some(e) = suffix(text, m + 2) {
                        zones(text, e, body, out);
                    }
                }
            }
            // graph_hms
            if sep == ':' && text.get(he + 3) == Some(&':') {
                let m = he + 1;
                let mm = if match_str(text, m, "00").is_some() { Some(0) } else { minute(text, m) };
                let ss = if match_str(text, m + 3, "00").is_some() { Some(0) } else { minute(text, m + 3) };
                if let (Some(mm), Some(ss)) = (mm, ss) {
                    if let Some(e) = suffix(text, m + 5) {
                        zones(text, e, Body { h, m: Some(mm), s: Some(ss) }, out);
                    }
                }
            }
        }
    }
}

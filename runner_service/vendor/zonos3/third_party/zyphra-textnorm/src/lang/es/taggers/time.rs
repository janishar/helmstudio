//! `es/taggers/time.py` (deterministic).

use super::cardinal;
use crate::lang::es::{Lazy, Sink, Stash, ok_end};
use crate::text::{Cost, cost, is_digit, is_nemo_space, match_str};

crate::tsv!(TIME_ZONES, "es/time/time_zone.tsv");
crate::tsv!(SUFFIX, "es/time/time_suffix.tsv");

/// One parsed time: every field is a reference into a static word table or a span of the input; the tag text is
/// built only for the candidates `Time` returns.
#[derive(Clone, Copy)]
struct T {
    end: usize,
    cost: Cost,
    hour: &'static str,
    minutes: Option<&'static str>,
    seconds: Option<&'static str>,
    suffix: Option<&'static str>,
    zone: Option<Zone>,
}

#[derive(Clone, Copy)]
struct Zone {
    name: &'static str,
    /// `(sign, number words)` of a `utc_or_gmt_diff`: one digit or `hour minutes`.
    diff: Option<(&'static str, &'static str, Option<&'static str>)>,
}

/// 1-2 digits, optional leading zero, value in `lo..=hi`.
fn num(text: &[char], start: usize, lo: u32, hi: u32, mut f: impl FnMut(usize, u32)) {
    let mut v = 0;
    for len in 1..=2 {
        let e = start + len;
        if e > text.len() || !is_digit(text[e - 1]) {
            break;
        }
        v = v * 10 + (text[e - 1] as u32 - '0' as u32);
        if (lo..=hi).contains(&v) {
            f(e, v);
        }
    }
}

fn fem(w: String) -> String {
    // cdrewrite un|ún → una anywhere
    let mut out = String::with_capacity(w.len() + 2);
    let mut i = 0;
    while i < w.len() {
        if w[i..].starts_with("un") {
            out.push_str("una");
            i += 2;
        } else if w[i..].starts_with("ún") {
            out.push_str("una");
            i += "ún".len();
        } else {
            let c = w[i..].chars().next().unwrap();
            out.push(c);
            i += c.len_utf8();
        }
    }
    out
}

/// Words and costs of 0..=59: hours (feminine `un`), minutes/seconds (masculine), utc digits (`un` → `uno`).
struct Words {
    hours: Vec<Option<(String, Cost)>>,
    minsec: Vec<Option<(String, Cost)>>,
    utc: Vec<Option<(String, Cost)>>,
}

static WORDS: std::sync::LazyLock<Words> = std::sync::LazyLock::new(|| Words {
    hours: (0..=24u32).map(|v| cardinal::number(&v.to_string()).map(|(w, c)| (fem(w), c))).collect(),
    minsec: (0..=59u32).map(|v| if v == 0 { None } else { cardinal::number(&v.to_string()) }).collect(),
    utc: (0..=9u32)
        .map(|v| {
            cardinal::number(&v.to_string())
                .map(|(w, c)| (w.replace("ún", "uno").replace("un", "uno").replace("unoo", "uno"), c))
        })
        .collect(),
});

fn hour(text: &[char], start: usize, h12: bool, mut f: impl FnMut(usize, &'static str, Cost)) {
    let (lo, hi) = if h12 { (1, 12) } else { (0, 24) };
    num(text, start, lo, hi, |e, v| {
        if let Some((w, c)) = &WORDS.hours[v as usize] {
            f(e, w, *c);
        }
    });
}

fn minsec(text: &[char], start: usize, mut f: impl FnMut(usize, &'static str, Cost)) {
    num(text, start, 1, 59, |e, v| {
        if let Some((w, c)) = &WORDS.minsec[v as usize] {
            f(e, w, *c);
        }
    });
}

fn skip_ws(text: &[char], mut p: usize) -> usize {
    while p < text.len() && is_nemo_space(text[p]) {
        p += 1;
    }
    p
}

/// `delete_space + X`: every position after an optional whitespace run.
fn ws_positions(text: &[char], p: usize) -> std::ops::RangeInclusive<usize> {
    p..=skip_ws(text, p)
}

fn suffix(text: &[char], p: usize, mut f: impl FnMut(usize, &'static str, Cost)) {
    let t: &'static crate::text::Table = &SUFFIX;
    for s in ws_positions(text, p) {
        for (row, e) in t.prefix_matches(text, s) {
            f(e, row_value(t, row), row.cost);
        }
    }
}

/// The `'static` value of a row returned by `prefix_matches` on a static table.
fn row_value(t: &'static crate::text::Table, row: &crate::text::Row) -> &'static str {
    let rows = t.rows();
    let i =
        (row as *const crate::text::Row as usize - rows.as_ptr() as usize) / std::mem::size_of::<crate::text::Row>();
    rows[i].value.as_str()
}

fn after_lit(text: &[char], p: usize, lits: &[&str], mut f: impl FnMut(usize)) {
    for s in ws_positions(text, p) {
        for l in lits {
            if let Some(n) = match_str(text, s, l) {
                f(s + n);
            }
        }
    }
}

/// `final_time_zone_optional` after `t`: pushes `t` and every zoned extension.
fn zone_opt(text: &[char], t: T, out: &mut Vec<T>) {
    out.push(t);
    let zones: &'static crate::text::Table = &TIME_ZONES;
    for s in ws_positions(text, t.end) {
        for (row, ze) in zones.prefix_matches(text, s) {
            let name = row_value(zones, row);
            let zc = t.cost + row.cost;
            out.push(T { end: ze, cost: zc, zone: Some(Zone { name, diff: None }), ..t });
            // utc_or_gmt_diff
            let mut p = ze;
            if text.get(p) == Some(&' ') {
                p += 1;
            }
            let sign = match text.get(p) {
                Some('+') => "más",
                Some('-') => "menos",
                _ => continue,
            };
            let mut q = p + 1;
            if text.get(q) == Some(&' ') {
                q += 1;
            }
            if q < text.len() && is_digit(text[q]) {
                if let Some((w, wc)) = &WORDS.utc[(text[q] as u32 - '0' as u32) as usize] {
                    out.push(T {
                        end: q + 1,
                        cost: zc + wc,
                        zone: Some(Zone { name, diff: Some((sign, w, None)) }),
                        ..t
                    });
                }
            }
            hour(text, q, false, |he, h, hc| {
                if !delim(text, he) {
                    return;
                }
                minsec(text, he + 1, |me, m, mc| {
                    out.push(T {
                        end: me,
                        cost: zc + hc + mc,
                        zone: Some(Zone { name, diff: Some((sign, h, Some(m))) }),
                        ..t
                    });
                });
            });
        }
    }
}

/// `(delete "00" | insert_space + field)` at `p`: `(end, word or None for "00", cost)`.
fn mm(text: &[char], p: usize, mut f: impl FnMut(usize, Option<&'static str>, Cost)) {
    if match_str(text, p, "00").is_some() {
        f(p + 2, None, 0);
    }
    minsec(text, p, |e, w, c| f(e, Some(w), c));
}

fn delim(text: &[char], p: usize) -> bool {
    matches!(text.get(p), Some('.') | Some(':'))
}

fn tag(t: &T) -> String {
    let mut s = String::with_capacity(96);
    s.push_str("time { hours: \"");
    s.push_str(t.hour);
    s.push('"');
    for (k, v) in [("minutes", t.minutes), ("seconds", t.seconds), ("suffix", t.suffix)] {
        if let Some(v) = v {
            s.push(' ');
            s.push_str(k);
            s.push_str(": \"");
            s.push_str(v);
            s.push('"');
        }
    }
    if let Some(z) = t.zone {
        s.push_str(" zone: \"");
        s.push_str(z.name);
        if let Some((sign, a, b)) = z.diff {
            s.push(' ');
            s.push_str(sign);
            s.push(' ');
            s.push_str(a);
            if let Some(b) = b {
                s.push(' ');
                s.push_str(b);
            }
        }
        s.push('"');
    }
    s.push_str(" preserve_order: true }");
    s
}

pub struct Time;

impl Lazy for Time {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if !is_digit(text[start]) {
            return;
        }
        // every branch has 1-2 hour digits followed by ":", ".", "h", whitespace or a suffix letter (a/A/p/P)
        let mut e = start;
        while e < text.len() && is_digit(text[e]) {
            e += 1;
        }
        let after_one = text.get(start + 1).copied();
        let ok = |c: Option<char>| {
            matches!(c, Some(':' | '.' | 'h' | 'a' | 'A' | 'p' | 'P')) || c.is_some_and(is_nemo_space)
        };
        if e - start > 2 || !(ok(text.get(e).copied()) || (e - start == 2 && ok(after_one))) {
            return;
        }
        let base = |end: usize, hour: &'static str, cost: Cost| T {
            end,
            cost,
            hour,
            minutes: None,
            seconds: None,
            suffix: None,
            zone: None,
        };
        let mut all: Vec<T> = SCRATCH.with(|s| s.take());
        all.clear();
        // graph_hm 1 and 3 (24h / 12h + suffix)
        for h12 in [false, true] {
            hour(text, start, h12, |he, h, hc| {
                if !delim(text, he) {
                    return;
                }
                // `mm` reads "00" and 1-2 digits: at most 3 minute readings, each with at most 3 second readings
                let mut stage: [Option<T>; 12] = [None; 12];
                let mut n = 0;
                mm(text, he + 1, |me, m, mc| {
                    let t = T { minutes: m, ..base(me, h, hc + mc) };
                    stage[n] = Some(t);
                    n += 1;
                    if delim(text, me) {
                        mm(text, me + 1, |se, s, sc| {
                            stage[n] = Some(T { end: se, cost: t.cost + sc, seconds: s, ..t });
                            n += 1;
                        });
                    }
                });
                for t in stage.into_iter().flatten() {
                    if h12 {
                        suffix(text, t.end, |se, s, sc| {
                            zone_opt(text, T { end: se, cost: t.cost + sc, suffix: Some(s), ..t }, &mut all)
                        });
                    } else {
                        zone_opt(text, t, &mut all);
                        after_lit(text, t.end, &["h"], |he2| zone_opt(text, T { end: he2, ..t }, &mut all));
                    }
                }
            });
        }
        // graph_hm 2: hour h [ws] (00 | min) (min|m) [ws (00|sec)(seg|s)]
        hour(text, start, false, |he, h, hc| {
            after_lit(text, he, &["h"], |p| {
                let p = skip_ws(text, p);
                mm(text, p, |me, m, mc| {
                    after_lit(text, me, &["min", "m"], |e| {
                        let t = T { minutes: m, ..base(e, h, hc + mc) };
                        zone_opt(text, t, &mut all);
                        for q in e..=skip_ws(text, e) {
                            mm(text, q, |se, s, sc| {
                                after_lit(text, se, &["seg", "s"], |e2| {
                                    zone_opt(text, T { end: e2, cost: t.cost + sc, seconds: s, ..t }, &mut all);
                                });
                            });
                        }
                    });
                });
            });
        });
        // graph_h
        hour(text, start, false, |he, h, hc| {
            after_lit(text, he, &["h"], |e| zone_opt(text, base(e, h, hc), &mut all));
        });
        hour(text, start, true, |he, h, hc| {
            suffix(text, he, |se, s, sc| zone_opt(text, T { suffix: Some(s), ..base(se, h, hc + sc) }, &mut all));
        });
        let exploring = out.exploring();
        for t in &all {
            if !ok_end(text, t.end) {
                continue;
            }
            let c = cost(1.09) + t.cost;
            if exploring {
                STASH.with(|s| s.put(start, t.end, c, 0, *t));
            }
            out.push(t.end, c, 0, || tag(t));
        }
        SCRATCH.with(|s| s.set(all));
    }

    fn recall(&self, _text: &[char], start: usize, end: usize, cost: Cost, rank: i32) -> Option<String> {
        STASH.with(|s| s.find(start, end, cost, rank)).map(|t| tag(&t))
    }
}

thread_local! {
    /// The readings explored on the current text (tags built only for the best-path ones).
    static STASH: Stash<T> = const { Stash::new() };
    /// Reused reading buffer of [`Time`].
    static SCRATCH: std::cell::Cell<Vec<T>> = const { std::cell::Cell::new(Vec::new()) };
}

/// Forget the previous text's readings.
pub fn reset() {
    STASH.with(Stash::clear);
}

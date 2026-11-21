//! `fr/taggers/date.py`: `DateFst`.
//!
//! The union starts from `pynini.accep("")`: the date FST accepts the empty string (tag ``, cost 1.1). The lattice
//! never takes zero-length candidates; in the oracle that empty token would sit between two other items at an extra
//! 1.1 with an empty `tokens {  }` body, which is never cheaper than the same path without it, so the branch cannot
//! affect any shortest path and is not emitted.
//!
//! `months.tsv` values keep their trailing spaces (`"mai "`, `"août "`, …): numeric months emit them inside the
//! value, and month names must match them literally in the input. The month-name branch writes
//! ` preserve_order: true}` without the space before `}`, as upstream.

use super::cardinal::reading_into;
use crate::lang::fr::lazy::{Lazy, Out, candidate, lazy_classifier, wanted};
use crate::lattice::Candidate;
use crate::text::{Cost, cost, is_digit};

crate::tsv!(MONTHS, "fr/dates/months.tsv");
crate::tsv!(ERAS, "fr/dates/eras.tsv");

pub struct Date;

pub const WEIGHT: Cost = cost(1.1);

/// A day: `optional_le` prefix, then the 1-2 digit number `text[ds..de]` (leading zero deleted).
#[derive(Clone, Copy)]
struct Day {
    le: &'static str,
    /// Digits after leading-zero deletion.
    ds: usize,
    de: usize,
}

impl Day {
    fn write<O: Out>(&self, text: &[char], out: &mut O) {
        out.push_str(self.le);
        // 1–31 always has a cardinal reading. `premier | cardinal_graph` on "1" ties; the oracle picks the cardinal
        // "un" ("1-1-1843" → day "un"), so "premier" never survives and is not emitted.
        reading_into(&text[self.ds..self.de], out);
    }
}

/// `optional_leading_zero @ valid(1..=max)` on `text[s..s+len]` (len 1 or 2): the digits kept.
fn number(text: &[char], s: usize, len: usize, max: u32) -> Option<(usize, usize)> {
    let x = text.get(s..s + len)?;
    let (ds, v) = match *x {
        [a] if is_digit(a) => (s, a as u32 - '0' as u32),
        ['0', b] if is_digit(b) => (s + 1, b as u32 - '0' as u32),
        [a, b] if is_digit(a) && is_digit(b) => (s, (a as u32 - '0' as u32) * 10 + b as u32 - '0' as u32),
        _ => return None,
    };
    (1..=max).contains(&v).then_some((ds, s + len))
}

/// The days accepted at one position (at most 3 `le` options × 2 lengths), on the stack.
#[derive(Clone, Copy)]
struct Days {
    items: [Day; 6],
    len: usize,
}

impl Days {
    fn iter(&self) -> std::slice::Iter<'_, Day> {
        self.items[..self.len].iter()
    }
}

/// `digit_to_day` at `start`.
fn days(text: &[char], start: usize) -> Days {
    let mut d = Days { items: [Day { le: "", ds: 0, de: 0 }; 6], len: 0 };
    for le in ["", "le ", "les "] {
        let Some(n) = crate::text::match_str(text, start, le) else { continue };
        let s = start + n;
        for len in 1..=2 {
            if let Some((ds, de)) = number(text, s, len, 31) {
                d.items[d.len] = Day { le, ds, de };
                d.len += 1;
            }
        }
    }
    d
}

/// `normalize_month_number @ months.tsv` at `start`: (end, month).
fn month_numeric(text: &[char], start: usize) -> impl Iterator<Item = (usize, &'static str)> + '_ {
    (1..=2).filter_map(move |len| {
        let (ds, de) = number(text, start, len, 12)?;
        let v = (ds..de).fold(0usize, |a, i| a * 10 + (text[i] as usize - '0' as usize));
        Some((de, MONTH_BY_NUMBER[v]?))
    })
}

static MONTH_BY_NUMBER: std::sync::LazyLock<[Option<&'static str>; 13]> = std::sync::LazyLock::new(|| {
    let mut a = [None; 13];
    for r in MONTHS.rows() {
        if let Ok(n) = r.key.parse::<usize>() {
            if n <= 12 && a[n].is_none() {
                a[n] = Some(r.value.as_str());
            }
        }
    }
    a
});

/// `month_numbers.project("output")` at `start`.
fn month_names(text: &[char], start: usize) -> impl Iterator<Item = (usize, &'static str)> + '_ {
    let first = text.get(start).copied();
    MONTHS.rows().iter().zip(MONTH_NAMES.iter()).filter_map(move |(r, m)| {
        if first != Some(m[0]) {
            return None;
        }
        Some((start + crate::text::match_str(text, start, &r.value)?, r.value.as_str()))
    })
}

/// `digits_to_year`: (NEMO_DIGIT - "0") DIGIT{1,3} @ cardinal — the ends of `text[start..end]`.
fn years(text: &[char], start: usize) -> impl Iterator<Item = usize> + '_ {
    let ok = text.get(start).is_some_and(|&c| is_digit(c) && c != '0');
    (2..=4).map(move |len| start + len).take_while(move |&e| ok && e <= text.len() && is_digit(text[e - 1]))
}

/// Writes `date { day: "…" month: "…"[ year: "…"]<close>`.
fn date_tag<O: Out>(text: &[char], day: &O, month: &str, year: Option<(usize, usize)>, close: &str) -> O {
    let mut t = O::new();
    t.push_str("date { day: \"");
    t.append(day);
    t.push_str("\" month: \"");
    t.push_str(month);
    t.push('"');
    if let Some((ys, ye)) = year {
        t.push_str(" year: \"");
        reading_into(&text[ys..ye], &mut t);
        t.push('"');
    }
    t.push_str(close);
    t
}

const CLOSE: &str = " preserve_order: true }";
/// The month-name branch's literal (no space before `}`).
const CLOSE_NAME: &str = " preserve_order: true}";

impl Lazy for Date {
    fn generate<O: Out>(&self, text: &[char], start: usize, want: Option<usize>, out: &mut Vec<Candidate>) {
        // every branch starts with a digit (days, eras) or with "le "/"les "
        if !text.get(start).is_some_and(|&c| is_digit(c) || c == 'l') {
            return;
        }
        // ranks count every accepted span in branch order, wanted or not
        let mut rank = 0i32;
        let mut push = |end: usize, tag: &dyn Fn() -> O, out: &mut Vec<Candidate>| {
            if wanted(want, end) {
                out.push(candidate(end, WEIGHT, rank, tag()));
            }
            rank += 1;
        };
        let day_list = days(text, start);
        if day_list.len == 0 && !is_digit(text[start]) {
            return;
        }
        let mut day = O::new();
        // numeric d SEP m (SEP y)?
        for sep in ['/', '.', '-'] {
            for d in day_list.iter() {
                if text.get(d.de) != Some(&sep) {
                    continue;
                }
                for (me, m) in month_numeric(text, d.de + 1) {
                    day.truncate(0);
                    d.write(text, &mut day);
                    push(me, &|| date_tag(text, &day, m, None, CLOSE), out);
                    if text.get(me) == Some(&sep) {
                        for ye in years(text, me + 1) {
                            push(ye, &|| date_tag(text, &day, m, Some((me + 1, ye)), CLOSE), out);
                        }
                    }
                }
            }
        }
        // d " " month_name (" " y)?
        for d in day_list.iter() {
            if text.get(d.de) != Some(&' ') {
                continue;
            }
            for (me, m) in month_names(text, d.de + 1) {
                day.truncate(0);
                d.write(text, &mut day);
                push(me, &|| date_tag(text, &day, m, None, CLOSE_NAME), out);
                if text.get(me) == Some(&' ') {
                    for ye in years(text, me + 1) {
                        push(ye, &|| date_tag(text, &day, m, Some((me + 1, ye)), CLOSE_NAME), out);
                    }
                }
            }
        }
        // eras
        for (row, e) in ERAS.prefix_matches(text, start) {
            if e > start {
                let tag = || {
                    let mut t = O::new();
                    t.push_str("date { year: \"");
                    t.push_str(&row.value);
                    t.push_str("\" preserve_order: true }");
                    t
                };
                push(e, &tag, out);
            }
        }
        // day ranges: (day SEP)+ day " " month_name (" " y)?
        for sep in ['-', '/'] {
            // frontier: (position after "day SEP", the days so far)
            let mut frontier: Vec<(usize, Vec<Day>)> =
                day_list.iter().filter(|d| text.get(d.de) == Some(&sep)).map(|d| (d.de + 1, vec![*d])).collect();
            while !frontier.is_empty() {
                let mut next = Vec::new();
                for (pos, acc) in &frontier {
                    for d in days(text, *pos).iter() {
                        if text.get(d.de) == Some(&sep) {
                            let mut v = acc.clone();
                            v.push(*d);
                            next.push((d.de + 1, v));
                        }
                        if text.get(d.de) != Some(&' ') {
                            continue;
                        }
                        for (me, m) in month_names(text, d.de + 1) {
                            day.truncate(0);
                            for a in acc {
                                a.write(text, &mut day);
                                day.push(' ');
                            }
                            d.write(text, &mut day);
                            push(me, &|| date_tag(text, &day, m, None, CLOSE), out);
                            if text.get(me) == Some(&' ') {
                                for ye in years(text, me + 1) {
                                    push(ye, &|| date_tag(text, &day, m, Some((me + 1, ye)), CLOSE), out);
                                }
                            }
                        }
                    }
                }
                frontier = next;
            }
        }
    }
}

lazy_classifier!(Date);

/// `months.tsv` values without their trailing space, as chars (none is empty).
static MONTH_NAMES: std::sync::LazyLock<Vec<Vec<char>>> =
    std::sync::LazyLock::new(|| MONTHS.rows().iter().map(|r| r.value.trim_end().chars().collect()).collect());

/// Whether `s` is a month name (`months.tsv` value without its trailing space, as an in-run item end).
pub fn is_month_name(s: &[char]) -> bool {
    MONTH_NAMES.iter().any(|m| m[..] == *s)
}

/// Whether `s` ends with a month name.
pub fn ends_with_month_name(s: &[char]) -> bool {
    let Some(&last) = s.last() else { return false };
    MONTH_NAMES.iter().any(|m| m[m.len() - 1] == last && s.ends_with(m))
}

/// Whether a month name starts at `i`.
pub fn month_name_at(text: &[char], i: usize) -> bool {
    let Some(&first) = text.get(i) else { return false };
    MONTH_NAMES.iter().any(|m| m[0] == first && text[i..].starts_with(m))
}

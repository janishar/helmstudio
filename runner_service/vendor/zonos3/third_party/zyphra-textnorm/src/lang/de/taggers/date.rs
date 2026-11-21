//! `de/taggers/date.py`: `DateFst`.
//!
//! Per span only the cheapest output is kept: a month name beats `digit_month` (+0.0001, a different output that can
//! never be cheapest), the capitalized month path (`TO_LOWER`, 0) beats the lowercase abbreviation (+0.0001).

use std::sync::LazyLock;

use super::cardinal::{WEIGHT, graph, graph_cost, h3, two_digit, two_digit_cost};
use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, Table, cost};

crate::labels!(ABBR_ROWS, "de/months/abbr_to_name.tsv");
crate::tsv!(NUMBER_TO_MONTH, "de/months/numbers.tsv");

static ABBR: LazyLock<Table> = LazyLock::new(|| {
    Table::from_pairs(ABBR_ROWS.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone())))
});
/// `month_graph`: the full names (second column), identity.
static NAMES: LazyLock<Table> = LazyLock::new(|| {
    Table::from_pairs(ABBR_ROWS.iter().filter(|r| r.len() >= 2).map(|r| (r[1].clone(), r[1].clone())))
});

/// Whether `m` is a full month name (second column of `abbr_to_name.tsv`).
pub fn is_month_name(m: &str) -> bool {
    NAMES.contains_key(m)
}

const ABBR_W: Cost = cost(0.0001);
const DOT_W: Cost = cost(-0.0001);
const YEAR_W: Cost = cost(0.001);

/// `optional_leading_zero @ union(1..=hi)`: `(end, value)` spans (the number without a leading zero).
fn small(text: &[char], s: usize, hi: u32) -> [Option<(usize, u32)>; 2] {
    let d = |i: usize| text.get(i).filter(|c| c.is_ascii_digit()).map(|&c| c as u32 - '0' as u32);
    let Some(a) = d(s) else { return [None, None] };
    let one = (1..=hi).contains(&a).then_some((s + 1, a));
    let two =
        d(s + 1).map(|b| if a == 0 { b } else { a * 10 + b }).filter(|v| (1..=hi).contains(v)).map(|v| (s + 2, v));
    [one, two]
}

/// The day words of a value 1–31.
fn words(n: u32) -> String {
    let mut buf = ['0'; 2];
    let d: &[char] = if n < 10 {
        buf[0] = char::from(b'0' + n as u8);
        &buf[..1]
    } else {
        buf = [char::from(b'0' + (n / 10) as u8), char::from(b'0' + (n % 10) as u8)];
        &buf
    };
    h3(d).map(|x| x.0).unwrap_or_default()
}

/// `month_number`: the month name of a value 1–12.
fn month_word(v: u32) -> &'static str {
    static NAMES: LazyLock<Vec<String>> = LazyLock::new(|| {
        (1..=12).map(|v| NUMBER_TO_MONTH.exact(&v.to_string()).next().expect("month").value.clone()).collect()
    });
    &NAMES[v as usize - 1]
}

/// `day`: 1–31 with an optional leading zero.
fn day(text: &[char], s: usize) -> impl Iterator<Item = (usize, u32)> {
    small(text, s, 31).into_iter().flatten()
}

/// Day spans followed by `.` (every date branch starting with a day needs it).
fn dotted_day(text: &[char], s: usize) -> impl Iterator<Item = (usize, u32)> + '_ {
    day(text, s).filter(|(e, _)| text.get(*e) == Some(&'.'))
}

fn month_number(text: &[char], s: usize) -> impl Iterator<Item = (usize, u32)> {
    small(text, s, 12).into_iter().flatten()
}

/// `month_name` spans: `(end, name, cost)`, cheapest per end.
fn month_name(text: &[char], s: usize) -> Vec<(usize, &'static str, Cost)> {
    let mut out: Vec<(usize, &'static str, Cost)> = Vec::new();
    // the table rows borrow from the (possibly local) matched text: map each value to the static name list
    let mut add = |e: usize, v: &str, c: Cost| {
        let v: &'static str = NAMES.rows().iter().find(|r| r.value == v).expect("month name").value.as_str();
        match out.iter_mut().find(|x| x.0 == e) {
            Some(x) if x.2 <= c => {}
            Some(x) => *x = (e, v, c),
            None => out.push((e, v, c)),
        }
    };
    // every key starts with a letter
    let first = text[s];
    if !first.is_alphabetic() {
        return out;
    }
    // TO_LOWER on the first char (ASCII upper only), the rest verbatim
    let mut buf = ['\0'; 12];
    let lowered: Option<&[char]> = first.is_ascii_uppercase().then(|| {
        let tail = &text[s + 1..text.len().min(s + 12)];
        buf[0] = first.to_ascii_lowercase();
        buf[1..=tail.len()].copy_from_slice(tail);
        &buf[..=tail.len()]
    });
    let abbr = |t: &[char], at: usize, offset: usize, base: Cost, add: &mut dyn FnMut(usize, &str, Cost)| {
        for (r, e) in ABBR.prefix_matches(t, at) {
            if e > at {
                let end = e - at + offset;
                add(end, &r.value, base);
                if text.get(end) == Some(&'.') {
                    add(end + 1, &r.value, base + DOT_W);
                }
            }
        }
    };
    for (r, e) in NAMES.prefix_matches(text, s) {
        if e > s {
            add(e, &r.value, 0);
        }
    }
    abbr(text, s, s, ABBR_W, &mut add);
    if let Some(l) = lowered {
        for (r, e) in NAMES.prefix_matches(l, 0) {
            if e > 0 {
                add(s + e, &r.value, 0);
            }
        }
        abbr(l, 0, s, 0, &mut add);
    }
    out
}

/// `self.year` (`get_year_graph` + 0.001) on a 4-digit span at `s`: the cheapest reading's cost, with a writer.
pub fn year(text: &[char], s: usize) -> Option<Cost> {
    year_reading(text, s).map(|(_, c)| c)
}

/// The reading kinds of [`year`]: the cardinal, two two-digit halves, or `xx hundert`.
#[derive(Clone, Copy)]
enum YearKind {
    Cardinal,
    Halves,
    Hundred,
}

fn year_reading(text: &[char], s: usize) -> Option<(YearKind, Cost)> {
    let d = text.get(s..s + 4)?;
    if !d.iter().all(char::is_ascii_digit) {
        return None;
    }
    let two = |x: &[char]| -> Option<Cost> {
        // delete_leading_zero @ two_digit_non_zero
        if x[0] == '0' { two_digit_cost(&x[1..]) } else { two_digit_cost(x) }
    };
    let mut best: Option<(YearKind, Cost)> = None;
    let mut consider = |k: YearKind, c: Cost| {
        if best.is_none_or(|b| c < b.1) {
            best = Some((k, c));
        }
    };
    // year_gt_2000 first: among equal costs the oracle reads 20xx as a cardinal (`2016` → zwei tausend sechzehn)
    if d[0] == '2' && (d[1] == '0' || d[1] == '1') {
        if let Some(c) = graph_cost(d) {
            consider(YearKind::Cardinal, c);
        }
    }
    if d[0] == '1' || (d[0] == '2' && d[1] == '0') {
        if let (Some(ca), Some(cb)) = (two(&d[..2]), two(&d[2..])) {
            consider(YearKind::Halves, ca + cb);
        }
        if d[0] == '1' && d[2] == '0' && d[3] == '0' {
            if let Some(ca) = two(&d[..2]) {
                consider(YearKind::Hundred, ca);
            }
        }
    }
    best.map(|(k, c)| (k, c + YEAR_W))
}

/// The words of [`year`] at `s`.
fn year_words(text: &[char], s: usize) -> String {
    let d = &text[s..s + 4];
    let two = |x: &[char]| -> String {
        let x = if x[0] == '0' { &x[1..] } else { x };
        two_digit(x).expect("year half").0
    };
    match year_reading(text, s).expect("year").0 {
        YearKind::Cardinal => graph(d).expect("year").0,
        YearKind::Halves => format!("{} {}", two(&d[..2]), two(&d[2..])),
        YearKind::Hundred => format!("{} hundert", two(&d[..2])),
    }
}

pub struct Date;

impl Spans for Date {
    fn may_start(&self, c: char) -> bool {
        c.is_ascii_digit()
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if !text[start].is_ascii_digit() {
            return;
        }
        let y = |at: usize| {
            if text.get(at) == Some(&' ') || text.get(at) == Some(&'.') { year(text, at + 1) } else { None }
        };
        for (de, dv) in dotted_day(text, start) {
            // day "." " "? month_name (" " year)?
            for ms in [de + 1, de + 2] {
                if ms == de + 2 && text.get(de + 1) != Some(&' ') {
                    continue;
                }
                if ms >= text.len() {
                    continue;
                }
                for (me, mw, mc) in month_name(text, ms) {
                    out.push(me, WEIGHT + mc, || {
                        format!("date {{ day: \"{}\" month: \"{mw}\" preserve_order: true }}", words(dv))
                    });
                    if text.get(me) == Some(&' ') {
                        if let Some(yc) = y(me) {
                            out.push(me + 5, WEIGHT + mc + yc, || {
                                format!(
                                    "date {{ day: \"{}\" month: \"{mw}\" year: \"{}\" preserve_order: true }}",
                                    words(dv),
                                    year_words(text, me + 1)
                                )
                            });
                        }
                    }
                }
            }
            // day "." month_number ("." year)?
            for (me, mv) in month_number(text, de + 1) {
                let mw = month_word(mv);
                out.push(me, WEIGHT, || {
                    format!("date {{ day: \"{}\" month: \"{mw}\" preserve_order: true }}", words(dv))
                });
                if text.get(me) == Some(&'.') {
                    if let Some(yc) = y(me) {
                        out.push(me + 5, WEIGHT + yc, || {
                            format!(
                                "date {{ day: \"{}\" month: \"{mw}\" year: \"{}\" preserve_order: true }}",
                                words(dv),
                                year_words(text, me + 1)
                            )
                        });
                    }
                }
            }
        }
        if let Some(yc) = year(text, start) {
            out.push(start + 4, WEIGHT + yc, || format!("date {{ year: \"{}\" }}", year_words(text, start)));
            if text.get(start + 4) == Some(&'-') {
                for (me, mv) in month_number(text, start + 5) {
                    let mw = month_word(mv);
                    out.push(me, WEIGHT + yc, || {
                        format!("date {{ year: \"{}\" month: \"{mw}\" }}", year_words(text, start))
                    });
                    if text.get(me) == Some(&'-') {
                        for (de, dv) in day(text, me + 1) {
                            out.push(de, WEIGHT + yc, || {
                                format!(
                                    "date {{ year: \"{}\" month: \"{mw}\" day: \"{}\" }}",
                                    year_words(text, start),
                                    words(dv)
                                )
                            });
                        }
                    }
                }
            }
        }
    }
}

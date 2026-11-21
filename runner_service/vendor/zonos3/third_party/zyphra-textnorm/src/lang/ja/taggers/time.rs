//! `ja/taggers/time.py`.
//!
//! Individual: `[suffix " "] (h | m | s | h " " m | h " " m " " s | m " " s)` with
//! `h = hours: "N(時|時間|時頃)"`, `m = minutes: "N分(過ぎ|頃)?" | minutes: "半(過ぎ|頃)?"`, `s = seconds: "N秒"`,
//! `N = cardinal | cardinal ("." → 点) cardinal`.
//! Clock: `0?hour.tsv` 時 `:` `0?minute.tsv` 分 [`:` `0?second.tsv` 秒].
//!
//! Components are kept as spans and spelled only when a candidate is emitted.

use super::Sink;
use super::cardinal::{digit_run, run_end, small_number, spell_into};
use crate::text::{Cost, Table, starts_with};

crate::tsv!(HOUR, "ja/time/hour.tsv");
crate::tsv!(MINUTE, "ja/time/minute.tsv");
crate::tsv!(SECOND, "ja/time/second.tsv");
crate::tsv!(DIVISION, "ja/time/division.tsv");

#[derive(Clone, Copy)]
enum V<'a> {
    /// `cardinal` of `t[a..b]`, optionally `点` + cardinal of `t[c..d]`.
    Num(usize, usize, Option<(usize, usize)>),
    Str(&'a str),
}

#[derive(Clone, Copy)]
struct Comp<'a> {
    end: usize,
    key: &'static str,
    v: V<'a>,
    /// Accepted unit text after the value (`時`, `分過ぎ`, …), split in two literal pieces.
    tail: (&'static str, &'static str),
}

/// `N` at `p`: `(end, value)`.
/// Every caller needs a non-digit unit right after `N`, so only the digit-run ends are reported.
fn number(t: &[char], p: usize, mut f: impl FnMut(usize, V<'static>)) {
    let Some(e) = run_end(t, p) else { return };
    f(e, V::Num(p, e, None));
    if t.get(e) == Some(&'.') {
        if let Some(e2) = run_end(t, e + 1) {
            f(e2, V::Num(p, e, Some((e + 1, e2))));
        }
    }
}

fn hours(t: &[char], p: usize, mut f: impl FnMut(Comp<'static>)) {
    number(t, p, |e, v| {
        if t.get(e) != Some(&'時') {
            return;
        }
        f(Comp { end: e + 1, key: "hours", v, tail: ("時", "") });
        match t.get(e + 1) {
            Some('間') => f(Comp { end: e + 2, key: "hours", v, tail: ("時間", "") }),
            Some('頃') => f(Comp { end: e + 2, key: "hours", v, tail: ("時頃", "") }),
            _ => {}
        }
    });
}

fn minutes(t: &[char], p: usize, mut f: impl FnMut(Comp<'static>)) {
    let mut with_tail = |e: usize, v: V<'static>, unit: &'static str| {
        f(Comp { end: e, key: "minutes", v, tail: (unit, "") });
        if starts_with(t, e, "過ぎ") {
            f(Comp { end: e + 2, key: "minutes", v, tail: (unit, "過ぎ") });
        } else if t.get(e) == Some(&'頃') {
            f(Comp { end: e + 1, key: "minutes", v, tail: (unit, "頃") });
        }
    };
    number(t, p, |e, v| {
        if t.get(e) == Some(&'分') {
            with_tail(e + 1, v, "分");
        }
    });
    if t.get(p) == Some(&'半') {
        with_tail(p + 1, V::Str(""), "半");
    }
}

fn seconds(t: &[char], p: usize, mut f: impl FnMut(Comp<'static>)) {
    number(t, p, |e, v| {
        if t.get(e) == Some(&'秒') {
            f(Comp { end: e + 1, key: "seconds", v, tail: ("秒", "") });
        }
    });
}

/// `pynutil.delete("0").ques + table`, with the unit inserted.
fn clock<'a>(
    table: &'a Table,
    t: &'a [char],
    p: usize,
    key: &'static str,
    unit: &'static str,
    mut f: impl FnMut(Comp<'a>),
) {
    for q in [p, p + 1] {
        if q == p + 1 && t.get(p) != Some(&'0') {
            break;
        }
        small_number(table, t, q, |end, value| f(Comp { end, key, v: V::Str(value), tail: (unit, "") }));
    }
}

/// Whether a time unit (`時`, `分`, `秒`) or the clock `:` follows the digit run at `p`, directly or after `.` and a
/// second digit run (`N = cardinal ("." → 点) cardinal`; a span ending inside a run is followed by a digit).
fn unit_follows(t: &[char], p: usize) -> bool {
    let unit = |q: usize| matches!(t.get(q), Some('時' | '分' | '秒'));
    let r = digit_run(t, p);
    unit(r) || t.get(r) == Some(&':') || (t.get(r) == Some(&'.') && unit(digit_run(t, r + 1)))
}

/// Whether any branch can start with `c`: a digit, `半` or a division word.
pub fn can_start(c: char) -> bool {
    c.is_ascii_digit() || c == '半' || DIVISION_FIRST.binary_search(&c).is_ok()
}

static DIVISION_FIRST: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let mut v: Vec<char> = DIVISION.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

pub struct Time {
    pub cost: Cost,
}

impl Time {
    /// Push `time { [suffix] <components joined by " "> }`. A span and tag reached twice is a duplicate of equal cost
    /// and rank, so the lattice keeps the first.
    fn push<S: Sink>(&self, t: &[char], out: &mut S, suffix: Option<&str>, comps: &[&Comp]) {
        let end = comps.last().map_or(0, |c| c.end);
        out.push(end, self.cost, 0, |tag| {
            tag.push_str("time { ");
            if let Some(s) = suffix {
                tag.push_str("suffix: \"");
                tag.push_str(s);
                tag.push_str("\" ");
            }
            for (k, c) in comps.iter().enumerate() {
                if k > 0 {
                    tag.push(' ');
                }
                tag.push_str(c.key);
                tag.push_str(": \"");
                match c.v {
                    V::Num(a, b, frac) => {
                        spell_into(&t[a..b], tag);
                        if let Some((c, d)) = frac {
                            tag.push('点');
                            spell_into(&t[c..d], tag);
                        }
                    }
                    V::Str(s) => tag.push_str(s),
                }
                tag.push_str(c.tail.0);
                tag.push_str(c.tail.1);
                tag.push('"');
            }
            tag.push_str(" }");
        });
    }

    fn individual<S: Sink>(&self, t: &[char], p: usize, suffix: Option<&str>, out: &mut S) {
        hours(t, p, |h| {
            self.push(t, out, suffix, &[&h]);
            minutes(t, h.end, |m| {
                self.push(t, out, suffix, &[&h, &m]);
                seconds(t, m.end, |s| self.push(t, out, suffix, &[&h, &m, &s]));
            });
        });
        minutes(t, p, |m| {
            self.push(t, out, suffix, &[&m]);
            seconds(t, m.end, |s| self.push(t, out, suffix, &[&m, &s]));
        });
        seconds(t, p, |s| self.push(t, out, suffix, &[&s]));
    }

    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        if !can_start(t[start]) {
            return;
        }
        if t[start].is_ascii_digit() && !unit_follows(t, start) {
            // every branch starting with a digit needs 時/分/秒/`:` after the digit run, or `.` + digits + unit
            return;
        }
        self.individual(t, start, None, out);
        if !t[start].is_ascii_digit() {
            for (row, end) in DIVISION.prefix_matches(t, start) {
                if end > start {
                    self.individual(t, end, Some(&row.value), out);
                }
            }
            return;
        }
        clock(&HOUR, t, start, "hours", "時", |h| {
            if t.get(h.end) != Some(&':') {
                return;
            }
            clock(&MINUTE, t, h.end + 1, "minutes", "分", |m| {
                self.push(t, out, None, &[&h, &m]);
                if t.get(m.end) == Some(&':') {
                    clock(&SECOND, t, m.end + 1, "seconds", "秒", |s| self.push(t, out, None, &[&h, &m, &s]));
                }
            });
        });
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn clock_keys_are_one_to_n() {
        for (table, n) in [(&*super::HOUR, 24), (&*super::MINUTE, 60), (&*super::SECOND, 60)] {
            let keys: Vec<String> = table.rows().iter().map(|r| r.key.clone()).collect();
            assert_eq!(keys, (1..=n).map(|i: u32| i.to_string()).collect::<Vec<_>>());
        }
    }
}

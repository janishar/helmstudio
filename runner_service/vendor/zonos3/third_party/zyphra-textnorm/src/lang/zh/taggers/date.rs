//! `zh/taggers/date.py`: `graph` (−2.0) = only year/month/day | year-month, month-day, year-month-day (`年月日`) |
//! `Y/M/D` with `/ - . ·` | era forms; and the range `[range: "从"] graph range: "<到|至|- ~ —— —|>" graph` at 0.
//! `data/date/suffixes.tsv` is the file read (`suffix.tsv`, with CRLF lines, is unused).
//!
//! Pruned (never on a cheapest path): a range without `从` whose goal is empty, `-`, `——` or `—` costs 1.1, while the
//! same span as `date` + punctuation + `date` costs at most −1.8 + 1.0.

use std::sync::LazyLock;

use super::{Gen, Recall, Sink, digit_end, digit_names, field, prefix_static};
use crate::text::{cost, match_str};

crate::tsv!(MONTHS, "zh/date/months.tsv");
crate::tsv!(DAY, "zh/date/day.tsv");
crate::tsv!(SUFFIXES, "zh/date/suffixes.tsv");

const ERA_PREFIX: &[&str] = &["公元", "西元", "公元前", "西元前", "纪元", "纪元前"];
const SIGN: &[char] = &['/', '-', '.', '·'];

/// One `graph` reading; strings are built only for emitted candidates.
#[derive(Clone, Copy, Default)]
pub struct G<'a> {
    end: usize,
    era: Option<&'a str>,
    era_first: bool,
    /// Digit span of the year.
    year: Option<(usize, usize)>,
    month: Option<&'a str>,
    day: Option<&'a str>,
}

impl<'a> G<'a> {
    fn new(end: usize) -> G<'a> {
        G { end, era: None, era_first: false, year: None, month: None, day: None }
    }

    fn write(&self, t: &[char], out: &mut String) {
        let start = out.len();
        let put = |out: &mut String, key: &str, v: &str| {
            if out.len() > start {
                out.push(' ');
            }
            field(out, key, v);
        };
        if self.era_first
            && let Some(e) = self.era
        {
            put(out, "era", e);
        }
        if let Some((a, b)) = self.year {
            if out.len() > start {
                out.push(' ');
            }
            out.push_str("year: \"");
            digit_names(&t[a..b], out);
            out.push('"');
        }
        if let Some(m) = self.month {
            put(out, "month", m);
        }
        if let Some(d) = self.day {
            put(out, "day", d);
        }
        if !self.era_first
            && let Some(e) = self.era
        {
            put(out, "era", e);
        }
    }
}

/// Whether an era suffix key can start at `p` (they start with a/A/b/B/c/C; unit test `suffix_first_chars`).
fn suffix_first(t: &[char], p: usize) -> bool {
    matches!(t.get(p), Some('a' | 'A' | 'b' | 'B' | 'c' | 'C'))
}

/// `only_year` at `p`: (end, digit span).
fn only_year(t: &[char], p: usize) -> Option<(usize, (usize, usize))> {
    let r = digit_end(t, p);
    (r - p >= 2 && t.get(r) == Some(&'年')).then_some((r + 1, (p, r)))
}

/// A 1-2 digit key table as arrays: the first value of the key `"d"` at `one[d]`, of `"ab"` at `two[10a + b]`.
struct Index {
    one: [Option<&'static str>; 10],
    two: [Option<&'static str>; 100],
}

fn index(table: &'static crate::text::Table) -> Index {
    let mut idx = Index { one: [None; 10], two: [None; 100] };
    for row in table.rows() {
        let d: Vec<usize> = row.key.chars().map(|c| c.to_digit(10).expect("digit key") as usize).collect();
        let slot = match *d.as_slice() {
            [a] => &mut idx.one[a],
            [a, b] => &mut idx.two[10 * a + b],
            _ => panic!("date key longer than two digits"),
        };
        slot.get_or_insert(row.value.as_str());
    }
    idx
}

static MONTH_INDEX: LazyLock<Index> = LazyLock::new(|| index(&MONTHS));
static DAY_INDEX: LazyLock<Index> = LazyLock::new(|| index(&DAY));

fn lookup(idx: &Index, key: &[char]) -> Option<&'static str> {
    let d = |c: char| c as usize - '0' as usize;
    match *key {
        [a] => idx.one[d(a)],
        [a, b] => idx.two[10 * d(a) + d(b)],
        _ => None,
    }
}

/// A 1-2 digit table value spanning the digit run at `p`, followed by one of `after`.
fn unit(t: &[char], p: usize, idx: &Index, after: &[char]) -> Option<(usize, &'static str)> {
    let r = digit_end(t, p);
    if r == p || r - p > 2 || !t.get(r).is_some_and(|c| after.contains(c)) {
        return None;
    }
    lookup(idx, &t[p..r]).map(|v| (r + 1, v))
}

fn only_month(t: &[char], p: usize) -> Option<(usize, &'static str)> {
    unit(t, p, &MONTH_INDEX, &['月'])
}

fn only_day(t: &[char], p: usize) -> Option<(usize, &'static str)> {
    unit(t, p, &DAY_INDEX, &['号', '號', '日'])
}

/// `graph_ymd | year_month | only_year` at `p`: up to three readings (y, ym, ymd).
fn ymd_ym_y(t: &[char], p: usize) -> [Option<G<'static>>; 3] {
    let mut out = [None; 3];
    let Some((ye, ys)) = only_year(t, p) else { return out };
    let mut g = G::new(ye);
    g.year = Some(ys);
    out[0] = Some(g);
    if let Some((me, m)) = only_month(t, ye) {
        g.end = me;
        g.month = Some(m);
        out[1] = Some(g);
        if let Some((de, d)) = only_day(t, me) {
            g.end = de;
            g.day = Some(d);
            out[2] = Some(g);
        }
    }
    out
}

/// `graph` (no range) at `p`, every reading in union order.
fn graph(t: &[char], p: usize, f: &mut impl FnMut(G<'static>)) {
    let Some(&c) = t.get(p) else { return };
    if c.is_ascii_digit() {
        let ymd = ymd_ym_y(t, p);
        for g in ymd.into_iter().flatten() {
            f(g);
        }
        // graph_suffix: (ymd | ym | y) era
        for g in ymd.into_iter().flatten() {
            if !suffix_first(t, g.end) {
                continue;
            }
            for (row, e) in prefix_static(&SUFFIXES, t, g.end) {
                f(G { end: e, era: Some(row.value.as_str()), ..g });
            }
        }
        if let Some((me, m)) = only_month(t, p) {
            let mut g = G::new(me);
            g.month = Some(m);
            f(g);
            if let Some((de, d)) = only_day(t, me) {
                g.end = de;
                g.day = Some(d);
                f(g);
            }
        }
        if let Some((de, d)) = only_day(t, p) {
            let mut g = G::new(de);
            g.day = Some(d);
            f(g);
        }
        let r = digit_end(t, p);
        // graph_sign: 2-4 digits sign month sign day (the day may be a prefix of a longer digit run)
        if (2..=4).contains(&(r - p)) && t.get(r).is_some_and(|c| SIGN.contains(c)) {
            let mr = digit_end(t, r + 1);
            if mr > r + 1
                && mr - r - 1 <= 2
                && t.get(mr).is_some_and(|c| SIGN.contains(c))
                && let Some(month) = lookup(&MONTH_INDEX, &t[r + 1..mr])
            {
                for (drow, de) in prefix_static(&DAY, t, mr + 1) {
                    let mut g = G::new(de);
                    g.year = Some((p, r));
                    g.month = Some(month);
                    g.day = Some(drow.value.as_str());
                    f(g);
                }
            }
        }
        // graph_suffix_year: digits era
        if !suffix_first(t, r) {
            return;
        }
        for (row, e) in prefix_static(&SUFFIXES, t, r) {
            let mut g = G::new(e);
            g.year = Some((p, r));
            g.era = Some(row.value.as_str());
            f(g);
        }
        return;
    }
    // graph_prefix: era (ymd | ym | y)
    if matches!(c, '公' | '西' | '纪') {
        for era in ERA_PREFIX {
            let Some(n) = match_str(t, p, era) else { continue };
            for mut g in ymd_ym_y(t, p + n).into_iter().flatten() {
                g.era = Some(era);
                g.era_first = true;
                f(g);
            }
        }
    }
}

/// Whether a digit-first date can continue its digit run with `f`: 年 月 号 號 日, a date sign or an era suffix
/// (a/A/b/B/c/C).
pub fn digit_follower(f: char) -> bool {
    matches!(f, '年' | '月' | '号' | '號' | '日' | '/' | '-' | '.' | '·' | 'a' | 'A' | 'b' | 'B' | 'c' | 'C')
}

pub struct Date;

/// The range goals in `date.py` order; without `从` only 到 至 ~ are kept (see the module docs).
const GOALS: [(&str, bool); 7] =
    [("", false), ("到", true), ("至", true), ("-", false), ("~", true), ("——", false), ("—", false)];

impl Gen for Date {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        if !(c.is_ascii_digit() || matches!(c, '公' | '西' | '纪' | '从')) {
            return;
        }
        if c.is_ascii_digit() && !t.get(digit_end(t, start)).is_some_and(|&f| digit_follower(f)) {
            return;
        }
        let source = c == '从';
        graph(t, start + usize::from(source), &mut |g1| {
            if !source {
                sink.keep(t, g1.end, cost(-2.0), 0, Recall::Date(Reading::Single(g1)));
            }
            // range: every goal but "" starts with its char
            let next = t.get(g1.end).copied();
            if !source && !matches!(next, Some('到' | '至' | '~')) {
                return;
            }
            for (goal, plain) in GOALS {
                if !(source || plain) || goal.chars().next().is_some_and(|g| Some(g) != next) {
                    continue;
                }
                let Some(n) = match_str(t, g1.end, goal) else { continue };
                graph(t, g1.end + n, &mut |g2| {
                    sink.keep(t, g2.end, 0, 0, Recall::Date(Reading::Range { source, g1, goal, g2 }));
                });
            }
        });
    }
}

/// One `date` token reading.
#[derive(Clone, Copy)]
pub enum Reading {
    Single(G<'static>),
    Range { source: bool, g1: G<'static>, goal: &'static str, g2: G<'static> },
}

impl Reading {
    pub fn write(&self, t: &[char], s: &mut String) {
        s.push_str("date { ");
        match *self {
            Reading::Single(g) => g.write(t, s),
            Reading::Range { source, g1, goal, g2 } => {
                if source {
                    s.push_str("range: \"从\" ");
                }
                g1.write(t, s);
                s.push(' ');
                field(s, "range", goal);
                s.push(' ');
                g2.write(t, s);
            }
        }
        s.push_str(" }");
    }
}

#[cfg(test)]
mod tests {
    /// Era suffix keys start with exactly the chars `suffix_first` and `Date`'s reject let through.
    #[test]
    fn suffix_first_chars() {
        assert_eq!(super::super::first_chars(&super::SUFFIXES), ['A', 'B', 'C', 'a', 'b', 'c']);
    }
}

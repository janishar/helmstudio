//! `es/taggers/date.py`.

use super::cardinal;
use crate::lang::es::graph_utils::{roman_to_int, strip_cardinal_apocope};
use crate::lang::es::{Lazy, Sink, Stash, ok_end};
use crate::text::{Cost, cost, is_digit, is_nemo_space, match_str};

crate::tsv!(MONTHS, "es/dates/months.tsv");
crate::tsv!(MONTHS_ABBR, "es/dates/months_abbr.tsv");
crate::tsv!(DAYS_ABBR, "es/dates/days_abbr.tsv");
crate::tsv!(YEAR_SUFFIX, "es/dates/year_suffix.tsv");

pub const ARTICLES: [&str; 5] = ["de", "del", "el", "del año", "año"];

/// A field value: a static word or an input span (copied out only when the tag is written).
#[derive(Clone, Copy)]
enum W {
    S(&'static str),
    /// A weekday abbreviation's value + " " + the day number word.
    Pre(&'static str, &'static str),
    /// An article + " " + the year words.
    Art(&'static str, &'static str),
}

impl W {
    fn write(&self, s: &mut String) {
        match *self {
            W::S(w) => s.push_str(w),
            W::Pre(a, b) | W::Art(a, b) => {
                s.push_str(a);
                s.push(' ');
                s.push_str(b);
            }
        }
    }
}

/// One date reading: `fields` in tag order (`day`/`month`/`year`), built into text only when returned.
#[derive(Clone, Copy)]
struct D {
    end: usize,
    cost: Cost,
    fields: [Option<(&'static str, W)>; 3],
}

impl D {
    fn push(mut self, end: usize, cost: Cost, key: &'static str, w: W) -> D {
        let slot = self.fields.iter_mut().find(|f| f.is_none()).expect("three fields at most");
        *slot = Some((key, w));
        self.end = end;
        self.cost += cost;
        self
    }

    fn tag(&self, po: bool) -> String {
        let mut s = String::with_capacity(96);
        s.push_str("date {");
        for (k, w) in self.fields.iter().flatten() {
            s.push(' ');
            s.push_str(k);
            s.push_str(": \"");
            w.write(&mut s);
            s.push('"');
        }
        if po {
            s.push_str(" preserve_order: true");
        }
        s.push_str(" }");
        s
    }
}

const EMPTY: D = D { end: 0, cost: 0, fields: [None; 3] };

type Hit = (usize, String, Cost);

/// 1-2 digits with optional leading zero, value in `lo..=hi`.
fn small(text: &[char], start: usize, hi: u32) -> Vec<(usize, u32)> {
    let mut out = Vec::new();
    let mut v = 0;
    for len in 1..=2 {
        let e = start + len;
        if e > text.len() || !is_digit(text[e - 1]) {
            break;
        }
        v = v * 10 + (text[e - 1] as u32 - '0' as u32);
        if v >= 1 && v <= hi {
            out.push((e, v));
        }
    }
    out
}

/// Day/month digit values 1..=31 as cardinal words (index = value).
static SMALL_WORDS: std::sync::LazyLock<Vec<Option<(String, Cost)>>> = std::sync::LazyLock::new(|| {
    (0..=31u32).map(|v| if v == 0 { None } else { cardinal::number(&v.to_string()) }).collect()
});

/// Month names by number (index = value).
static MONTH_BY_NUM: std::sync::LazyLock<Vec<Vec<(&'static str, Cost)>>> = std::sync::LazyLock::new(|| {
    let t: &'static crate::text::Table = &MONTHS;
    (0..=12u32)
        .map(|v| t.rows().iter().filter(|r| r.key == v.to_string()).map(|r| (r.value.as_str(), r.cost)).collect())
        .collect()
});

/// Whether a day ending at `e` can continue into a `dmy` date (" " / " de" then a month, or a separator).
fn day_continues(text: &[char], e: usize) -> bool {
    matches!(text.get(e), Some('.' | '-' | '/' | ' '))
}

/// `day`: an optional weekday abbreviation + " ", then 1-2 digits (1..=31).
fn day(text: &[char], start: usize, mut f: impl FnMut(usize, W, Cost)) {
    let days: &'static crate::text::Table = &DAYS_ABBR;
    let mut starts: [(usize, Option<&'static str>, Cost); 4] = [(start, None, 0); 4];
    let mut n = 1;
    for (row, e) in days.prefix_matches(text, start) {
        if n < starts.len() && text.get(e) == Some(&' ') && text.get(e + 1).is_some_and(|c| is_digit(*c)) {
            starts[n] = (e + 1, Some(static_value(days, row)), row.cost);
            n += 1;
        }
    }
    for &(s, pre, pc) in &starts[..n] {
        if !text.get(s).is_some_and(|c| is_digit(*c)) {
            continue;
        }
        for (e, v) in small(text, s, 31) {
            if let Some((w, c)) = &SMALL_WORDS[v as usize] {
                let w = match pre {
                    Some(p) => W::Pre(p, w),
                    None => W::S(w),
                };
                f(e, w, pc + c);
            }
        }
    }
}

/// The `'static` value of a row returned by `prefix_matches` on a static table.
fn static_value(t: &'static crate::text::Table, row: &crate::text::Row) -> &'static str {
    let rows = t.rows();
    let i =
        (row as *const crate::text::Row as usize - rows.as_ptr() as usize) / std::mem::size_of::<crate::text::Row>();
    rows[i].value.as_str()
}

fn month_name(text: &[char], start: usize, mut f: impl FnMut(usize, W, Cost)) {
    if !text.get(start).is_some_and(|c| c.is_alphabetic()) {
        return;
    }
    let months: &'static crate::text::Table = &MONTHS;
    for r in months.rows() {
        if r.value.chars().next() != Some(text[start]) {
            continue;
        }
        if let Some(n) = match_str(text, start, &r.value) {
            f(start + n, W::S(r.value.as_str()), 0);
        }
    }
    let abbr: &'static crate::text::Table = &MONTHS_ABBR;
    for (row, e) in abbr.prefix_matches(text, start) {
        f(e, W::S(static_value(abbr, row)), row.cost);
    }
}

fn month_number(text: &[char], start: usize, mut f: impl FnMut(usize, W, Cost)) {
    for (e, v) in small(text, start, 12) {
        for &(m, c) in &MONTH_BY_NUM[v as usize] {
            f(e, W::S(m), c);
        }
    }
}

/// `strip_cardinal_apocope(cardinal(v))` for 10..=9999 (index = value).
static YEARS: std::sync::LazyLock<Vec<Option<(String, Cost)>>> = std::sync::LazyLock::new(|| {
    (0..10000u32)
        .map(|v| {
            if v < 10 {
                return None;
            }
            cardinal::number(&v.to_string()).map(|(w, c)| (strip_cardinal_apocope(&w), c))
        })
        .collect()
});

/// `year`: 2-4 digits not starting with 0, cardinal with apocope stripped.
fn year_words(text: &[char], start: usize, mut f: impl FnMut(usize, &'static str, Cost)) {
    if start >= text.len() || !is_digit(text[start]) || text[start] == '0' {
        return;
    }
    for len in 2..=4 {
        let e = start + len;
        if e > text.len() || !is_digit(text[e - 1]) {
            break;
        }
        // a year is the end of its date: a following digit leaves no path
        if text.get(e).is_some_and(|c| is_digit(*c)) {
            continue;
        }
        let v = text[start..e].iter().fold(0usize, |a, c| a * 10 + (*c as usize - '0' as usize));
        if let Some((w, c)) = &YEARS[v] {
            f(e, w, *c);
        }
    }
}

/// `year_with_articles`: an optional article + " ", then the year words.
fn year_with_articles(text: &[char], start: usize, mut f: impl FnMut(usize, W, Cost)) {
    year_words(text, start, |e, w, c| f(e, W::S(w), c));
    for a in ARTICLES {
        if let Some(n) = match_str(text, start, a) {
            if text.get(start + n) == Some(&' ') {
                year_words(text, start + n + 1, |e, w, c| f(e, W::Art(a, w), c));
            }
        }
    }
}

/// `closure(" " + year_with_articles, 0, 1)` after each reading.
fn opt_year(text: &[char], hits: &mut Vec<D>) {
    let n = hits.len();
    for i in 0..n {
        let d = hits[i];
        if text.get(d.end) == Some(&' ') {
            year_with_articles(text, d.end + 1, |e, y, c| hits.push(d.push(e, c, "year", y)));
        }
    }
}

/// Roman centuries `("siglo "|"año ") roman`.
fn roman_centuries(text: &[char], start: usize) -> Vec<Hit> {
    let mut out = Vec::new();
    if !matches!(text.get(start), Some('s' | 'a')) {
        return Vec::new();
    }
    for p in ["siglo ", "año "] {
        let Some(n) = match_str(text, start, p) else {
            continue;
        };
        let s = start + n;
        let mut e = s;
        while e < text.len() && "ivxlcdmIVXLCDM".contains(text[e]) {
            e += 1;
            let r: String = text[s..e].iter().collect();
            for (d, rc) in roman_to_int(&r) {
                if let Some((w, c)) = cardinal::number(&d) {
                    out.push((e, format!("{p}{w}"), rc + c));
                }
            }
        }
    }
    out
}

/// First chars of every alphabetic way a date can start: month names, month and weekday abbreviations,
/// "siglo"/"año" (roman centuries).
static ALPHA_FIRST: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let mut v: Vec<char> = MONTHS
        .rows()
        .iter()
        .map(|r| &r.value)
        .chain(MONTHS_ABBR.rows().iter().map(|r| &r.key))
        .chain(DAYS_ABBR.rows().iter().map(|r| &r.key))
        .filter_map(|s| s.chars().next())
        .chain(['s', 'a'])
        .collect();
    v.sort_unstable();
    v.dedup();
    v
});

pub struct Date;

impl Lazy for Date {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if !is_digit(text[start]) && ALPHA_FIRST.binary_search(&text[start]).is_err() {
            return;
        }
        if is_digit(text[start]) {
            // digit-led branches continue with "." "-" "/" whitespace or a letter (year suffix) after the digits
            let mut e = start;
            while e < text.len() && is_digit(text[e]) {
                e += 1;
            }
            match text.get(e) {
                None => return,
                Some(c) if matches!(c, '.' | '-' | '/') || is_nemo_space(*c) || c.is_alphabetic() => {}
                _ => return,
            }
        }
        let exploring = out.exploring();
        let push = |out: &mut Sink, hits: &[D], po: bool, rank: i32| {
            for d in hits {
                if ok_end(text, d.end) {
                    let c = cost(1.1) + d.cost;
                    if exploring {
                        STASH.with(|s| s.put(start, d.end, c, rank, (*d, po)));
                    }
                    out.push(d.end, c, rank, || d.tag(po));
                }
            }
        };
        // dmy: day (" de")* " " month_name (" " year)? | day sep month_number (sep year)?
        let (mut days, mut hits) = SCRATCH.with(|s| s.take());
        days.clear();
        hits.clear();
        day(text, start, |e, w, c| {
            if day_continues(text, e) {
                days.push(EMPTY.push(e, c, "day", w));
            }
        });
        for &d in &days {
            let mut p = d.end;
            loop {
                if text.get(p) == Some(&' ') {
                    month_name(text, p + 1, |me, m, mc| hits.push(d.push(me, mc, "month", m)));
                }
                match match_str(text, p, " de") {
                    Some(n) => p += n,
                    None => break,
                }
            }
        }
        opt_year(text, &mut hits);
        for sep in ['.', '-', '/'] {
            for &d in &days {
                if text.get(d.end) != Some(&sep) {
                    continue;
                }
                month_number(text, d.end + 1, |me, m, mc| {
                    let dm = d.push(me, mc, "month", m);
                    hits.push(dm);
                    if text.get(me) == Some(&sep) {
                        year_words(text, me + 1, |ye, y, yc| hits.push(dm.push(ye, yc, "year", W::S(y))));
                    }
                });
            }
        }
        push(out, &hits, true, 0);
        // ymd: 4-digit year "-" month_number ("-" day)?
        hits.clear();
        if start + 4 <= text.len()
            && text[start..start + 4].iter().all(|c| is_digit(*c))
            && text.get(start + 4) == Some(&'-')
        {
            let v = text[start..start + 4].iter().fold(0usize, |a, c| a * 10 + (*c as usize - '0' as usize));
            if let Some((y, yc)) = &YEARS[v] {
                let yd = EMPTY.push(start + 4, *yc, "year", W::S(y));
                month_number(text, start + 5, |me, m, mc| {
                    let ym = yd.push(me, mc, "month", m);
                    hits.push(ym);
                    if text.get(me) == Some(&'-') {
                        day(text, me + 1, |de, dw, dc| hits.push(ym.push(de, dc, "day", dw)));
                    }
                });
            }
        }
        push(out, &hits, false, 1);
        // mdy: month_name WS+ day (" " year)?
        hits.clear();
        if text[start].is_alphabetic() {
            month_name(text, start, |me, m, mc| {
                let mut p = me;
                while p < text.len() && is_nemo_space(text[p]) {
                    p += 1;
                }
                if p == me {
                    return;
                }
                let md = EMPTY.push(me, mc, "month", m);
                day(text, p, |de, dw, dc| hits.push(md.push(de, dc, "day", dw)));
            });
            opt_year(text, &mut hits);
            push(out, &hits, false, 2);
        }
        SCRATCH.with(|s| s.set((days, hits)));
        if !text[start].is_alphabetic() {
            // nothing below starts with a digit except the year-suffix branch
            return self.year_suffix(text, start, out);
        }
        // roman centuries
        for (end, y, c) in roman_centuries(text, start) {
            if ok_end(text, end) {
                out.push(end, cost(1.1) + c, 3, || format!("date {{ year: \"{y}\" preserve_order: true }}"));
            }
        }
        self.year_suffix(text, start, out);
    }

    fn recall(&self, _text: &[char], start: usize, end: usize, cost: Cost, rank: i32) -> Option<String> {
        // dmy/ymd/mdy readings are stashed; roman centuries and year suffixes (ranks 3, 4) are re-run
        STASH.with(|s| s.find(start, end, cost, rank)).map(|(d, po)| d.tag(po))
    }
}

thread_local! {
    /// The day/month/year readings explored on the current text with their `preserve_order` flag.
    static STASH: Stash<(D, bool)> = const { Stash::new() };
    /// Reused `(days, readings)` buffers of [`Date`].
    static SCRATCH: std::cell::Cell<(Vec<D>, Vec<D>)> = const { std::cell::Cell::new((Vec::new(), Vec::new())) };
}

/// Forget the previous text's readings.
pub fn reset() {
    STASH.with(Stash::clear);
}

impl Date {
    /// `year_suffix_graph`: (cardinal | roman century) + " "? + suffix.
    fn year_suffix(&self, text: &[char], start: usize, out: &mut Sink) {
        let has_suffix = |e: usize| {
            YEAR_SUFFIX.prefix_matches(text, e).next().is_some()
                || (text.get(e) == Some(&' ') && YEAR_SUFFIX.prefix_matches(text, e + 1).next().is_some())
        };
        let mut ys = Vec::new();
        let mut bases: Vec<(usize, String, Cost)> = roman_centuries(text, start);
        bases.retain(|(e, _, _)| has_suffix(*e));
        for e in cardinal::number_ends(text, start) {
            if !has_suffix(e) {
                continue;
            }
            if let Some((w, c)) = cardinal::graph(&text[start..e]) {
                bases.push((e, w, c));
            }
        }
        for (e, w, c) in bases {
            for s in [e, e + 1] {
                if s == e + 1 && text.get(e) != Some(&' ') {
                    continue;
                }
                for (row, se) in YEAR_SUFFIX.prefix_matches(text, s) {
                    let sep = if s == e { "" } else { " " };
                    ys.push((se, format!("year: \"{w}{sep}{}\"", row.value), c + row.cost));
                }
            }
        }
        for (end, b, c) in ys {
            if ok_end(text, end) {
                out.push(end, cost(1.1) + c, 4, || format!("date {{ {b} }}"));
            }
        }
    }
}

//! `en/taggers/date.py` (deterministic).

use std::sync::LazyLock;

use crate::lang::en::graph_utils::{Outs, row_index};
use crate::lang::en::lazy::{Reading, branch_on, building, lz};
use crate::lang::en::taggers::cardinal::{DIGIT, TY, hundred_component, single_digits};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Row, Table, cost, is_digit, is_nemo_space, match_str};

crate::tsv!(MONTH_NAME, "en/date/month_name.tsv");
crate::tsv!(MONTH_ABBR, "en/date/month_abbr.tsv");
crate::tsv!(MONTH_NUMBER, "en/date/month_number.tsv");
crate::labels!(YEAR_SUFFIX_LABELS, "en/date/year_suffix.tsv");

static YEAR_SUFFIX: LazyLock<Table> = LazyLock::new(|| {
    let mut v: Vec<(String, String)> = YEAR_SUFFIX_LABELS.iter().map(|r| (r[0].clone(), r[1].clone())).collect();
    for r in YEAR_SUFFIX_LABELS.iter() {
        if r[0].ends_with('.') && !r[1].ends_with('.') {
            v.push((r[0].clone(), format!("{}.", r[1])));
        }
    }
    Table::from_pairs(v)
});

const TEEN: [&str; 10] =
    ["ten", "eleven", "twelve", "thirteen", "fourteen", "fifteen", "sixteen", "seventeen", "eighteen", "nineteen"];

fn dg(c: char) -> Option<usize> {
    is_digit(c).then(|| c as usize - '0' as usize)
}

/// `get_ties_graph(True)` on exactly two digits: teen | ty "0"→ty | ty d | "0" d → "oh d".
fn ties2(a: usize, b: usize) -> Option<String> {
    match a {
        1 => Some(TEEN[b].into()),
        0 if b != 0 => Some(lz!("oh {}", DIGIT[b])),
        0 => None,
        _ if b == 0 => Some(TY[a].into()),
        _ => Some(lz!("{} {}", TY[a], DIGIT[b])),
    }
}

/// `y` → `ies` or `+s` at the end.
fn pluralize_s(s: &str) -> String {
    match s.strip_suffix('y') {
        Some(p) => lz!("{p}ies"),
        None => lz!("{s}s"),
    }
}

/// `get_four_digit_year_graph(True)` on 4 digits + optional `s` / ` s` tail: `(input len, output)` list.
/// Priority union: if `thousand_graph` accepts the full input, only its output.
fn four_digit_year(t: &[char], keep: &dyn Fn(usize) -> bool) -> Vec<(usize, String)> {
    let mut res = Vec::new();
    if t.len() < 4 {
        return res;
    }
    let (Some(a), Some(b), Some(c), Some(e)) = (dg(t[0]), dg(t[1]), dg(t[2]), dg(t[3])) else { return res };
    // candidate input lengths: 4, 5 ("s"), 6 (" s")
    let tails = [
        (4, false, true),
        (5, true, t.get(4) == Some(&'s')),
        (6, true, t.get(4) == Some(&' ') && t.get(5) == Some(&'s')),
    ];
    for (len, s, present) in tails {
        if !present || !keep(len) {
            continue;
        }
        // thousand_graph
        let mut thousand = Vec::new();
        if a != 0 && b == 0 && c == 0 && !s {
            if e == 0 {
                thousand.push(lz!("{} thousand", DIGIT[a]));
            } else {
                thousand.push(lz!("{} thousand {}", DIGIT[a], DIGIT[e]));
            }
        }
        if a != 0 && b == 0 && c == 0 && e == 0 && s {
            // "000" + " "? + "s" accepted (s kept)
            thousand.push(lz!("{} thousands", DIGIT[a]));
        }
        if !thousand.is_empty() {
            res.extend(thousand.into_iter().map(|o| (len, o)));
            continue;
        }
        let mut other = Vec::new();
        if !s {
            if let (Some(x), Some(y)) = (ties2(a, b), ties2(c, e)) {
                other.push(lz!("{x} {y}"));
            }
            if c == 0 && e == 0 && a != 0 {
                if let Some(x) = ties2(a, b) {
                    other.push(lz!("{x} hundred"));
                }
            }
        } else {
            // graph_with_s: teen (ty | "1"→ten) "0s", or (teen|ties) "00"→hundred "s"; then y→ies | +s
            if e == 0 && a == 1 {
                if c >= 2 {
                    other.push(pluralize_s(&lz!("{} {}", TEEN[b], TY[c])));
                } else if c == 1 {
                    other.push(pluralize_s(&lz!("{} ten", TEEN[b])));
                }
            }
            if c == 0 && e == 0 {
                if let Some(x) = ties2(a, b) {
                    other.push(pluralize_s(&lz!("{x} hundred")));
                }
            }
        }
        res.extend(other.into_iter().map(|o| (len, o)));
    }
    res
}

/// `_get_year_graph`: every `(end, output)` from `start` whose end satisfies `keep`.
fn year(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool) -> Vec<(usize, String)> {
    let mut v = Vec::new();
    // every branch starts with a digit or "'" + digit
    if !text.get(start).is_some_and(|&c| is_digit(c) || c == '\'') {
        return v;
    }
    let t = &text[start..];
    if t.first().is_some_and(|&c| c == '1' || c == '2') {
        for (l, o) in four_digit_year(t, &|l| keep(start + l)) {
            v.push((start + l, o));
        }
    }
    // two digit year with s: "'"? ty "0s"
    let mut i = start;
    if text.get(i) == Some(&'\'') {
        i += 1;
    }
    if let (Some(a), Some('0'), Some('s')) = (text.get(i).and_then(|&c| dg(c)), text.get(i + 1), text.get(i + 2)) {
        if a >= 2 && keep(i + 3) {
            v.push((i + 3, pluralize_s(TY[a])));
        }
    }
    // year_with_suffix: (four_digit | three_digit) delete_space insert_space suffix
    let suffix_at = |e: usize| {
        let mut p = e;
        while p < text.len() && is_nemo_space(text[p]) {
            p += 1;
        }
        (p, YEAR_SUFFIX.prefix_matches(text, p).next().is_some())
    };
    for base_len in [4usize, 3] {
        if t.len() < base_len || !suffix_at(start + base_len).1 {
            continue;
        }
        let o = if base_len == 4 {
            match four_digit_year(t, &|l| l == 4).into_iter().next() {
                Some((_, o)) => o,
                None => continue,
            }
        } else {
            if !t[..3].iter().all(|&c| is_digit(c)) {
                continue;
            }
            let x: String = t[..1].iter().collect();
            let y: String = t[1..3].iter().collect();
            match (hundred_component(&x), hundred_component(&y)) {
                (Some(x), Some(y)) => lz!("{x} {y}"),
                _ => continue,
            }
        };
        let (p, _) = suffix_at(start + base_len);
        for (r, se) in YEAR_SUFFIX.prefix_matches(text, p) {
            if keep(se) {
                v.push((se, lz!("{o} {}", r.value)));
            }
        }
    }
    v
}

/// `_get_two_digit_year`: `dd @ priority(cardinal_graph(hundred component), single digits)`.
fn two_digit_year(text: &[char], start: usize) -> Option<String> {
    let s: String = text.get(start..start + 2)?.iter().collect();
    if !s.chars().all(is_digit) {
        return None;
    }
    hundred_component(&s).or_else(|| single_digits(&s))
}

/// Month spellings the tagger accepts: `month_name` as is, with the first letter or all letters `TO_LOWER`ed
/// (≥ 2 letters); `month_abbr` likewise (then an optional "."). Value = month name; bool = abbreviation.
static MONTHS: LazyLock<Table> = LazyLock::new(|| {
    let mut v: Vec<crate::text::Row> = Vec::new();
    for (abbr, t) in [(false, &*MONTH_NAME), (true, &*MONTH_ABBR)] {
        for r in t.rows() {
            let mut keys = vec![r.key.clone()];
            let mut c = r.key.chars();
            if let Some(f) = c.next() {
                if f.is_ascii_lowercase() {
                    keys.push(format!("{}{}", f.to_ascii_uppercase(), c.as_str()));
                }
            }
            if r.key.chars().count() >= 2 && r.key.chars().all(|c| c.is_ascii_lowercase()) {
                keys.push(r.key.to_ascii_uppercase());
            }
            keys.dedup();
            for k in keys {
                v.push(crate::text::Row { key: k, value: r.value.clone(), cost: if abbr { 1 } else { 0 } });
            }
        }
    }
    Table::from_rows(v)
});

/// Month names at `start`: `(end, value)`.
fn months(text: &[char], start: usize, f: &mut dyn FnMut(usize, &'static str)) {
    let table: &'static Table = &MONTHS;
    let rows: &'static [crate::text::Row] = table.rows();
    let mut seen: [(usize, &str); 8] = [(usize::MAX, ""); 8];
    let mut ns = 0;
    for (r, e) in table.prefix_matches(text, start) {
        // the same value as a 'static str (prefix_matches ties its rows to `text` too)
        let Some(v) = rows.iter().map(|x| x.value.as_str()).find(|x| *x == r.value) else { continue };
        let dot = r.cost == 1 && text.get(e) == Some(&'.');
        for e in [Some(e), dot.then_some(e + 1)].into_iter().flatten() {
            if !seen[..ns].iter().any(|(se, sv)| *se == e && *sv == v) && ns < seen.len() {
                seen[ns] = (e, v);
                ns += 1;
                f(e, v);
            }
        }
    }
}

const ENDINGS: [&str; 8] = ["rd", "th", "st", "nd", "RD", "TH", "ST", "ND"];

/// `day_graph`: `"the "? ((1|2)d | d | 3(0|1)) endings? @ cardinal`: `f(end, words, day number)`.
fn days(text: &[char], start: usize, f: &mut dyn FnMut(usize, String, u8)) {
    days_if(text, start, &|_| true, f)
}

/// [`days`] restricted to ends accepted by `keep` (nothing is named otherwise).
fn days_if(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, String, u8)) {
    for p in [start, start + 4] {
        if p != start && match_str(text, start, "the ").is_none() {
            continue;
        }
        let Some(a) = text.get(p).and_then(|&c| dg(c)) else { continue };
        // (number end, value): `d` or `(1|2)d | 3(0|1)`
        let mut nums = [(p + 1, a), (0, 0)];
        let mut nn = 1;
        if let Some(b) = text.get(p + 1).and_then(|&c| dg(c)) {
            if a == 1 || a == 2 || (a == 3 && b <= 1) {
                nums[1] = (p + 2, a * 10 + b);
                nn = 2;
            }
        }
        for &(e, n) in &nums[..nn] {
            // `hundred_component` on 1-2 digits: every value except a lone "0"
            if n == 0 {
                continue;
            }
            let mut ends = [0usize; 1 + ENDINGS.len()];
            let mut ne = 0;
            for x in std::iter::once(e).chain(ENDINGS.iter().filter_map(|end| match_str(text, e, end).map(|l| e + l))) {
                if keep(x) {
                    ends[ne] = x;
                    ne += 1;
                }
            }
            if ne == 0 {
                continue;
            }
            let w = if building() { day_name(n as u8) } else { String::new() };
            for &x in &ends[..ne] {
                f(x, w.clone(), n as u8);
            }
        }
    }
}

/// The words of day number `n` (1-31).
fn day_name(n: u8) -> String {
    hundred_component(&n.to_string()).unwrap_or_default()
}

fn q(k: &str, v: &str) -> String {
    lz!("{k}: \"{v}\"")
}

/// The year of a [`DateR`]: `text[s..e]` read by [`year`] (its first output at `e`) or, with `two`, by
/// [`two_digit_year`]; `glue` selects `graph_year`'s field form (0 ` year: "…"`, 1 ` year: ",…"`, 2
/// ` year: ", …"`) where the layout uses it.
#[derive(Clone, Copy, Debug)]
pub struct YearR {
    s: u32,
    e: u32,
    two: bool,
    glue: u8,
}

impl YearR {
    fn new(s: usize, e: usize, glue: u8) -> YearR {
        YearR { s: s as u32, e: e as u32, two: false, glue }
    }

    fn two(s: usize) -> YearR {
        YearR { s: s as u32, e: s as u32 + 2, two: true, glue: 0 }
    }

    fn words(self, text: &[char]) -> String {
        let (s, e) = (self.s as usize, self.e as usize);
        let w =
            if self.two { two_digit_year(text, s) } else { year(text, s, &|x| x == e).into_iter().next().map(|x| x.1) };
        w.unwrap_or_default()
    }
}

/// Field order of a [`DateR`].
#[derive(Clone, Copy, Debug)]
enum Layout {
    /// `month day? graph_year? preserve_order` (month names).
    MonthDay,
    /// `day month graph_year? preserve_order` (month names).
    DayMonth,
    /// `month day year preserve_order` (numeric).
    NumMdy,
    /// `day month year preserve_order` (numeric).
    NumDmy,
    /// `year month day`.
    Ymd,
    /// `year`.
    Year,
    /// `text: "<ordinal> <unit>" year`: indices into [`PERIOD_ORD`], [`PERIOD_UNIT`].
    Period(u8, u8),
}

const PERIOD_ORD: [&str; 4] = ["first", "second", "third", "fourth"];
const PERIOD_UNIT: [&str; 2] = ["half", "quarter"];

/// One `DateFst` reading: the layout, the month value, the day number (0: none) and the year.
#[derive(Clone, Copy, Debug)]
pub struct DateR {
    layout: Layout,
    month: &'static str,
    day: u8,
    year: Option<YearR>,
}

/// The `date { … }` tag of reading `r`.
fn tag(text: &[char], r: DateR) -> String {
    let mut s = String::with_capacity(96);
    let field = |s: &mut String, k: &str, v: &str| {
        s.push_str(k);
        s.push_str(": \"");
        s.push_str(v);
        s.push('"');
    };
    let day = |s: &mut String| field(s, "day", &day_name(r.day));
    let month = |s: &mut String| field(s, "month", r.month);
    let year = |s: &mut String| field(s, "year", &r.year.map(|y| y.words(text)).unwrap_or_default());
    let graph_year = |s: &mut String| {
        if let Some(y) = r.year {
            s.push_str([" year: \"", " year: \",", " year: \", "][y.glue as usize]);
            s.push_str(&y.words(text));
            s.push('"');
        }
    };
    s.push_str("date { ");
    match r.layout {
        Layout::MonthDay => {
            month(&mut s);
            if r.day != 0 {
                s.push(' ');
                day(&mut s);
            }
            graph_year(&mut s);
        }
        Layout::DayMonth => {
            day(&mut s);
            s.push(' ');
            month(&mut s);
            graph_year(&mut s);
        }
        Layout::NumMdy | Layout::NumDmy | Layout::Ymd => {
            let order: [&dyn Fn(&mut String); 3] = match r.layout {
                Layout::NumMdy => [&month, &day, &year],
                Layout::NumDmy => [&day, &month, &year],
                _ => [&year, &month, &day],
            };
            for (k, f) in order.iter().enumerate() {
                if k > 0 {
                    s.push(' ');
                }
                f(&mut s);
            }
        }
        Layout::Year => year(&mut s),
        Layout::Period(o, u) => {
            s.push_str("text: \"");
            s.push_str(PERIOD_ORD[o as usize]);
            s.push(' ');
            s.push_str(PERIOD_UNIT[u as usize]);
            s.push_str("\" ");
            year(&mut s);
        }
    }
    if !matches!(r.layout, Layout::Ymd | Layout::Year | Layout::Period(..)) {
        s.push_str(" preserve_order: true");
    }
    s.push_str(" }");
    s
}

/// Necessary shape of every digit-initial date branch: a 3-4 digit run (years, year suffixes), a 1-2 digit run
/// followed by a separator (`-`, `/`, `.`), `0s` (decades), a financial period (`1H`, `3Q`), or a 1-2 digit day (with
/// an optional ordinal ending) followed by whitespace and a month.
fn digit_date_shape(text: &[char], start: usize) -> bool {
    let mut e = start;
    while e < text.len() && is_digit(text[e]) {
        e += 1;
    }
    let n = e - start;
    if n == 3 || n == 4 {
        return true;
    }
    if n > 4 {
        return false;
    }
    if matches!(text.get(e), Some('-' | '/' | '.' | 'H' | 'Q' | 's')) {
        return true;
    }
    let mut d = e;
    for end in ENDINGS {
        if let Some(l) = match_str(text, e, end) {
            d = e + l;
        }
    }
    let mut m = d;
    while m < text.len() && is_nemo_space(text[m]) {
        m += 1;
    }
    m > d && MONTHS.prefix_matches(text, m).next().is_some()
}

/// The value of row `r` of the static table `t`, as a `'static str`.
fn static_value(t: &'static Table, r: &Row) -> &'static str {
    &t.rows()[row_index(t, r) as usize].value
}

/// Every `(end, fields)` of `DateFst` from `start`, with the reading of each.
pub fn date_spans(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool, out: &mut Outs<Option<DateR>>) {
    let n = text.len();
    // build a tag only for ends the caller can use
    macro_rules! add {
        ($e:expr, $s:expr, $r:expr) => {{
            let e = $e;
            if keep(e) {
                out.add_r(e, 0, $s, Some($r));
            }
        }};
    }
    // graph_year: " year: \"" + delete(" ") + year | " year: \"" + "," + " "? + year
    let graph_year = |p: usize, f: &mut dyn FnMut(usize, String, YearR)| {
        if text.get(p) == Some(&' ') {
            for (e, y) in year(text, p + 1, keep) {
                f(e, lz!(" year: \"{y}\""), YearR::new(p + 1, e, 0));
            }
        }
        if text.get(p) == Some(&',') {
            for (e, y) in year(text, p + 1, keep) {
                f(e, lz!(" year: \",{y}\""), YearR::new(p + 1, e, 1));
            }
            if text.get(p + 1) == Some(&' ') {
                for (e, y) in year(text, p + 2, keep) {
                    f(e, lz!(" year: \", {y}\""), YearR::new(p + 2, e, 2));
                }
            }
        }
    };
    let extra_space_end = |p: usize| -> Option<usize> {
        let mut e = p;
        while e < n && is_nemo_space(text[e]) {
            e += 1;
        }
        (e > p).then_some(e)
    };
    let po = " preserve_order: true";
    let r = |layout, month, day, year| DateR { layout, month, day, year };
    // MDY with month names
    let mut ms = Vec::new();
    months(text, start, &mut |e, v| ms.push((e, v)));
    let the_day = match_str(text, start, "the ").is_some() && text.get(start + 4).is_some_and(|&c| is_digit(c));
    if ms.is_empty() && !is_digit(text[start]) && text[start] != '\'' && !the_day {
        return;
    }
    if is_digit(text[start]) && !digit_date_shape(text, start) {
        return;
    }
    // branches (`lazy::branch_on`): 1 month-name MDY, 2 numeric MDY/DMY, 3 DMY with month names, 4 YMD, 5 year,
    // 6 financial period
    out.set_branch(1);
    for &(me, mv) in ms.iter().filter(|_| branch_on(1)) {
        let m = q("month", mv);
        let mut dd: Vec<(usize, String, u8)> = Vec::new();
        if let Some(se) = extra_space_end(me) {
            days(text, se, &mut |e, w, d| dd.push((e, w, d)));
        }
        let mut sp_days = Vec::new();
        if text.get(me) == Some(&' ') {
            days(text, me + 1, &mut |e, w, d| sp_days.push((e, w, d)));
        }
        for (de, dw, d) in dd.iter().chain(sp_days.iter()) {
            add!(*de, lz!("{m} {}{po}", q("day", dw)), r(Layout::MonthDay, mv, *d, None));
        }
        graph_year(me, &mut |e, y, yr| add!(e, lz!("{m}{y}{po}"), r(Layout::MonthDay, mv, 0, Some(yr))));
        for (de, dw, d) in &dd {
            graph_year(*de, &mut |e, y, yr| {
                add!(e, lz!("{m} {}{y}{po}", q("day", dw)), r(Layout::MonthDay, mv, *d, Some(yr)))
            });
        }
        if text.get(me) == Some(&'-') {
            let mut d2 = Vec::new();
            days(text, me + 1, &mut |e, w, d| d2.push((e, w, d)));
            for (de, dw, d) in d2 {
                let base = lz!("{m} {}", q("day", &dw));
                add!(de, lz!("{base}{po}"), r(Layout::MonthDay, mv, d, None));
                if text.get(de) == Some(&'-') {
                    // ("-"→" " + SIGMA) @ graph_year: only the " year" branch (needs a space)
                    for (e, y) in year(text, de + 1, keep) {
                        let yr = YearR::new(de + 1, e, 0);
                        add!(e, lz!("{base} year: \"{y}\"{po}"), r(Layout::MonthDay, mv, d, Some(yr)));
                    }
                }
            }
        }
    }
    // numeric MDY / DMY
    out.set_branch(2);
    for sep in ['-', '/', '.'].into_iter().filter(|_| branch_on(2)) {
        for (row, me) in MONTH_NUMBER.prefix_matches(text, start) {
            if text.get(me) != Some(&sep) {
                continue;
            }
            let mv = static_value(&MONTH_NUMBER, row);
            let m = q("month", mv);
            for dstart in [me + 1, me + 2] {
                if dstart == me + 2 && text.get(me + 1) != Some(&'0') {
                    continue;
                }
                let mut dd = Vec::new();
                days_if(text, dstart, &|e| text.get(e) == Some(&sep), &mut |e, w, d| dd.push((e, w, d)));
                for (de, dw, d) in dd {
                    let base = lz!("{m} {} ", q("day", &dw));
                    for (e, y) in year(text, de + 1, keep) {
                        let yr = YearR::new(de + 1, e, 0);
                        add!(e, lz!("{base}{}{po}", q("year", &y)), r(Layout::NumMdy, mv, d, Some(yr)));
                    }
                    if let Some(y) = two_digit_year(text, de + 1) {
                        let yr = YearR::two(de + 1);
                        add!(de + 3, lz!("{base}{}{po}", q("year", &y)), r(Layout::NumMdy, mv, d, Some(yr)));
                    }
                }
            }
        }
        // DMY numeric: day (2 digits not a month number) sep month_number sep year
        if let Some(two) = text.get(start..start + 2) {
            if two.iter().all(|&c| is_digit(c))
                && text.get(start + 2) == Some(&sep)
                && !MONTH_NUMBER.prefix_matches(two, 0).any(|(_, e)| e == 2)
            {
                let mut dd = Vec::new();
                days_if(text, start, &|e| e == start + 2, &mut |_, w, d| dd.push((w, d)));
                for (dw, d) in dd {
                    for (row, me) in MONTH_NUMBER.prefix_matches(text, start + 3) {
                        if text.get(me) != Some(&sep) {
                            continue;
                        }
                        let mv = static_value(&MONTH_NUMBER, row);
                        let base = lz!("{} {} ", q("day", &dw), q("month", mv));
                        for (e, y) in year(text, me + 1, keep) {
                            let yr = YearR::new(me + 1, e, 0);
                            add!(e, lz!("{base}{}{po}", q("year", &y)), r(Layout::NumDmy, mv, d, Some(yr)));
                        }
                        if let Some(y) = two_digit_year(text, me + 1) {
                            let yr = YearR::two(me + 1);
                            add!(me + 3, lz!("{base}{}{po}", q("year", &y)), r(Layout::NumDmy, mv, d, Some(yr)));
                        }
                    }
                }
            }
        }
    }
    // DMY with month names: day delete_extra_space month optional_year
    out.set_branch(3);
    let mut dd = Vec::new();
    if branch_on(3) {
        days_if(
            text,
            start,
            &|e| extra_space_end(e).is_some_and(|m| MONTHS.prefix_matches(text, m).next().is_some()),
            &mut |e, w, d| dd.push((e, w, d)),
        );
    }
    for (de, dw, d) in dd {
        let Some(ms0) = extra_space_end(de) else { continue };
        let mut ms = Vec::new();
        months(text, ms0, &mut |e, v| ms.push((e, v)));
        for (me, mv) in ms {
            let base = lz!("{} {}", q("day", &dw), q("month", mv));
            add!(me, lz!("{base}{po}"), r(Layout::DayMonth, mv, d, None));
            graph_year(me, &mut |e, y, yr| add!(e, lz!("{base}{y}{po}"), r(Layout::DayMonth, mv, d, Some(yr))));
        }
    }
    // YMD
    let ymd_sep = |e: usize| {
        let Some(&sep) = text.get(e) else { return false };
        matches!(sep, '-' | '/' | '.')
            && MONTH_NUMBER.prefix_matches(text, e + 1).any(|(_, me)| text.get(me) == Some(&sep))
    };
    out.set_branch(4);
    let mut ys: Vec<(usize, String, YearR)> = if branch_on(4) {
        year(text, start, &ymd_sep).into_iter().map(|(e, y)| (e, y, YearR::new(start, e, 0))).collect()
    } else {
        Vec::new()
    };
    if branch_on(4) && ymd_sep(start + 2) {
        if let Some(y) = two_digit_year(text, start) {
            ys.push((start + 2, y, YearR::two(start)));
        }
    }
    for sep in ['-', '/', '.'] {
        for (ye, yw, yr) in &ys {
            if text.get(*ye) != Some(&sep) {
                continue;
            }
            for (row, me) in MONTH_NUMBER.prefix_matches(text, ye + 1) {
                if text.get(me) != Some(&sep) {
                    continue;
                }
                let mv = static_value(&MONTH_NUMBER, row);
                for dstart in [me + 1, me + 2] {
                    if dstart == me + 2 && text.get(me + 1) != Some(&'0') {
                        continue;
                    }
                    days(text, dstart, &mut |e, w, d| {
                        let s = lz!("{} {} {}", q("year", yw), q("month", mv), q("day", &w));
                        add!(e, s, r(Layout::Ymd, mv, d, Some(*yr)))
                    });
                }
            }
        }
    }
    // year alone
    out.set_branch(5);
    if branch_on(5) {
        for (e, y) in year(text, start, keep) {
            add!(e, q("year", &y), r(Layout::Year, "", 0, Some(YearR::new(start, e, 0))));
        }
    }
    // financial period: (1|2)H | (1-4)Q, " ", two digit year
    out.set_branch(6);
    if let (true, Some(&a), Some(&k)) = (branch_on(6), text.get(start), text.get(start + 1)) {
        let ord = match a {
            '1' => Some(0),
            '2' => Some(1),
            '3' if k == 'Q' => Some(2),
            '4' if k == 'Q' => Some(3),
            _ => None,
        };
        let unit = match k {
            'H' => Some(0),
            'Q' => Some(1),
            _ => None,
        };
        if let (Some(o), Some(u)) = (ord, unit) {
            if let Some(y) = two_digit_year(text, start + 2) {
                let s = lz!("{} {}", q("text", &lz!("{} {}", PERIOD_ORD[o], PERIOD_UNIT[u])), q("year", &y));
                add!(start + 4, s, r(Layout::Period(o as u8, u as u8), "", 0, Some(YearR::two(start + 2))));
            }
        }
    }
}

pub struct Date;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 2;

impl Classifier for Date {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let c = text[start];
        if !(is_digit(c) || c == '\'' || (crate::text::is_alpha(c) && "jfmasondtJFMASONDT".contains(c))) {
            return;
        }
        let mut o = Outs::default();
        date_spans(text, start, &|e| crate::lang::en::lazy::keep_end(text, e), &mut o);
        for ((e, c, s), (b, r)) in o.into_entries() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            if e > start {
                let cost = cost(1.09) + c;
                crate::lang::en::lazy::record(MEMBER, start, e, cost, b, r.map_or(Reading::None, Reading::Date));
                out.push(Candidate { end: e, cost, rank: 0, tag: lz!("date {{ {s} }}") });
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Date(r) => Some(tag(text, r)),
            _ => None,
        })
    }
}

//! `ja/taggers/date.py`.
//!
//! Fields are written `key: "value"` and joined by single spaces inside `date { … }`:
//! - basic: `[era] (year: "N年" <sign>)+ month: "M月" <sign> day: "D日" [week]` (`<sign>` one of `/ . -`
//!   deleted, month/day `0?` + `month.tsv`/`day.tsv`, era from `era.tsv`);
//! - individual: `(y | m | d | week | y2) [week]` with `y = [era] year: "N年"`, `m = month: "0?M月" |
//!   month: "(中|下|上)旬"`, `d = day: "N日"`, `y2 = [era] year: "N(世紀)?" | era: "<abbrev>" year: "N年"`;
//! - combined: `(y m | m d | y m d) [week]`;
//! - nendai: `[era] year: "N年代"`.
//!
//! `week = <sp> ( <sp> weekday: "W" | "W から W" | "W W" (・ deleted) <sp> ) <sp>` with full- or half-width
//! brackets; `<sp>` = any run of ` `, U+202F, U+00A0, all deleted (every trailing run length is a span).
//!
//! The parse is a depth-first walk over a stack of field descriptors; a tag string is rendered only for each
//! accepted span (numbers are spelled at that point).

use super::Sink;
use super::cardinal::{ends, run_end, small_number, spell_into};
use crate::text::{Cost, Row, Table, match_str, starts_with};

crate::tsv!(MONTH, "ja/date/month.tsv");
crate::tsv!(DAY, "ja/date/day.tsv");
crate::tsv!(WEEK, "ja/date/week.tsv");
crate::tsv!(ERA, "ja/date/era.tsv");
crate::tsv!(ERA_ABBREV, "ja/date/era_abbrev.tsv");

#[derive(Clone, Copy)]
enum V {
    /// `just_cardinals` of `text[a..b]`.
    Num(usize, usize),
    Str(&'static str),
    /// Two weekdays joined (`から` for `〜`, nothing for `・`).
    Pair(&'static str, &'static str, &'static str),
}

#[derive(Clone, Copy)]
struct F {
    key: &'static str,
    v: V,
    tail: &'static str,
}

fn is_date_space(c: char) -> bool {
    matches!(c, ' ' | '\u{202f}' | '\u{a0}')
}

fn skip_spaces(t: &[char], mut p: usize) -> usize {
    while p < t.len() && is_date_space(t[p]) {
        p += 1;
    }
    p
}

fn is_sign(c: Option<&char>) -> bool {
    matches!(c, Some('/' | '.' | '-'))
}

/// `pynutil.delete("0").ques + table`: `(end, value)`.
fn table_0(table: &'static Table, t: &[char], p: usize, mut f: impl FnMut(usize, &'static str)) {
    small_number(table, t, p, &mut f);
    if t.get(p) == Some(&'0') {
        small_number(table, t, p + 1, &mut f);
    }
}

/// `week_component` spans at `p`: `(end, field)`.
fn week(t: &[char], p: usize, mut f: impl FnMut(usize, F)) {
    let q = skip_spaces(t, p);
    if !matches!(t.get(q), Some('(' | '（')) {
        return;
    }
    let q = skip_spaces(t, q + 1);
    let mut close = |e: usize, v: V| {
        let c = skip_spaces(t, e);
        if matches!(t.get(c), Some(')' | '）')) {
            let field = F { key: "weekday", v, tail: "" };
            for end in c + 1..=skip_spaces(t, c + 1) {
                f(end, field);
            }
        }
    };
    prefix_matches(&WEEK_ROWS, t, q, |r1, e1| {
        close(e1, V::Str(&r1.value));
        if let Some(&sep @ ('〜' | '・')) = t.get(e1) {
            let mid = if sep == '〜' { "から" } else { "" };
            prefix_matches(&WEEK_ROWS, t, e1 + 1, |r2, e2| close(e2, V::Pair(&r1.value, &r2.value, mid)));
        }
    });
}

struct Walk<'a, 'o, S: Sink> {
    t: &'a [char],
    start: usize,
    cost: Cost,
    stack: Vec<F>,
    out: &'o mut S,
}

impl<S: Sink> Walk<'_, '_, S> {
    fn emit(&mut self, end: usize) {
        if end <= self.start {
            return;
        }
        let (t, stack) = (self.t, &self.stack);
        self.out.push(end, self.cost, 0, |tag| {
            tag.push_str("date { ");
            for (k, f) in stack.iter().enumerate() {
                if k > 0 {
                    tag.push(' ');
                }
                tag.push_str(f.key);
                tag.push_str(": \"");
                match f.v {
                    V::Num(a, b) => {
                        spell_into(&t[a..b], tag);
                    }
                    V::Str(s) => tag.push_str(s),
                    V::Pair(a, b, mid) => {
                        tag.push_str(a);
                        tag.push_str(mid);
                        tag.push_str(b);
                    }
                }
                tag.push_str(f.tail);
                tag.push('"');
            }
            tag.push_str(" }");
        });
    }

    /// `… + closure(" " + week_component, 0, 1)`.
    fn emit_week(&mut self, end: usize) {
        self.emit(end);
        let t = self.t;
        week(t, end, |e, f| self.with(f, |w| w.emit(e)));
    }

    fn with(&mut self, f: F, body: impl FnOnce(&mut Self)) {
        if self.out.done() {
            return;
        }
        self.stack.push(f);
        body(self);
        self.stack.pop();
    }

    /// `[era_component " "]`, then `body` at the position after it.
    fn era_opt(&mut self, p: usize, mut body: impl FnMut(&mut Self, usize)) {
        body(self, p);
        let t = self.t;
        if !starts_era(t, p) {
            return;
        }
        prefix_matches(&ERA_ROWS, t, p, |row, end| {
            self.with(F { key: "era", v: V::Str(&row.value), tail: "" }, |w| body(w, end))
        });
    }

    /// `(year_component + signs)+ month signs day [week]` from `p` (inside the year closure).
    fn basic_years(&mut self, p: usize) {
        let t = self.t;
        if let Some(e) = run_end(t, p).filter(|&e| is_sign(t.get(e))) {
            self.with(F { key: "year", v: V::Num(p, e), tail: "年" }, |w| {
                w.basic_month_day(e + 1);
                w.basic_years(e + 1);
            });
        }
    }

    fn basic_month_day(&mut self, p: usize) {
        let t = self.t;
        table_0(&MONTH, t, p, |e, m| {
            if is_sign(t.get(e)) {
                self.with(F { key: "month", v: V::Str(m), tail: "月" }, |w| {
                    table_0(&DAY, t, e + 1, |e2, d| {
                        w.with(F { key: "day", v: V::Str(d), tail: "日" }, |w| w.emit_week(e2))
                    });
                });
            }
        });
    }

    /// `individual_year_component` (`[era] year: "N年"`), then `then` after it.
    fn year_individual(&mut self, p: usize, then: &mut dyn FnMut(&mut Self, usize)) {
        self.era_opt(p, |w, q| {
            let t = w.t;
            if let Some(e) = run_end(t, q).filter(|&e| t.get(e) == Some(&'年')) {
                w.with(F { key: "year", v: V::Num(q, e), tail: "年" }, |w| then(w, e + 1));
            }
        });
    }

    /// `individual_month_component`, then `then` after it.
    fn month_individual(&mut self, p: usize, then: &mut dyn FnMut(&mut Self, usize)) {
        let t = self.t;
        table_0(&MONTH, t, p, |e, m| {
            if t.get(e) == Some(&'月') {
                self.with(F { key: "month", v: V::Str(m), tail: "月" }, |w| then(w, e + 1));
            }
        });
        if matches!(t.get(p), Some('中' | '下' | '上')) && t.get(p + 1) == Some(&'旬') {
            let x = match t[p] {
                '中' => "中旬",
                '下' => "下旬",
                _ => "上旬",
            };
            self.with(F { key: "month", v: V::Str(x), tail: "" }, |w| then(w, p + 2));
        }
    }

    /// `individual_day_component`, then `then` after it.
    fn day_individual(&mut self, p: usize, then: &mut dyn FnMut(&mut Self, usize)) {
        let t = self.t;
        if let Some(e) = run_end(t, p).filter(|&e| t.get(e) == Some(&'日')) {
            self.with(F { key: "day", v: V::Num(p, e), tail: "日" }, |w| then(w, e + 1));
        }
    }

    fn year2(&mut self, p: usize) {
        self.era_opt(p, |w, q| {
            let t = w.t;
            for e in ends(t, q) {
                w.with(F { key: "year", v: V::Num(q, e), tail: "" }, |w| w.emit_week(e));
                if starts_with(t, e, "世紀") {
                    w.with(F { key: "year", v: V::Num(q, e), tail: "世紀" }, |w| w.emit_week(e + 2));
                }
            }
        });
        let t = self.t;
        if !starts_era(t, p) {
            return;
        }
        prefix_matches(&ERA_ABBREV_ROWS, t, p, |row, q| {
            self.with(F { key: "era", v: V::Str(&row.value), tail: "" }, |w| {
                for e in ends(t, q) {
                    w.with(F { key: "year", v: V::Num(q, e), tail: "年" }, |w| w.emit_week(e));
                }
            })
        });
    }

    fn nendai(&mut self, p: usize) {
        self.era_opt(p, |w, q| {
            let t = w.t;
            if let Some(e) = run_end(t, q).filter(|&e| starts_with(t, e, "年代")) {
                w.with(F { key: "year", v: V::Num(q, e), tail: "年代" }, |w| w.emit(e + 2));
            }
        });
    }
}

/// Whether an `era.tsv` or `era_abbrev.tsv` key can start at `p`.
fn starts_era(t: &[char], p: usize) -> bool {
    t.get(p).is_some_and(|&c| !c.is_ascii_digit() && ERA_FIRST.binary_search(&c).is_ok())
}

/// Whether any date branch can start with `c`: a digit, an era or era abbreviation, a weekday bracket (possibly
/// after spaces) or a `旬` month.
pub fn can_start(c: char) -> bool {
    c.is_ascii_digit()
        || is_date_space(c)
        || matches!(c, '(' | '（' | '中' | '下' | '上')
        || ERA_FIRST.binary_search(&c).is_ok()
}

/// First chars of the `era.tsv` and `era_abbrev.tsv` keys.
static ERA_FIRST: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let keys = ERA.rows().iter().chain(ERA_ABBREV.rows());
    let mut v: Vec<char> = keys.filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

thread_local! {
    /// The field stack of [`Walk`], reused across calls.
    static STACK: std::cell::Cell<Vec<F>> = const { std::cell::Cell::new(Vec::new()) };
}

/// A small table's rows in `Table::prefix_matches` order: shorter keys first, file order within one key length
/// (no key is a duplicate of another here, so the order among equal keys is file order too).
fn by_key_length(table: &'static Table) -> Vec<&'static Row> {
    let mut rows: Vec<&'static Row> = table.rows().iter().filter(|r| !r.key.is_empty()).collect();
    rows.sort_by_key(|r| r.key.chars().count());
    rows
}

static WEEK_ROWS: std::sync::LazyLock<Vec<&'static Row>> = std::sync::LazyLock::new(|| by_key_length(&WEEK));
static ERA_ROWS: std::sync::LazyLock<Vec<&'static Row>> = std::sync::LazyLock::new(|| by_key_length(&ERA));
static ERA_ABBREV_ROWS: std::sync::LazyLock<Vec<&'static Row>> =
    std::sync::LazyLock::new(|| by_key_length(&ERA_ABBREV));

/// `Table::prefix_matches` (non-empty keys) over [`by_key_length`] rows.
fn prefix_matches(rows: &[&'static Row], t: &[char], p: usize, mut f: impl FnMut(&'static Row, usize)) {
    for &row in rows {
        if let Some(n) = match_str(t, p, &row.key) {
            f(row, p + n);
        }
    }
}

pub struct Date {
    pub cost: Cost,
}

impl Date {
    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        if !can_start(t[start]) {
            return;
        }
        let mut stack = STACK.take();
        stack.clear();
        let mut w = Walk { t, start, cost: self.cost, stack, out };
        // graph_basic_date
        w.era_opt(start, |w, q| w.basic_years(q));
        // graph_individual_component
        w.year_individual(start, &mut |w, e| w.emit_week(e));
        w.month_individual(start, &mut |w, e| w.emit_week(e));
        w.day_individual(start, &mut |w, e| w.emit_week(e));
        week(t, start, |e, f| w.with(f, |w| w.emit_week(e)));
        w.year2(start);
        // graph_individual_component_combined
        w.year_individual(start, &mut |w, e| {
            w.month_individual(e, &mut |w, e2| {
                w.emit_week(e2);
                w.day_individual(e2, &mut |w, e3| w.emit_week(e3));
            })
        });
        w.month_individual(start, &mut |w, e| w.day_individual(e, &mut |w, e2| w.emit_week(e2)));
        // era_nendai
        w.nendai(start);
        STACK.set(w.stack);
    }
}

#[cfg(test)]
mod tests {
    #[test]
    fn month_and_day_keys_are_one_to_n() {
        for (table, n) in [(&*super::MONTH, 12), (&*super::DAY, 31)] {
            let keys: Vec<String> = table.rows().iter().map(|r| r.key.clone()).collect();
            assert_eq!(keys, (1..=n).map(|i: u32| i.to_string()).collect::<Vec<_>>());
        }
    }
}

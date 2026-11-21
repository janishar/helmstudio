//! `ko/taggers/money.py`, streamed (see graph_utils).

use std::sync::LazyLock;

use crate::lang::ko::At;
use crate::lang::ko::graph_utils::*;
use crate::lang::ko::number::{self, CharCount};
use crate::lang::ko::taggers::cardinal::push_reading;
use crate::text::{Row, Table, csv_rows, is_nemo_space};

/// `currency_major_prepended` (surface → unit) and `currency_major_appended` (unit → unit), as tables whose value
/// is the unit.
static PRE: LazyLock<Table> = LazyLock::new(|| table(false));
static APP: LazyLock<Table> = LazyLock::new(|| table(true));

fn table(appended: bool) -> Table {
    let rows = csv_rows(data!("money/currency_major.tsv"));
    Table::from_rows(
        rows.iter()
            .map(|r| Row { key: if appended { r[1].clone() } else { r[0].clone() }, value: r[1].clone(), cost: 0 })
            .collect(),
    )
}

/// The digits of `t[p..e]` without the deleted commas.
fn digits(t: &[char], p: usize, e: usize) -> ([u8; 17], usize) {
    let mut buf = [0u8; 17];
    let mut n = 0;
    for &c in &t[p..e] {
        if c != ',' {
            buf[n] = c as u8;
            n += 1;
        }
    }
    (buf, n)
}

fn push_integer(t: &[char], p: usize, e: usize, o: &mut String) {
    let (buf, n) = digits(t, p, e);
    number::read_into(&buf[..n], o);
}

/// `integer_part_fst @ graph_cardinal`: `0`, or a nonzero digit followed by digits and deleted commas. Calls `f`
/// with every end `keep` accepts; the value is [`push_integer`] on `t[p..end]`.
fn integer_ends(t: &[char], p: usize, keep: impl Fn(usize) -> bool, mut f: impl FnMut(usize)) {
    match t.get(p) {
        Some('0') => f(p + 1),
        Some(c) if c.is_ascii_digit() => {
            let mut n = 0;
            let mut e = p;
            while e < t.len() && (t[e].is_ascii_digit() || t[e] == ',') {
                if t[e] != ',' {
                    if n == 17 {
                        break;
                    }
                    n += 1;
                }
                e += 1;
                if keep(e) {
                    f(e);
                }
            }
        }
        _ => {}
    }
}

/// A `number_component` output: the integer `t[p..e]`, an optional scale word, an optional minor part `t[m..m+2]`.
#[derive(Clone, Copy)]
struct Amount {
    p: usize,
    e: usize,
    scale: Option<char>,
    minor: Option<usize>,
}

impl Amount {
    fn render(&self, t: &[char], o: &mut String) {
        o.push_str("integer_part: \"");
        push_integer(t, self.p, self.e, o);
        if let Some(c) = self.scale {
            o.push(c);
        }
        o.push_str("\" ");
        if let Some(m) = self.minor {
            o.push_str("minor_part: \"");
            push_reading(t, m, m + 2, o);
            o.push_str("\" ");
        }
    }

    /// The `integer_part` value has at most two chars.
    fn short(&self, t: &[char]) -> bool {
        let (buf, n) = digits(t, self.p, self.e);
        let mut c = CharCount(self.scale.is_some() as usize);
        number::read_into(&buf[..n], &mut c);
        c.0 <= 2
    }
}

/// `number_component = graph_integer_with_suffix | number_component_plain`, as `(end, amount)`.
fn number_component(t: &[char], p: usize, appended: bool, mut f: impl FnMut(usize, Amount)) {
    // appended: the component must be followed by a unit, so only ends before a unit, ".", or a scale word count
    let keep = |e: usize| {
        !appended || matches!(t.get(e), Some('.' | '만' | '억' | '조')) || APP.prefix_matches(t, e).next().is_some()
    };
    let plain = Amount { p, e: p, scale: None, minor: None };
    integer_ends(t, p, keep, |e| {
        if let Some(&c) = t.get(e) {
            if matches!(c, '만' | '억' | '조') {
                f(e + 1, Amount { e, scale: Some(c), ..plain });
            }
        }
    });
    integer_ends(t, p, keep, |e| {
        f(e, Amount { e, ..plain });
        if t.get(e) == Some(&'.')
            && t.get(e + 1).is_some_and(char::is_ascii_digit)
            && t.get(e + 2).is_some_and(char::is_ascii_digit)
            && t[e + 1] != '0'
        {
            f(e + 3, Amount { e, minor: Some(e + 1), ..plain });
        }
    });
}

/// Necessary condition for the appended form: some prefix of the digit/comma run (or the run plus `.dd`, or
/// plus a scale word) is followed by a currency unit. Units never start with a digit, comma or scale word, so
/// only the run end and the two extensions need checking (an end inside the run is followed by a digit or comma).
fn appended_possible(t: &[char], p: usize) -> bool {
    // `0` is read alone
    let e = if t[p] == '0' { p + 1 } else { run(t, p, |c| c.is_ascii_digit() || c == ',') };
    let unit_at = |k: usize| APP.prefix_matches(t, k).next().is_some();
    unit_at(e)
        || (t.get(e) == Some(&'.') && unit_at(e + 3))
        || (matches!(t.get(e), Some('만' | '억' | '조')) && unit_at(e + 1))
}

/// The non-digit chars a money span can start with (prepended currencies).
pub fn lead_chars() -> Vec<char> {
    PRE.rows().iter().filter_map(|r| r.key.chars().next()).collect()
}

/// Necessary condition for a money span at a digit.
pub fn digit_gate(a: &At) -> bool {
    appended_possible(a.t, a.p)
}

/// The flag marks the fitted tie rule (see `Tie` in mod.rs): an amount of at most two chars not followed by a digit.
pub fn tag(t: &[char], p: usize, f: TagSink<'_>) {
    let loses = |a: &Amount, end: usize| !t.get(end).is_some_and(char::is_ascii_digit) && a.short(t);
    for (row, e) in PRE.prefix_matches(t, p) {
        for k in e..=run(t, e, is_nemo_space) {
            number_component(t, k, false, |e2, a| {
                f(e2, 0, loses(&a, e2), &|o: &mut String| {
                    o.push_str("currency_maj: \"");
                    o.push_str(&row.value);
                    o.push_str("\" ");
                    a.render(t, o);
                });
            });
        }
    }
    if t.get(p).is_some_and(char::is_ascii_digit) && appended_possible(t, p) {
        number_component(t, p, true, |e, a| {
            for (row, e2) in APP.prefix_matches(t, e) {
                f(e2, 0, loses(&a, e2), &|o: &mut String| {
                    a.render(t, o);
                    o.push_str("currency_maj: \"");
                    o.push_str(&row.value);
                    o.push_str("\" ");
                });
            }
        });
    }
}

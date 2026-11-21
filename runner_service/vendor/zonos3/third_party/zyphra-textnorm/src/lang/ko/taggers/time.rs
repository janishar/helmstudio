//! `ko/taggers/time.py`, streamed (see graph_utils).

use std::sync::LazyLock;

use crate::lang::ko::At;
use crate::lang::ko::graph_utils::*;
use crate::lang::ko::taggers::cardinal::{self, push_reading};
use crate::text::{Cost, Table, is_nemo_space, match_str};

static HOUR: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("time/hour.tsv")));
static DIVISION: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("time/division.tsv")));

type Part = fn(&[char], usize, Sink<'_>);

/// `strip0 + x` starts.
fn strip0(t: &[char], p: usize) -> impl Iterator<Item = usize> {
    let z = t.get(p) == Some(&'0');
    std::iter::once(p).chain(z.then_some(p + 1))
}

/// `strip0 + hour_clock` values.
fn hour_clock(t: &[char], p: usize, f: Sink<'_>) {
    for q in strip0(t, p) {
        for (row, e) in HOUR.prefix_matches(t, q) {
            f(e, row.cost, &|o: &mut String| o.push_str(&row.value));
        }
    }
}

/// `graph_cardinal` restricted to 13-24 (two digits from `p`).
fn is_13_24(t: &[char], p: usize) -> bool {
    matches!((t.get(p), t.get(p + 1)), (Some('1'), Some('3'..='9')) | (Some('2'), Some('0'..='4')))
}

/// `hours: "<value>시"`.
fn hours(o: &mut String, v: Render<'_>) {
    o.push_str("hours: \"");
    v(o);
    o.push_str("시\"");
}

/// Hangul `hour_component_ko`: `hours: "…시"` over `<digits>시`.
fn hour_ko(t: &[char], p: usize, f: Sink<'_>) {
    let mut emit = |e: usize, c: Cost, v: Render<'_>| {
        if t.get(e) == Some(&'시') {
            f(e + 1, c, &|o: &mut String| hours(o, v));
        }
    };
    if is_13_24(t, p) {
        emit(p + 2, 0, &|o: &mut String| push_reading(t, p, p + 2, o));
    }
    // h_zero = strip0 + "0" → 영
    for q in strip0(t, p) {
        if t.get(q) == Some(&'0') {
            emit(q + 1, 0, &|o: &mut String| o.push('영'));
        }
    }
    hour_clock(t, p, &mut emit);
}

/// `<key>: "<strip0 cardinal><unit>"` over `<digits><unit>`.
fn unit_ko(t: &[char], p: usize, key: &str, unit: char, f: Sink<'_>) {
    cardinal::lz_ends(t, p, |q, e| {
        if t.get(e) == Some(&unit) {
            f(e + 1, 0, &|o: &mut String| {
                o.push_str(key);
                o.push_str(": \"");
                push_reading(t, q, e, o);
                o.push(unit);
                o.push('"');
            });
        }
    });
}

fn minute_ko(t: &[char], p: usize, f: Sink<'_>) {
    if t.get(p) == Some(&'반') {
        f(p + 1, 0, &|o: &mut String| o.push_str("minutes: \"반\""));
        for a in ['쯤', '경'] {
            if t.get(p + 1) == Some(&a) {
                f(p + 2, 0, &|o: &mut String| {
                    o.push_str("minutes: \"반");
                    o.push(a);
                    o.push('"');
                });
            }
        }
    }
    unit_ko(t, p, "minutes", '분', f);
}

fn second_ko(t: &[char], p: usize, f: Sink<'_>) {
    unit_ko(t, p, "seconds", '초', f);
}

/// `a + SEP + b` with `SEP = delete_space + insert(" ")`.
fn sep_then(t: &[char], a: Gen<'_>, b: Part, f: Sink<'_>) {
    a(&mut |e, c, ra| {
        for k in e..=run(t, e, is_nemo_space) {
            b(t, k, &mut |e2, c2, rb| {
                f(e2, c + c2, &|o: &mut String| {
                    ra(o);
                    o.push(' ');
                    rb(o);
                })
            });
        }
    });
}

fn division(t: &[char], p: usize, f: Sink<'_>) {
    for (row, e) in DIVISION.prefix_matches(t, p) {
        f(e, row.cost, &|o: &mut String| {
            o.push_str("suffix: \"");
            o.push_str(&row.value);
            o.push('"');
        });
    }
}

/// `core | suffix_prefix + core`.
fn prefixed(t: &[char], p: usize, core: Part, f: Sink<'_>) {
    core(t, p, f);
    if DIVISION.prefix_matches(t, p).next().is_some() {
        sep_then(t, &|g: Sink<'_>| division(t, p, g), core, f);
    }
}

/// `suffix_prefix_opt + core(p) + suffix_postfix_opt`: the unsuffixed outputs first, then each with a suffix.
fn with_suffixes(t: &[char], p: usize, core: Part, f: Sink<'_>) {
    let division_after = |e: usize| DIVISION.prefix_matches(t, run(t, e, is_nemo_space)).next().is_some();
    let mut post = false;
    prefixed(t, p, core, &mut |e, c, r| {
        post |= division_after(e);
        f(e, c, r)
    });
    if post {
        sep_then(t, &|g: Sink<'_>| prefixed(t, p, core, g), division, f);
    }
}

/// From digits the first component is chosen by the unit after the digit run (시 hour, 분 minute, 초 second); the
/// other components emit nothing there.
fn hangul_core(t: &[char], p: usize, f: Sink<'_>) {
    let (hour, minute, second) = if t.get(p).is_some_and(char::is_ascii_digit) {
        let u = t.get(run(t, p, is_digit));
        (u == Some(&'시'), u == Some(&'분'), u == Some(&'초'))
    } else {
        (true, true, true)
    };
    let h = |g: Sink<'_>| hour_ko(t, p, g);
    let hm = |g: Sink<'_>| sep_then(t, &h, minute_ko, g);
    let m = |g: Sink<'_>| minute_ko(t, p, g);
    if hour {
        h(f);
        hm(f);
        sep_then(t, &hm, second_ko, f);
    }
    if minute {
        m(f);
        sep_then(t, &m, second_ko, f);
    }
    if second {
        second_ko(t, p, f);
    }
}

fn special_core(t: &[char], p: usize, f: Sink<'_>) {
    cross(t, p, "정오", "hours: \"열두시\"", f);
    cross(t, p, "자정", "hours: \"영시\"", f);
}

/// `hour_clock_component` for the clock forms.
fn hour_cc(t: &[char], p: usize, f: Sink<'_>) {
    hour_clock(t, p, &mut |e, c, v| f(e, c, &|o: &mut String| hours(o, v)));
    if match_str(t, p, "00").is_some() {
        f(p + 2, 0, &|o: &mut String| o.push_str("hours: \"영시\""));
    }
    if t.get(p) == Some(&'0') {
        f(p + 1, 0, &|o: &mut String| o.push_str("hours: \"영시\""));
    }
    if is_13_24(t, p) {
        f(p + 2, 0, &|o: &mut String| hours(o, &|o: &mut String| push_reading(t, p, p + 2, o)));
    }
}

/// `a + delete_space.ques + ":" + delete_space.ques + (cross("00", "") | closure(" " + key: "<strip0
/// cardinal><unit>", 0, 1))`.
fn after_colon(t: &[char], a: Gen<'_>, key: &str, unit: char, f: Sink<'_>) {
    a(&mut |e, c, ra| {
        for a in e..=run(t, e, is_nemo_space) {
            if t.get(a) != Some(&':') {
                continue;
            }
            for k in a + 1..=run(t, a + 1, is_nemo_space) {
                if match_str(t, k, "00").is_some() {
                    f(k + 2, c, ra);
                }
                f(k, c, ra);
                cardinal::lz_ends(t, k, |q, e2| {
                    f(e2, c, &|o: &mut String| {
                        ra(o);
                        o.push(' ');
                        o.push_str(key);
                        o.push_str(": \"");
                        push_reading(t, q, e2, o);
                        o.push(unit);
                        o.push('"');
                    })
                });
            }
        }
    });
}

fn hm_core(t: &[char], p: usize, f: Sink<'_>) {
    let d = run(t, p, is_digit);
    if d == p || !matches!(t.get(run(t, d, is_nemo_space)), Some(':')) {
        return;
    }
    after_colon(t, &|g: Sink<'_>| hour_cc(t, p, g), "minutes", '분', f);
}

fn hms_core(t: &[char], p: usize, f: Sink<'_>) {
    after_colon(t, &|g: Sink<'_>| hm_core(t, p, g), "seconds", '초', f);
}

/// The non-digit chars a time span can start with (special and division words).
pub fn lead_chars() -> Vec<char> {
    let mut v = vec!['반', '정', '자'];
    v.extend(DIVISION.rows().iter().filter_map(|r| r.key.chars().next()));
    v
}

/// Necessary condition for a time span at a digit: digits + 시/분/초, or digits + ":" (after spaces).
pub fn digit_gate(a: &At) -> bool {
    matches!(a.u, Some('시' | '분' | '초')) || a.t.get(run(a.t, a.d, is_nemo_space)) == Some(&':')
}

pub fn tag(t: &[char], p: usize, f: TagSink<'_>) {
    let Some(&c) = t.get(p) else { return };
    let digit = c.is_ascii_digit();
    // from a digit only the Hangul unit forms (digits + 시/분/초) or the clock forms (digits + ":") apply
    let (unit, clock) = if digit {
        let d = run(t, p, is_digit);
        (matches!(t.get(d), Some('시' | '분' | '초')), t.get(run(t, d, is_nemo_space)) == Some(&':'))
    } else if matches!(c, '반' | '정' | '자') || DIVISION.prefix_matches(t, p).next().is_some() {
        (true, true)
    } else {
        return;
    };
    let f = &mut |e: usize, c: Cost, r: Render<'_>| f(e, c, false, r);
    if !digit {
        with_suffixes(t, p, special_core, f);
    }
    if unit {
        with_suffixes(t, p, hangul_core, f);
    }
    if clock {
        with_suffixes(t, p, hm_core, f);
        with_suffixes(t, p, hms_core, f);
    }
}

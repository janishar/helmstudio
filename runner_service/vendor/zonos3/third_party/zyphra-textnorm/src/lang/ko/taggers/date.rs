//! `ko/taggers/date.py`, streamed (see graph_utils): every sub-grammar sends its `(end, cost, output)` to a sink in
//! the order the upstream union/sequence lists them; outputs are written only for the candidate that wins.

use std::sync::LazyLock;

use crate::lang::ko::At;
use crate::lang::ko::graph_utils::*;
use crate::lang::ko::taggers::cardinal::{self, push_reading};
use crate::text::{Cost, Table, cost, is_nemo_space, match_str};

static WEEK: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("date/week.tsv")));

fn d(t: &[char], i: usize) -> Option<char> {
    t.get(i).copied().filter(char::is_ascii_digit)
}

/// `month_cardinal = strip0 + (month_exceptions | graph_cardinal_non_exceptions)` on `t[q..e]` (a span from
/// [`cardinal::lz_ends`]).
fn push_month(t: &[char], q: usize, e: usize, o: &mut String) {
    match &t[q..e] {
        ['6'] => o.push('유'),
        ['1', '0'] => o.push('시'),
        _ => push_reading(t, q, e, o),
    }
}

/// The first `strip0 + cardinal` span from `p` ending at `e`.
fn lz_span(t: &[char], p: usize, e: usize) -> Option<usize> {
    let mut r = None;
    cardinal::lz_ends(t, p, |q, x| {
        if x == e && r.is_none() {
            r = Some(q);
        }
    });
    r
}

/// `MM @ month_cardinal`: the value's span start (the value ends at `p + 2`).
fn mm(t: &[char], p: usize) -> Option<usize> {
    let (a, b) = (d(t, p)?, d(t, p + 1)?);
    let ok = (a == '0' && b != '0') || (a == '1' && matches!(b, '0' | '1' | '2'));
    if !ok {
        return None;
    }
    lz_span(t, p, p + 2)
}

/// `DD @ cardinal_lz`: the value's span start (the value ends at `p + 2`).
fn dd(t: &[char], p: usize) -> Option<usize> {
    let (a, b) = (d(t, p)?, d(t, p + 1)?);
    let ok = match a {
        '0' => b != '0',
        '1' | '2' => true,
        '3' => matches!(b, '0' | '1'),
        _ => false,
    };
    if !ok {
        return None;
    }
    lz_span(t, p, p + 2)
}

fn signs(t: &[char], p: usize) -> Option<usize> {
    matches!(t.get(p), Some('/' | '.' | '-')).then_some(p + 1)
}

/// `front_bracket`/`preceding_bracket`: ws* (open|close) ws*, every whitespace split (empty when no bracket).
fn bracket(t: &[char], p: usize, a: char, b: char) -> std::ops::Range<usize> {
    let e = run(t, p, is_nemo_space);
    match t.get(e) {
        Some(&c) if c == a || c == b => e + 1..run(t, e + 1, is_nemo_space) + 1,
        _ => 0..0,
    }
}

/// The `week.tsv` value at `p` (every key is one char; the first row of a key wins, as the FST's union).
fn week_key(t: &[char], p: usize) -> Option<&str> {
    let c = *t.get(p)?;
    if !matches!(c, '월' | '화' | '수' | '목' | '금' | '토' | '일' | '공') {
        return None;
    }
    WEEK.prefix_matches(t, p).next().map(|(r, _)| r.value.as_str())
}

fn weekday(o: &mut String, parts: &[&str]) {
    o.push_str("weekday: \"");
    for s in parts {
        o.push_str(s);
    }
    o.push('"');
}

/// `week_component = week_component_bracketed | week_component_plain`.
fn week_component(t: &[char], p: usize, f: Sink<'_>) {
    // every branch starts with a week char or (after spaces) an opening bracket
    let q = run(t, p, is_nemo_space);
    if !matches!(t.get(q), Some('(' | '（')) && week_key(t, p).is_none() {
        return;
    }
    for fp in bracket(t, p, '(', '（') {
        let Some(w1) = week_key(t, fp) else { continue };
        for b in bracket(t, fp + 1, ')', '）') {
            f(b, 0, &|o: &mut String| weekday(o, &[w1]));
        }
        for (sep, out) in [('〜', "부터"), ('・', "")] {
            if t.get(fp + 1) == Some(&sep) {
                if let Some(w2) = week_key(t, fp + 2) {
                    for b in bracket(t, fp + 3, ')', '）') {
                        f(b, 0, &|o: &mut String| weekday(o, &[w1, out, w2]));
                    }
                }
            }
        }
    }
    if let Some(w) = week_key(t, p) {
        f(p + 1, 0, &|o: &mut String| weekday(o, &[w]));
    }
}

/// `pynini.closure(pynini.closure(insert_space, 0, 1) + week_component, 0, 1)`.
fn opt_week(t: &[char], p: usize, f: Sink<'_>) {
    f(p, 0, &|_: &mut String| {});
    week_component(t, p, f);
    week_component(t, p, &mut |e, c, r| {
        f(e, c, &|o: &mut String| {
            o.push(' ');
            r(o);
        })
    });
}

/// `x + pynini.closure(insert_space + week_component, 0, 1)`: each `x` with a following week first, then `x`.
fn with_tail_week(t: &[char], x: Gen<'_>, f: Sink<'_>) {
    let week_may_follow =
        |e: usize| week_key(t, e).is_some() || matches!(t.get(run(t, e, is_nemo_space)), Some('(' | '（'));
    x(&mut |e, c, r| {
        if week_may_follow(e) {
            week_component(t, e, &mut |e2, c2, r2| {
                f(e2, c + c2, &|o: &mut String| {
                    r(o);
                    o.push(' ');
                    r2(o);
                })
            });
        }
        f(e, c, r);
    });
}

fn era(t: &[char], p: usize) -> Option<(usize, &'static str)> {
    if t.get(p) != Some(&'기') {
        return None;
    }
    ["기원전", "기원후"].into_iter().find_map(|e| match_str(t, p, e).map(|k| (p + k, e)))
}

/// `(YEAR_1TO4 @ graph_cardinal) + "년"` (the year value with its 년).
fn year_1to4(t: &[char], p: usize, f: Sink<'_>) {
    let z = run(t, p, |c| c == '0').min(p + 3);
    for q in p..=z {
        if q > p && t[q - 1] != '0' {
            break;
        }
        let Some(c) = d(t, q) else { continue };
        if c == '0' {
            continue;
        }
        let de = run(t, q, is_digit).min(q + 4);
        for e in q + 1..=de {
            let Some(k) = match_str(t, e, "년") else { continue };
            f(e + k, 0, &|o: &mut String| {
                push_reading(t, q, e, o);
                o.push('년');
            });
        }
    }
}

fn individual_year(t: &[char], p: usize, f: Sink<'_>) {
    if let Some((e, name)) = era(t, p) {
        year_1to4(t, e, &mut |e2, c, y| {
            f(e2, c, &|o: &mut String| {
                o.push_str("era: \"");
                o.push_str(name);
                field(o, "\" year", y);
            })
        });
    }
    year_1to4(t, p, &mut |e, c, y| f(e, c, &|o: &mut String| field(o, "year", y)));
}

fn individual_month(t: &[char], p: usize, f: Sink<'_>) {
    cardinal::lz_ends(t, p, |q, e| {
        if t.get(e) == Some(&'월') {
            f(e + 1, 0, &|o: &mut String| {
                o.push_str("month: \"");
                push_month(t, q, e, o);
                o.push_str("월\"");
            });
        }
    });
}

/// The josa variants of `individual_month_component_with_josa` (the bare month is listed separately).
fn month_josa(t: &[char], p: usize, f: Sink<'_>) {
    individual_month(t, p, &mut |e, c, m| {
        for j in ["에", "은", "는", "에는"] {
            if let Some(k) = match_str(t, e, j) {
                f(e + k, c, &|o: &mut String| {
                    m(o);
                    o.pop();
                    o.push_str(j);
                    o.push('"');
                });
            }
        }
    });
}

fn individual_day(t: &[char], p: usize, f: Sink<'_>) {
    cardinal::lz_ends(t, p, |q, e| {
        if t.get(e) == Some(&'일') {
            f(e + 1, 0, &|o: &mut String| {
                o.push_str("day: \"");
                push_reading(t, q, e, o);
                o.push_str("일\"");
            });
        }
    });
}

/// `x + closure(insert_space, 0, 1) + week_component_full_word`.
fn with_full_week(t: &[char], x: Gen<'_>, f: Sink<'_>) {
    x(&mut |e, c, s| {
        if !matches!(t.get(e), Some('월' | '화' | '수' | '목' | '금' | '토' | '일' | '공' | ' ')) {
            return;
        }
        for w in ["월요일", "화요일", "수요일", "목요일", "금요일", "토요일", "일요일", "공휴일"]
        {
            if let Some(k) = match_str(t, e, w) {
                for sep in ["", " "] {
                    f(e + k, c, &|o: &mut String| {
                        s(o);
                        o.push_str(sep);
                        weekday(o, &[w]);
                    });
                }
            }
        }
    });
}

type Part = fn(&[char], usize, Sink<'_>);

/// `a + " " + b`.
fn join(t: &[char], a: Gen<'_>, b: Part, f: Sink<'_>) {
    a(&mut |e, c, ra| {
        b(t, e, &mut |e2, c2, rb| {
            f(e2, c + c2, &|o: &mut String| {
                ra(o);
                o.push(' ');
                rb(o);
            })
        })
    });
}

/// `year_component_md_strict` (y4 | y2 at 1.0).
fn year_md(t: &[char], p: usize, f: Sink<'_>) {
    if matches!(t.get(p), Some('1' | '2')) && (1..4).all(|k| d(t, p + k).is_some()) {
        f(p + 4, 0, &|o: &mut String| {
            o.push_str("year: \"");
            push_reading(t, p, p + 4, o);
            o.push_str("년\"");
        });
    }
    // (`graph_cardinal` rejects a leading zero)
    if d(t, p).is_some_and(|c| c != '0') && d(t, p + 1).is_some() {
        f(p + 2, cost(1.0), &|o: &mut String| {
            o.push_str("year: \"");
            push_reading(t, p, p + 2, o);
            o.push_str("년\"");
        });
    }
}

fn md_date(t: &[char], p: usize, month_first: bool, f: Sink<'_>) {
    let first = if month_first { mm(t, p) } else { dd(t, p) };
    let Some(qa) = first else { return };
    let Some(q) = signs(t, p + 2) else { return };
    let second = if month_first { dd(t, q) } else { mm(t, q) };
    let Some(qb) = second else { return };
    let Some(q2) = signs(t, q + 2) else { return };
    let head = |o: &mut String| {
        if month_first {
            o.push_str("month: \"");
            push_month(t, qa, p + 2, o);
            o.push_str("월\" day: \"");
            push_reading(t, qb, q + 2, o);
            o.push_str("일\" ");
        } else {
            o.push_str("day: \"");
            push_reading(t, qa, p + 2, o);
            o.push_str("일\" month: \"");
            push_month(t, qb, q + 2, o);
            o.push_str("월\" ");
        }
    };
    year_md(t, q2, &mut |e, c, y| {
        opt_week(t, e, &mut |e2, c2, w| {
            f(e2, c + c2, &|o: &mut String| {
                head(o);
                y(o);
                w(o);
            })
        })
    });
}

fn era_head(name: Option<&str>, o: &mut String) {
    if let Some(name) = name {
        o.push_str("era: \"");
        o.push_str(name);
        o.push_str("\" ");
    }
}

fn basic(t: &[char], p: usize, f: Sink<'_>) {
    let era = era(t, p);
    for (q, h) in std::iter::once((p, None)).chain(era.map(|(e, name)| (e, Some(name)))) {
        if !(matches!(t.get(q), Some('1' | '2')) && (1..4).all(|k| d(t, q + k).is_some())) {
            continue;
        }
        let Some(m0) = signs(t, q + 4) else { continue };
        cardinal::lz_ends(t, m0, |mq, me| {
            let Some(d0) = signs(t, me) else { return };
            cardinal::lz_ends(t, d0, |dq, de| {
                // (the day may be followed by anything: opt_week includes the empty week)
                opt_week(t, de, &mut |we, c, w| {
                    f(we, c, &|o: &mut String| {
                        era_head(h, o);
                        o.push_str("year: \"");
                        push_reading(t, q, q + 4, o);
                        o.push_str("년\" month: \"");
                        push_month(t, mq, me, o);
                        o.push_str("월\" day: \"");
                        push_reading(t, dq, de, o);
                        o.push_str("일\"");
                        w(o);
                    })
                })
            });
        });
    }
}

/// `graph_individual_component | graph_individual_component_combined` from a digit or `기` at `p`. A year span
/// starts with an era or digits followed by `년`, a month (day) span with digits followed by `월` (`일`), so `unit`
/// (the char after the digit run) selects the branches that can match; the others emit nothing.
fn individual_and_combined(t: &[char], p: usize, unit: Option<char>, f: Sink<'_>) {
    let year_first = unit == Some('년') || t[p] == '기';
    let (month_first, day_first) = (unit == Some('월'), unit == Some('일'));
    let individual = |g: Sink<'_>| {
        if day_first {
            with_full_week(t, &|h: Sink<'_>| individual_day(t, p, h), g);
        }
        if month_first {
            with_full_week(t, &|h: Sink<'_>| individual_month(t, p, h), g);
        }
        if year_first {
            individual_year(t, p, g);
        }
        if month_first {
            month_josa(t, p, g);
            individual_month(t, p, g);
        }
        if day_first {
            individual_day(t, p, g);
        }
    };
    with_tail_week(t, &individual, f);
    let combined = |g: Sink<'_>| {
        let year = |h: Sink<'_>| individual_year(t, p, h);
        if year_first {
            join(t, &year, individual_month, g);
        }
        if month_first {
            join(t, &|h: Sink<'_>| individual_month(t, p, h), individual_day, g);
        }
        if year_first {
            join(t, &|h: Sink<'_>| join(t, &year, individual_month, h), individual_day, g);
        }
    };
    if year_first || month_first {
        with_tail_week(t, &combined, f);
    }
}

fn era_nendai(t: &[char], p: usize, f: Sink<'_>) {
    let era = era(t, p);
    for (q, h) in std::iter::once((p, None)).chain(era.map(|(e, name)| (e, Some(name)))) {
        for e in cardinal::ends(t, q) {
            if let Some(k) = match_str(t, e, "년대") {
                f(e + k, 0, &|o: &mut String| {
                    era_head(h, o);
                    o.push_str("year: \"");
                    push_reading(t, q, e, o);
                    o.push_str("년대\"");
                });
            }
        }
    }
}

/// The non-digit chars a date span can start with (whitespace too: the bracketed week deletes leading spaces).
pub fn lead_chars() -> Vec<char> {
    let mut v = vec!['기', '(', '（', ' ', '\t', '\n', '\r', '\u{a0}'];
    v.extend(WEEK.rows().iter().filter_map(|r| r.key.chars().next()));
    v
}

/// Necessary condition for a date span at a digit.
pub fn digit_gate(a: &At) -> bool {
    matches!(a.u, Some('/' | '.' | '-' | '년' | '월' | '일'))
}

pub fn tag(t: &[char], p: usize, f: TagSink<'_>) {
    let Some(&c) = t.get(p) else { return };
    let digit = c.is_ascii_digit();
    if digit && !matches!(t.get(run(t, p, is_digit)), Some('/' | '.' | '-' | '년' | '월' | '일')) {
        return;
    }
    if !(digit || c == '기' || c == '(' || c == '（' || is_nemo_space(c) || week_key(t, p).is_some()) {
        return;
    }
    let f = &mut |e: usize, c: Cost, r: Render<'_>| f(e, c, false, r);
    if !digit && c != '기' {
        // only the bare week component can start here (bracketed or plain), plus its optional tail week
        return with_tail_week(t, &|g: Sink<'_>| week_component(t, p, g), f);
    }
    let unit = t.get(run(t, p, is_digit)).copied();
    let sep = matches!(unit, Some('/' | '.' | '-'));
    if sep || c == '기' {
        basic(t, p, f);
    }
    if sep {
        md_date(t, p, true, f);
        md_date(t, p, false, f);
    }
    if !sep {
        individual_and_combined(t, p, unit, f);
        if unit == Some('년') || c == '기' {
            era_nendai(t, p, f);
        }
    }
}

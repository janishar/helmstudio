//! `zh/taggers/time.py`: `H:M[:S]` (`:`/`：`, values get `点`/`分`/`秒`), clock/duration words (`H点 M分 S秒`, `小时`,
//! `刻`, …), back-counts with `差`, each optionally followed by an AM/PM suffix.
//!
//! The back-count branches with zero `差` and the suffix branches with an empty suffix produce the same fields as
//! the plain branches (only spacing or a `suffix: ""` the parser rejects differ); the oracle picks the plain ones,
//! so only those are emitted.

use std::sync::LazyLock;

use super::{Gen, Recall, Sink, digit_end, field, prefix_static};
use crate::text::{Table, match_str};

crate::tsv!(HOUR, "zh/time/hour.tsv");
crate::tsv!(MINUTE, "zh/time/minute.tsv");
crate::tsv!(SECOND, "zh/time/second.tsv");
crate::tsv!(AM, "zh/time/AM.tsv");
crate::tsv!(PM, "zh/time/PM.tsv");

const HOUR_WORDS: &[(&str, &str)] = &[
    ("点", "点"),
    ("點", "点"),
    ("个点", "个点"),
    ("個點", "個点"),
    ("小时", "小时"),
    ("小時", "小时"),
    ("个小时", "个小时"),
    ("個小時", "个小时"),
    ("个钟头", "个钟头"),
    ("個鐘頭", "个钟头"),
];
const MINUTE_WORDS: &[(&str, &str)] = &[("分", "分"), ("刻", "刻"), ("分钟", "分钟"), ("刻钟", "刻钟")];
const SECOND_WORDS: &[(&str, &str)] = &[("秒", "秒"), ("秒钟", "秒钟"), ("秒鐘", "秒钟")];

#[derive(Clone, Copy, PartialEq)]
enum K {
    H,
    M,
    S,
}

/// One component reading: (end, key, table value, word).
type Comp<'a> = (usize, K, &'a str, &'static str);

fn key(k: K) -> &'static str {
    match k {
        K::H => "hours",
        K::M => "minutes",
        K::S => "seconds",
    }
}

/// A 1-2 digit key table (`hour.tsv`, `minute.tsv`, `second.tsv`: each key once) as arrays: `one[d]` for the key
/// `"d"`, `two[10a + b]` for `"ab"`.
struct Keys {
    one: [Option<&'static str>; 10],
    two: [Option<&'static str>; 100],
}

fn keys(table: &'static Table) -> Keys {
    let mut k = Keys { one: [None; 10], two: [None; 100] };
    for row in table.rows() {
        let d: Vec<u32> = row.key.chars().map(|c| c.to_digit(10).expect("digit key")).collect();
        let slot = match *d.as_slice() {
            [a] => &mut k.one[a as usize],
            [a, b] => &mut k.two[(10 * a + b) as usize],
            _ => panic!("time key longer than two digits"),
        };
        assert!(slot.is_none(), "duplicate time key");
        *slot = Some(row.value.as_str());
    }
    k
}

static HOUR_KEYS: LazyLock<Keys> = LazyLock::new(|| keys(&HOUR));
static MINUTE_KEYS: LazyLock<Keys> = LazyLock::new(|| keys(&MINUTE));
static SECOND_KEYS: LazyLock<Keys> = LazyLock::new(|| keys(&SECOND));

/// The `(end, value)` matches of a key table at `p`, shorter key first (`Table::prefix_matches` order).
fn key_matches(k: &'static Keys, t: &[char], p: usize) -> [Option<(usize, &'static str)>; 2] {
    let digit = |i: usize| t.get(i).and_then(|c| c.to_digit(10));
    let Some(a) = digit(p) else { return [None, None] };
    let one = k.one[a as usize].map(|v| (p + 1, v));
    let two = digit(p + 1).and_then(|b| k.two[(10 * a + b) as usize]).map(|v| (p + 2, v));
    [one, two]
}

/// Every component reading of kind `k` at `p` (a 1- or 2-digit key followed by one of the kind's words).
#[inline]
fn comps(t: &[char], p: usize, k: K, mut f: impl FnMut(&Comp<'static>)) {
    if !t.get(p).is_some_and(char::is_ascii_digit) {
        return;
    }
    let (table, words): (&'static Keys, &'static [(&str, &'static str)]) = match k {
        K::H => (&HOUR_KEYS, HOUR_WORDS),
        K::M => (&MINUTE_KEYS, MINUTE_WORDS),
        K::S => (&SECOND_KEYS, SECOND_WORDS),
    };
    for (e, value) in key_matches(table, t, p).into_iter().flatten() {
        let Some(&w0) = t.get(e) else { continue };
        // the words' first chars
        if !matches!(w0, '点' | '點' | '个' | '個' | '小' | '分' | '刻' | '秒') {
            continue;
        }
        for (w, v) in words {
            if w.chars().next() == Some(w0)
                && let Some(n) = match_str(t, e, w)
            {
                f(&(e + n, k, value, v));
            }
        }
    }
}

/// A reading of the time body (its end and the data its fields are written from).
#[derive(Clone, Copy)]
enum R<'a> {
    /// `graph_colon`: hour, minute and optional second table values.
    Colon { end: usize, h: &'a str, m: &'a str, s: Option<&'a str> },
    /// `graph_clock_period`: 1-3 components.
    Period { end: usize, parts: [Option<Comp<'a>>; 3] },
    /// `graph_backcount`: `差`×b1, hour, `差`×b2, then 1-2 components.
    Back { end: usize, b1: usize, h: Comp<'a>, b2: usize, rest: [Option<Comp<'a>>; 2] },
    /// `graph_minute`: `M 差 S`.
    MinuteBack { end: usize, m: Comp<'a>, s: Comp<'a> },
}

impl R<'_> {
    fn end(&self) -> usize {
        match *self {
            R::Colon { end, .. } | R::Period { end, .. } | R::Back { end, .. } | R::MinuteBack { end, .. } => end,
        }
    }

    fn write(&self, out: &mut String) {
        match *self {
            R::Colon { h, m, s, .. } => {
                out.push_str("hours: \"");
                out.push_str(h);
                out.push_str("点\" minutes: \"");
                out.push_str(m);
                out.push_str("分\"");
                if let Some(s) = s {
                    out.push_str(" seconds: \"");
                    out.push_str(s);
                    out.push_str("秒\"");
                }
            }
            R::Period { parts, .. } => fields(&parts, out),
            R::Back { b1, h, b2, rest, .. } => {
                push_backs(out, b1);
                out.push(' ');
                write_comp(out, &h);
                out.push(' ');
                push_backs(out, b2);
                out.push(' ');
                fields(&rest, out);
            }
            R::MinuteBack { m, s, .. } => {
                write_comp(out, &m);
                out.push(' ');
                out.push_str(BACK);
                out.push(' ');
                write_comp(out, &s);
            }
        }
    }
}

fn write_comp(s: &mut String, c: &Comp<'_>) {
    s.push_str(key(c.1));
    s.push_str(": \"");
    s.push_str(c.2);
    s.push_str(c.3);
    s.push('"');
}

fn fields(parts: &[Option<Comp<'_>>], s: &mut String) {
    for (i, c) in parts.iter().flatten().enumerate() {
        if i > 0 {
            s.push(' ');
        }
        write_comp(s, c);
    }
}

fn is_colon(c: Option<&char>) -> bool {
    matches!(c, Some(':' | '：'))
}

/// `graph_colon`.
fn colon(t: &[char], p: usize, f: &mut impl FnMut(R<'static>)) {
    for (he, h) in key_matches(&HOUR_KEYS, t, p).into_iter().flatten() {
        if !is_colon(t.get(he)) {
            continue;
        }
        for (me, m) in key_matches(&MINUTE_KEYS, t, he + 1).into_iter().flatten() {
            if is_colon(t.get(me)) {
                for (se, s) in key_matches(&SECOND_KEYS, t, me + 1).into_iter().flatten() {
                    f(R::Colon { end: se, h, m, s: Some(s) });
                }
            }
            f(R::Colon { end: me, h, m, s: None });
        }
    }
}

/// `graph_clock_period`: `H | M | S | H M | H S | M S | H M S`.
fn clock_period(t: &[char], p: usize, f: &mut impl FnMut(R<'static>)) {
    let mut add = |a: &Comp<'static>, b: Option<&Comp<'static>>, c: Option<&Comp<'static>>| {
        let end = c.or(b).unwrap_or(a).0;
        f(R::Period { end, parts: [Some(*a), b.copied(), c.copied()] });
    };
    comps(t, p, K::H, |h| {
        comps(t, h.0, K::M, |m| {
            comps(t, m.0, K::S, |s| add(h, Some(m), Some(s)));
            add(h, Some(m), None);
        });
        comps(t, h.0, K::S, |s| add(h, Some(s), None));
        add(h, None, None);
    });
    comps(t, p, K::M, |m| {
        comps(t, m.0, K::S, |s| add(m, Some(s), None));
        add(m, None, None);
    });
    comps(t, p, K::S, |s| add(s, None, None));
}

const BACK: &str = "morphosyntactic_features: \"差\"";

/// The end of the run of `差` at `p`.
fn back_end(t: &[char], p: usize) -> usize {
    let mut e = p;
    while t.get(e) == Some(&'差') {
        e += 1;
    }
    e
}

fn push_backs(s: &mut String, n: usize) {
    for _ in 0..n {
        s.push_str(BACK);
    }
}

/// `graph_backcount` readings holding at least one `差`.
fn backcount(t: &[char], p: usize, f: &mut impl FnMut(R<'static>)) {
    // closure(backcount) before the hour: every count of the `差` run at `p`
    for b1e in p..=back_end(t, p) {
        comps(t, b1e, K::H, |h| {
            for b2e in h.0..=back_end(t, h.0) {
                if b1e == p && b2e == h.0 {
                    continue;
                }
                let (b1, b2, h) = (b1e - p, b2e - h.0, *h);
                comps(t, b2e, K::M, |m| {
                    comps(t, m.0, K::S, |s| f(R::Back { end: s.0, b1, h, b2, rest: [Some(*m), Some(*s)] }));
                    f(R::Back { end: m.0, b1, h, b2, rest: [Some(*m), None] });
                });
                comps(t, b2e, K::S, |s| f(R::Back { end: s.0, b1, h, b2, rest: [Some(*s), None] }));
            }
        });
    }
    // graph_minute: M 差 S
    comps(t, p, K::M, |m| {
        if t.get(m.0) == Some(&'差') {
            comps(t, m.0 + 1, K::S, |s| f(R::MinuteBack { end: s.0, m: *m, s: *s }));
        }
    });
}

/// Whether a digit-first time can continue its digit run with `f`: a clock word, `:`/`：` or `差`.
pub fn digit_follower(f: char) -> bool {
    matches!(f, '点' | '點' | '个' | '個' | '小' | '分' | '刻' | '秒' | ':' | '：' | '差')
}

/// One `time` token reading: the body and an optional AM/PM suffix value.
#[derive(Clone, Copy)]
pub struct Reading {
    body: R<'static>,
    suffix: Option<&'static str>,
}

impl Reading {
    pub fn write(&self, s: &mut String) {
        s.push_str("time { ");
        self.body.write(s);
        if let Some(v) = self.suffix {
            s.push(' ');
            field(s, "suffix", v);
        }
        s.push_str(" }");
    }
}

pub struct Time;

impl Gen for Time {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let c = t[start];
        if !(c.is_ascii_digit() || c == '差') {
            return;
        }
        // every branch reads a digit run followed by a clock word, `:`/`：` or `差`
        if c.is_ascii_digit() && !t.get(digit_end(t, start)).is_some_and(|&f| digit_follower(f)) {
            return;
        }
        let mut emit = |r: R<'static>| {
            let end = r.end();
            // AM/PM keys start with a space or a/A/p/P
            if matches!(t.get(end), Some(' ' | 'a' | 'A' | 'p' | 'P')) {
                for table in [&AM, &PM] {
                    for (row, e) in prefix_static(table, t, end) {
                        sink.keep(t, e, 0, 0, Recall::Time(Reading { body: r, suffix: Some(row.value.as_str()) }));
                    }
                }
            }
            sink.keep(t, end, 0, 0, Recall::Time(Reading { body: r, suffix: None }));
        };
        // `graph_colon` needs `:`/`：` right after the 1-2 digit hour
        if is_colon(t.get(start + 1)) || is_colon(t.get(start + 2)) {
            colon(t, start, &mut emit);
        }
        clock_period(t, start, &mut emit);
        // a back-count has a `差` at `start` or right after its first component (at most 2 digits + 3 word chars)
        if t[start..t.len().min(start + 6)].contains(&'差') {
            backcount(t, start, &mut emit);
        }
    }
}

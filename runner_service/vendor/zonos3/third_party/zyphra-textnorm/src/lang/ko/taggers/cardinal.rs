//! `ko/taggers/cardinal.py`.

use std::sync::LazyLock;

use crate::lang::ko::graph_utils::*;
use crate::lang::ko::number;
use crate::text::{Row, Table};

/// The ends of every digit prefix from `p` that `graph_num` accepts (`0`, or 1-17 digits without a leading
/// zero), shortest first. No allocation; read the value with [`push_reading`].
pub fn ends(t: &[char], p: usize) -> std::ops::Range<usize> {
    match t.get(p) {
        Some('0') => p + 1..p + 2,
        Some(c) if c.is_ascii_digit() => p + 1..run(t, p, is_digit).min(p + 17) + 1,
        _ => p..p,
    }
}

/// `graph_num` on `t[p..e]` (an end from [`ends`]), appended to `out`.
pub fn push_reading(t: &[char], p: usize, e: usize, out: &mut String) {
    number::read_chars_into(&t[p..e], out);
}

/// `pynini.closure(pynutil.delete("0"), 0, 1) + graph` (`strip0 + graph_cardinal`): every `(q, e)` whose value is
/// the reading of `t[q..e]`, in union order.
pub fn lz_ends(t: &[char], p: usize, mut f: impl FnMut(usize, usize)) {
    for e in ends(t, p) {
        f(p, e);
    }
    if t.get(p) == Some(&'0') {
        for e in ends(t, p + 1) {
            f(p + 1, e);
        }
    }
}

fn read(t: &[char], p: usize, e: usize) -> String {
    let mut s = String::with_capacity(6 * (e - p));
    push_reading(t, p, e, &mut s);
    s
}

/// `cardinal.graph` (`graph_num`): every digit prefix from `p` the FST reads, shortest first.
pub fn graph(t: &[char], p: usize) -> Outs {
    ends(t, p).map(|e| (e, read(t, p, e), 0)).collect()
}

/// `graph` restricted to the ends `keep` accepts (values built only for those).
pub fn graph_if(t: &[char], p: usize, keep: impl Fn(usize) -> bool) -> Outs {
    ends(t, p).filter(|&e| keep(e)).map(|e| (e, read(t, p, e), 0)).collect()
}

/// `native_1_to_39` (`native_ones | ordinal_tens | ordinal_tens_prefix + native_ones`).
static NATIVE_1_39: LazyLock<Table> = LazyLock::new(|| {
    let ones = Table::parse(data!("number/native_ones.tsv"));
    let tens = Table::parse(data!("ordinal/tens.tsv"));
    let prefix = Table::parse(data!("ordinal/tens_prefix.tsv"));
    let mut rows: Vec<Row> = ones.rows().to_vec();
    rows.extend(tens.rows().iter().cloned());
    for p in prefix.rows() {
        for o in ones.rows() {
            rows.push(Row {
                key: format!("{}{}", p.key, o.key),
                value: format!("{}{}", p.value, o.value),
                cost: p.cost + o.cost,
            });
        }
    }
    Table::from_rows(rows)
});

static COUNTER: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("number/counter_suffix.tsv")));

pub fn starts(c: char) -> bool {
    c.is_ascii_digit() || matches!(c, '-' | '+' | '(')
}

/// The full tagger output (`cardinal { … }`, already wrapped); the flag marks the counter branch.
pub fn tag(t: &[char], p: usize, f: TagSink<'_>) {
    if !t.get(p).copied().is_some_and(starts) {
        return;
    }
    // paren_negative: the 0-2 spaces are accepted (copied into the tag)
    if t[p] == '(' {
        for a in spaces02(t, p + 1) {
            for e in ends(t, a) {
                for b in spaces02(t, e) {
                    if t.get(b) == Some(&')') {
                        f(b + 1, 0, false, &|o: &mut String| {
                            o.push_str("cardinal { negative: \"true\" ");
                            o.extend(&t[p + 1..a]);
                            o.push_str("integer: \"");
                            push_reading(t, a, e, o);
                            o.push('"');
                            o.extend(&t[e..b]);
                            o.push_str(" }");
                        });
                    }
                }
            }
        }
    }
    // signed_integer
    let (q, neg) = match t[p] {
        '-' => (p + 1, true),
        '+' => (p + 1, false),
        _ => (p, false),
    };
    for e in ends(t, q) {
        f(e, 0, false, &|o: &mut String| {
            o.push_str("cardinal { ");
            if neg {
                o.push_str("negative: \"true\" ");
            }
            o.push_str("integer: \"");
            push_reading(t, q, e, o);
            o.push_str("\" }");
        });
    }
    // counter_case: a 1-39 key (one or two digits from `p`) followed by a counter suffix
    let de = run(t, p, is_digit);
    if de == p || de > p + 2 || COUNTER.prefix_matches(t, de).next().is_none() {
        return;
    }
    for (row, e) in NATIVE_1_39.prefix_matches(t, p) {
        for (c, e2) in COUNTER.prefix_matches(t, e) {
            f(e2, row.cost + c.cost, true, &|o: &mut String| {
                o.push_str("cardinal { integer: \"");
                o.push_str(&row.value);
                o.push_str("\" counter: \"");
                o.push_str(&c.key);
                o.push_str("\" }");
            });
        }
    }
}

/// `closure(" ", 0, 2)` ends.
fn spaces02(t: &[char], p: usize) -> impl Iterator<Item = usize> {
    let mut v = [p, usize::MAX, usize::MAX];
    if t.get(p) == Some(&' ') {
        v[1] = p + 1;
        if t.get(p + 1) == Some(&' ') {
            v[2] = p + 2;
        }
    }
    v.into_iter().filter(|&x| x != usize::MAX)
}

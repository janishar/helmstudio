//! `zh/taggers/measure.py`: `closure(cardinal { with_sign }  units: "…", 1)` | `closure(decimal { decimal } units:
//! "…", 1)`, units from `units_en.tsv` plus the file's own `m…` list; repetitions are concatenated.

use std::sync::LazyLock;

use super::decimal::{self, Dec};
use super::{Buf, Gen, ROOT, Sink, after_signed_digits, card, card_run, first_chars};
use crate::text::{Row, Table};

crate::tsv!(UNITS_EN, "zh/measure/units_en.tsv");

const UNITS: &[(&str, &str)] = &[
    ("ms", "毫秒"),
    ("m²", "平方米"),
    ("m2", "平方米"),
    ("m³", "立方米"),
    ("mbps", "兆比特每秒"),
    ("mg", "毫克"),
    ("mhz", "兆赫兹"),
    ("mi2", "平方英里"),
    ("mi²", "平方英里"),
    ("mi", "英里"),
    ("min", "分钟"),
    ("ml", "毫升"),
    ("mm2", "平方毫米"),
    ("mm²", "平方毫米"),
    ("mol", "摩尔"),
    ("mpa", "兆帕"),
    ("mph", "英里每小时"),
    ("mm", "毫米"),
    ("mv", "毫伏"),
    ("mw", "毫瓦"),
];

/// `units_en | units`, one row per distinct (key, value).
static ALL_UNITS: LazyLock<Table> = LazyLock::new(|| {
    let mut rows: Vec<Row> = UNITS_EN.rows().to_vec();
    for (k, v) in UNITS {
        if !rows.iter().any(|r| r.key == *k && r.value == *v) {
            rows.push(Row { key: k.to_string(), value: v.to_string(), cost: 0 });
        }
    }
    Table::from_rows(rows)
});

/// The first chars of [`ALL_UNITS`]' keys.
static UNIT_FIRST: LazyLock<Vec<char>> = LazyLock::new(|| first_chars(&ALL_UNITS));

/// Whether a unit key starts with `c`.
pub fn unit_first(c: char) -> bool {
    UNIT_FIRST.binary_search(&c).is_ok()
}

/// One number of a repetition: `cardinal.with_sign` (sign, digits `t[q..e]`) or a decimal reading.
#[derive(Clone, Copy)]
enum Num {
    Card { neg: bool, q: usize, e: usize },
    Dec(Dec),
}

impl Default for Num {
    fn default() -> Num {
        Num::Card { neg: false, q: 0, e: 0 }
    }
}

/// `cardinal.with_sign` at `p` when a unit can follow (units never start with a digit, so only the whole run).
fn with_sign(t: &[char], p: usize) -> Option<Num> {
    let (neg, q) = match t.get(p) {
        Some('-' | '负') => (true, p + 1),
        _ => (false, p),
    };
    let e = card_run(t, q)?;
    ALL_UNITS.prefix_matches(t, e).next()?;
    Some(Num::Card { neg, q, e })
}

/// A chain node: one `number units` repetition after its parent's.
#[derive(Clone, Copy)]
struct Node<'a> {
    parent: u32,
    num: Num,
    unit: &'a str,
}

impl Default for Node<'_> {
    fn default() -> Self {
        Node { parent: ROOT, num: Num::default(), unit: "" }
    }
}

fn write(t: &[char], arena: &Buf<Node<'_>, 4>, i: u32, out: &mut String) {
    let n = arena.get(i as usize);
    if n.parent != ROOT {
        write(t, arena, n.parent, out);
    }
    match n.num {
        Num::Card { neg, q, e } => {
            out.push_str("cardinal { ");
            if neg {
                out.push_str("negative: \"-\" ");
            }
            out.push_str("integer: \"");
            card(t, q, e, out);
            out.push_str("\" }  units: \"");
        }
        Num::Dec(d) => {
            out.push_str("decimal { ");
            d.write(t, out);
            out.push_str(" } units: \"");
        }
    }
    out.push_str(n.unit);
    out.push('"');
}

pub struct Measure;

impl Gen for Measure {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        // a unit (cardinal branch) or the decimal point follows the first digit run
        match after_signed_digits(t, start) {
            Some('.') => {}
            Some(c) if unit_first(c) => {}
            _ => return,
        }
        for decimal_branch in [false, true] {
            let mut arena: Buf<Node, 4> = Buf::new();
            let mut frontier: Buf<(usize, u32), 8> = Buf::new();
            frontier.push((start, ROOT));
            while let Some((pos, parent)) = frontier.pop() {
                let mut nums: Buf<Num, 4> = Buf::new();
                if decimal_branch {
                    decimal::decimal(t, pos, |d| {
                        nums.push(Num::Dec(d));
                    });
                } else if let Some(n) = with_sign(t, pos) {
                    nums.push(n);
                }
                for k in 0..nums.len() {
                    let num = nums.get(k);
                    let ne = match num {
                        Num::Card { e, .. } => e,
                        Num::Dec(d) => d.end,
                    };
                    for (row, ue) in ALL_UNITS.prefix_matches(t, ne) {
                        let i = arena.push(Node { parent, num, unit: row.value.as_str() }) as u32;
                        sink.push(ue, 0, 0, "measure", |s| write(t, &arena, i, s));
                        frontier.push((ue, i));
                    }
                }
            }
        }
    }
}

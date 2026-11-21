//! `ko/taggers/ordinal.py`.

use std::sync::LazyLock;

use crate::lang::ko::graph_utils::*;
use crate::lang::ko::taggers::cardinal;
use crate::text::{Row, Table, match_str};

/// `graph_exceptions | graph_digit | graph_zero | graph_tens | graph_11_to_39` (all keys are digit strings).
static ORD_1_39: LazyLock<Table> = LazyLock::new(|| {
    let digit = Table::parse(data!("ordinal/digit.tsv"));
    let prefix = Table::parse(data!("ordinal/tens_prefix.tsv"));
    let mut rows: Vec<Row> = Table::parse(data!("ordinal/exceptions.tsv")).rows().to_vec();
    rows.extend(digit.rows().iter().cloned());
    rows.extend(Table::parse(data!("number/zero.tsv")).rows().iter().cloned());
    rows.extend(Table::parse(data!("ordinal/tens.tsv")).rows().iter().cloned());
    for p in prefix.rows() {
        for o in digit.rows() {
            rows.push(Row {
                key: format!("{}{}", p.key, o.key),
                value: format!("{}{}", p.value, o.value),
                cost: p.cost + o.cost,
            });
        }
    }
    Table::from_rows(rows)
});

const JOSA_SINGLE: [&str; 9] = ["가", "이", "은", "는", "를", "을", "로", "도", "다"];
const JOSA_MULTI: [&str; 2] = ["부터", "까지"];

pub fn tag(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    if !t.get(p).is_some_and(|c| c.is_ascii_digit()) || match_str(t, run(t, p, is_digit), "번째").is_none() {
        return r;
    }
    let mut bases = Outs::new();
    for (row, e) in ORD_1_39.prefix_matches(t, p) {
        add(&mut bases, e, row.value.clone(), row.cost);
    }
    for (e, s, c) in cardinal::graph_if(t, p, |e| match_str(t, e, "번째").is_some()) {
        let n = e - p;
        if (n == 2 && ('4'..='9').contains(&t[p])) || n >= 3 {
            add(&mut bases, e, s, c);
        }
    }
    for (e, s, c) in bases {
        let Some(k) = match_str(t, e, "번째") else { continue };
        let e = e + k;
        let mut ends: Vec<(usize, String)> = vec![(e, String::new())];
        for j in JOSA_SINGLE {
            if let Some(k) = match_str(t, e, j) {
                ends.push((e + k, j.to_string()));
            }
        }
        let singles = ends.clone();
        for (e1, s1) in singles {
            for m in JOSA_MULTI {
                if let Some(k) = match_str(t, e1, m) {
                    ends.push((e1 + k, format!("{s1}{m}")));
                }
            }
        }
        for (e2, j) in ends {
            add(&mut r, e2, format!("integer: \"{s}번째{j}\""), c);
        }
    }
    r
}

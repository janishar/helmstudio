//! `de/taggers/money.py`: `MoneyFst`.
//!
//! `currency.tsv` maps `$` to `dollar` and `us dollar` and `dm` to `d-mark` and `deutsche mark` at equal cost; the
//! oracle shows `dollar` and `d-mark` (the first row), so only the first row of a key is emitted.

use std::sync::LazyLock;

use super::cardinal::{WEIGHT, digit_end, graph, graph_cost, two_digit, two_digit_cost};
use super::decimal;
use crate::lang::de::{Sink, Spans};
use crate::text::{Cost, Table, cost, space_to_nbsp};

crate::tsv!(CURRENCY, "de/money/currency.tsv");
crate::tsv!(MIN_SINGULAR, "de/money/currency_minor_singular.tsv");
crate::tsv!(MIN_PLURAL, "de/money/currency_minor_plural.tsv");

/// First row per key.
static MAJ: LazyLock<Table> = LazyLock::new(|| {
    let mut seen: Vec<&str> = Vec::new();
    let mut rows = Vec::new();
    for r in CURRENCY.rows() {
        if !seen.contains(&r.key.as_str()) {
            seen.push(&r.key);
            rows.push((r.key.clone(), space_to_nbsp(&r.value)));
        }
    }
    Table::from_pairs(rows)
});

const NO_MINOR_W: Cost = cost(0.0001);

fn first<'a>(t: &'a Table, k: &str) -> Option<&'a str> {
    t.rows().iter().find(|r| r.key == k).map(|r| r.value.as_str())
}

/// The first chars of the currency keys (sorted).
static FIRST: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = MAJ.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
});

/// Whether a currency key matches at `s` (every money span starts with one).
pub fn currency_at(text: &[char], s: usize) -> bool {
    FIRST.binary_search(&text[s]).is_ok() && MAJ.prefix_matches(text, s).any(|(_, e)| e > s)
}

/// `two_digits_fractional_part` on the fractional digit run: the 2-digit form (no output when not accepted).
fn two_digits_fractional(d: &[char]) -> Option<[char; 2]> {
    // closure(DIGIT) + (DIGIT - 0) + delete(0*): the last nonzero digit ends the kept part
    let last_nz = d.iter().rposition(|&c| c != '0')?;
    let kept = &d[..=last_nz];
    match kept {
        ['0', b] => Some(['0', *b]),
        [a] => Some([*a, '0']),
        [a, b] if *a != '0' => Some([*a, *b]),
        _ => None,
    }
}

pub struct Money;

/// `graph_integer_only` on a digit run: `ein` for `1`, else the cardinal graph (cost only).
fn int_cost(d: &[char]) -> Option<Cost> {
    if d == ['1'] { Some(0) } else { graph_cost(d) }
}

fn int_words(d: &[char]) -> String {
    if d == ['1'] { "ein".to_string() } else { graph(d).expect("money integer").0 }
}

/// The fractional reading of `text[a..e]` (`two_digits_fractional_part`): words, cost, singular minor unit.
fn fractional(d: &[char]) -> Option<([char; 2], Cost, bool)> {
    let two = two_digits_fractional(d)?;
    if two == ['0', '1'] {
        return Some((two, 0, true));
    }
    let t = if two[0] == '0' { &two[1..] } else { &two[..] };
    Some((two, two_digit_cost(t)?, false))
}

fn fractional_words(two: [char; 2]) -> String {
    if two == ['0', '1'] {
        return "ein".to_string();
    }
    let t = if two[0] == '0' { &two[1..] } else { &two[..] };
    two_digit(t).expect("money fraction").0
}

impl Spans for Money {
    fn may_start(&self, c: char) -> bool {
        FIRST.binary_search(&c).is_ok()
    }

    fn spans(&self, text: &[char], start: usize, out: &mut Sink) {
        if FIRST.binary_search(&text[start]).is_err() {
            return;
        }
        for (row, ps) in MAJ.prefix_matches(text, start) {
            if ps == start {
                continue;
            }
            let maj = row.value.as_str();
            let sym = row.key.as_str();
            let ie = digit_end(text, ps);
            if ie == ps {
                continue;
            }
            let ints = &text[ps..ie];
            let simple =
                |k: usize| format!("money {{ currency_maj: \"{maj}\" integer_part: \"{}\" }}", int_words(&text[ps..k]));
            // graph_integer_only (+ optional ",0+")
            let integer = int_cost(ints);
            // graph_integer_only on every digit prefix (tokens may touch in the `De` shape)
            for k in ps + 1..ie {
                if let Some(ic) = int_cost(&text[ps..k]) {
                    out.push(k, WEIGHT + ic, || simple(k));
                }
            }
            if let Some(ic) = integer {
                out.push(ie, WEIGHT + ic, || simple(ie));
                if text.get(ie) == Some(&',') {
                    let mut z = ie + 1;
                    while text.get(z) == Some(&'0') {
                        z += 1;
                        out.push(z, WEIGHT + ic, || simple(ie));
                    }
                }
            }
            // graph_decimal: maj + decimal.fst on (decimal_delete_last_zeros | decimal_with_quantity)
            decimal::fst(text, ps, &|_| true, &mut |de, dc, d| {
                let span = &text[ps..de];
                let with_q = d.qty.is_some();
                // decimal_delete_last_zeros: digits "," ≥2 digits, a nonzero digit, then deleted zeros
                let three = !with_q && d.frac.is_some_and(|(a, b)| b - a >= 3) && span[span.len() - 1] != '0';
                if !(with_q || three) {
                    return;
                }
                let tag = || {
                    let mut t = format!("money {{ currency_maj: \"{maj}\" decimal {{ ");
                    d.fields(text, &mut t);
                    t.push_str(" } }");
                    t
                };
                if three {
                    let mut z = de;
                    while text.get(z) == Some(&'0') {
                        z += 1;
                        out.push(z, WEIGHT + dc, tag);
                    }
                }
                out.push(de, WEIGHT + dc, tag);
            });
            // decimal_graph_with_minor: symbol int "," fractional [minor]
            if text.get(ie) != Some(&',') {
                continue;
            }
            let fe = digit_end(text, ie + 1);
            let Some(min_pl) = first(&MIN_PLURAL, sym) else {
                // without a minor currency only the 0.0001 branch exists
                if let Some(ic) = integer {
                    if ints != ['0'] {
                        no_minor(text, ps, ie, fe, ic, maj, out);
                    }
                }
                continue;
            };
            let min_sg = first(&MIN_SINGULAR, sym).unwrap_or(min_pl);
            for e in ie + 2..=fe {
                let Some((two, fc, singular)) = fractional(&text[ie + 1..e]) else { continue };
                let minor = if singular { min_sg } else { min_pl };
                if ints == ['0'] {
                    out.push(e, WEIGHT + fc, || {
                        format!(
                            "money {{ fractional_part: \"{}\" currency_min: \"{minor}\" preserve_order: true }}",
                            fractional_words(two)
                        )
                    });
                }
                if let Some(ic) = integer {
                    if ints != ['0'] {
                        out.push(e, WEIGHT + ic + fc, || {
                            format!(
                                "money {{ integer_part: \"{}\" currency_maj: \"{maj}\" fractional_part: \"{}\"  currency_min: \"{minor}\" preserve_order: true }}",
                                int_words(ints),
                                fractional_words(two)
                            )
                        });
                    }
                }
            }
            if let Some(ic) = integer {
                if ints != ['0'] {
                    no_minor(text, ps, ie, fe, ic, maj, out);
                }
            }
        }
    }
}

/// The 0.0001 branch: `integer_plus_maj "," fractional_part` without a minor unit.
fn no_minor(text: &[char], ps: usize, ie: usize, fe: usize, ic: Cost, maj: &str, out: &mut Sink) {
    for e in ie + 2..=fe {
        let Some(two) = two_digits_fractional(&text[ie + 1..e]) else { continue };
        let t = if two[0] == '0' { &two[1..] } else { &two[..] };
        let Some(fc) = two_digit_cost(t) else { continue };
        out.push(e, WEIGHT + ic + fc + NO_MINOR_W, || {
            format!(
                "money {{ integer_part: \"{}\" currency_maj: \"{maj}\" fractional_part: \"{}\" preserve_order: true }}",
                int_words(&text[ps..ie]),
                two_digit(t).expect("money fraction").0
            )
        });
    }
}

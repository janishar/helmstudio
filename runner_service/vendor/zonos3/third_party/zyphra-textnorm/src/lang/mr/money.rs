//! Marathi currency amounts, minor denominations, quantities and rates.
use super::{numbers, numeric};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
use std::sync::LazyLock;

const MAJ: &str = include_str!("../../../data/mr/money/currency.tsv");
const SING: &str = include_str!("../../../data/mr/money/currency_singular.tsv");
const MIN: &str = include_str!("../../../data/mr/money/major_minor_currencies.tsv");
const QTY: &str = include_str!("../../../data/mr/numbers/thousands.tsv");

const PER: &[(&str, &str)] = &[
    ("/तास", "प्रति तास"),
    ("/h", "प्रति तास"),
    ("/hr", "प्रति तास"),
    ("/मिनिट", "प्रति मिनिट"),
    ("/min", "प्रति मिनिट"),
    ("/सेकंद", "प्रति सेकंद"),
    ("/s", "प्रति सेकंद"),
    ("/दिवस", "प्रति दिवस"),
    ("/d", "प्रति दिवस"),
    ("/आठवडा", "प्रति आठवडा"),
    ("/wk", "प्रति आठवडा"),
    ("/महिना", "प्रति महिना"),
    ("/mo", "प्रति महिना"),
    ("/वर्ष", "प्रति वर्ष"),
    ("/yr", "प्रति वर्ष"),
    ("/kg", "प्रति किलोग्रॅम"),
    ("/किग्रॅ", "प्रति किलोग्रॅम"),
    ("/g", "प्रति ग्रॅम"),
    ("/t", "प्रति टन"),
    ("/l", "प्रति लीटर"),
    ("/L", "प्रति लीटर"),
    ("/ml", "प्रति मिलीलीटर"),
    ("/mL", "प्रति मिलीलीटर"),
    ("/m", "प्रति मीटर"),
    ("/km", "प्रति किलोमीटर"),
    ("/m²", "प्रति चौरस मीटर"),
    ("/m2", "प्रति चौरस मीटर"),
    ("/m³", "प्रति घन मीटर"),
    ("/m3", "प्रति घन मीटर"),
    ("/ft²", "प्रति चौरस फूट"),
    ("/ac", "प्रति एकर"),
    ("/kWh", "प्रति किलोवॉट तास"),
];

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

static INITIALS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut chars: Vec<_> = rows(MAJ).chain(rows(SING)).filter_map(|(key, _)| key.chars().next()).collect();
    chars.sort_unstable();
    chars.dedup();
    chars
});

static MIN_MAP: LazyLock<std::collections::HashMap<&'static str, &'static str>> = LazyLock::new(|| {
    let mut out = std::collections::HashMap::new();
    for (k, v) in rows(MIN) {
        out.insert(k, v);
    }
    out
});

struct CurrencyIndex {
    starts: std::collections::HashMap<char, Vec<(&'static str, &'static str)>>,
    ends: std::collections::HashMap<char, Vec<(&'static str, &'static str)>>,
}

impl CurrencyIndex {
    fn new(data: &'static str) -> Self {
        let mut index = Self { starts: Default::default(), ends: Default::default() };
        for (key, value) in rows(data) {
            if let Some(c) = key.chars().next() {
                index.starts.entry(c).or_default().push((key, value));
            }
            if let Some(c) = key.chars().last() {
                index.ends.entry(c).or_default().push((key, value));
            }
        }
        index
    }

    fn starts(&self, s: &str) -> &[(&'static str, &'static str)] {
        s.chars().next().and_then(|c| self.starts.get(&c)).map(Vec::as_slice).unwrap_or(&[])
    }

    fn ends(&self, s: &str) -> &[(&'static str, &'static str)] {
        s.chars().last().and_then(|c| self.ends.get(&c)).map(Vec::as_slice).unwrap_or(&[])
    }
}

static MAJOR_INDEX: LazyLock<CurrencyIndex> = LazyLock::new(|| CurrencyIndex::new(MAJ));
static SINGULAR_INDEX: LazyLock<CurrencyIndex> = LazyLock::new(|| CurrencyIndex::new(SING));

pub(crate) fn possible_start(c: char) -> bool {
    if numbers::digit(c).is_some() || matches!(c, '-' | '−' | '~' | '±') {
        return true;
    }
    INITIALS.binary_search(&c).is_ok()
}

static PREFIXES: LazyLock<super::address::KeywordIndex> = LazyLock::new(|| {
    let mut index = super::address::KeywordIndex::new();
    for (key, _) in rows(MAJ).chain(rows(SING)) {
        index.insert(key);
    }
    index
});

pub(crate) fn has_currency_prefix(s: &str) -> bool {
    let trimmed = s.trim_start_matches(|c| matches!(c, '-' | '−' | '~' | '±' | ' '));
    trimmed.chars().next().is_some_and(|c| PREFIXES.get(c).iter().any(|key| trimmed.starts_with(*key)))
}

pub(crate) fn contains_currency(s: &str) -> bool {
    s.char_indices().any(|(i, c)| PREFIXES.get(c).iter().any(|key| s[i..].starts_with(*key)))
}

pub(crate) fn possible_char(c: char) -> bool {
    static CHARS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
        PER.iter()
            .map(|(key, _)| *key)
            .chain(rows(MAJ).chain(rows(SING)).chain(rows(QTY)).map(|(key, _)| key))
            .flat_map(str::chars)
            .collect()
    });
    numbers::digit(c).is_some() || numeric::structural_char(c) || possible_start(c) || CHARS.contains(&c)
}

pub(crate) fn possible_end(c: char) -> bool {
    static ENDS: LazyLock<std::collections::HashSet<char>> = LazyLock::new(|| {
        PER.iter()
            .map(|(key, _)| *key)
            .chain(rows(MAJ).chain(rows(SING)).chain(rows(QTY)).map(|(key, _)| key))
            .filter_map(|key| key.chars().last())
            .collect()
    });
    numbers::digit(c).is_some() || ENDS.contains(&c)
}

pub(crate) fn matching_suffix(s: &str, _c: char) -> bool {
    static SUFFIXES: LazyLock<std::collections::HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
        let mut index: std::collections::HashMap<char, Vec<&'static str>> = Default::default();
        for (key, _) in rows(MAJ).chain(rows(SING)).chain(rows(QTY)) {
            if let Some(c) = key.chars().last() {
                index.entry(c).or_default().push(key);
            }
        }
        index
    });
    let matches = |text: &str| {
        text.chars()
            .last()
            .and_then(|c| SUFFIXES.get(&c))
            .is_some_and(|keys| keys.iter().any(|key| text.ends_with(*key)))
    };
    for (rate, _) in PER {
        if let Some(prefix) = s.strip_suffix(rate) {
            let p = prefix.trim_end();
            if p.chars().last().is_some_and(|ch| numbers::digit(ch).is_some()) || matches(p) {
                return true;
            }
        }
    }
    matches(s) || s.chars().last().is_some_and(|ch| numbers::digit(ch).is_some())
}

fn emit(out: &mut Vec<Candidate>, end: usize, fields: impl FnOnce() -> String, c: Cost) {
    out.push(Candidate {
        end,
        cost: cost(1.1) + c,
        rank: 7,
        tag: super::lazy::tag(|| format!("money {{ {} }}", fields())),
    });
}

fn sign(s: &str) -> (&str, &'static str) {
    if let Some(x) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        (x, "negative: \"true\" ")
    } else if let Some(x) = s.strip_prefix('~') {
        (x.strip_prefix(' ').unwrap_or(x), "negative: \"सुमारे\" ")
    } else if let Some(x) = s.strip_prefix('±') {
        (x.strip_prefix(' ').unwrap_or(x), "negative: \"अधिक किंवा उणे\" ")
    } else {
        (s, "")
    }
}

fn amount(s: &str) -> Vec<(String, Cost)> {
    let mut out = numeric::cardinal(s);
    if let Some((a, b)) = s.split_once('.') {
        if let Some(f) = numbers::digitwise(b) {
            for (n, c) in numeric::cardinal(a) {
                out.push((super::lazy::tag(|| format!("{n} दशांश {f}")), c));
            }
        }
    }
    out
}

fn quantity_amount(s: &str) -> Vec<(String, Cost)> {
    let mut out = numbers::graph(s);
    if let Some((a, b)) = s.split_once('.') {
        if let Some(f) = numbers::digitwise(b) {
            for (n, c) in numbers::graph(a) {
                out.push((super::lazy::tag(|| format!("{n} दशांश {f}")), c));
            }
        }
    }
    out
}

fn body(
    n: &str,
    major: &str,
    singular: bool,
    prefix: bool,
    sign: &str,
    per: Option<&str>,
    end: usize,
    out: &mut Vec<Candidate>,
) {
    let mut variants = vec![(n, None)];
    for (q, v) in rows(QTY).chain([
        ("k", "हजार"),
        ("K", "हजार"),
        ("M", "दशलक्ष"),
        ("m", "दशलक्ष"),
        ("B", "अब्ज"),
        ("bn", "अब्ज"),
        ("T", "खर्व"),
    ]) {
        if let Some(x) = n.strip_suffix(q) {
            variants.push((x.strip_suffix(' ').unwrap_or(x), Some(v)));
        }
    }

    let suffix = super::lazy::tag(|| per.map(|v| format!(" morphosyntactic_features: \"{v}\"")).unwrap_or_default());

    for (value, quantity) in variants {
        if let Some(q) = quantity {
            if singular {
                continue;
            }
            for (spoken, c) in quantity_amount(value) {
                emit(
                    out,
                    end,
                    || {
                        let word = format!("{spoken} {q}");
                        if prefix {
                            format!("{sign}currency_maj: \"{major}\" integer_part: \"{word}\"{suffix}")
                        } else {
                            format!("{sign}integer_part: \"{word}\" currency_maj: \"{major}\"{suffix}")
                        }
                    },
                    c + cost(-0.0005),
                );
            }
            continue;
        }

        let (i, f) = value.split_once('.').map_or((value, None), |(a, b)| (a, Some(b)));
        if singular && !matches!(i, "1" | "१") {
            continue;
        }

        for (integer, ic) in numeric::cardinal(i) {
            let base = || {
                if prefix {
                    format!("{sign}currency_maj: \"{major}\" integer_part: \"{integer}\"")
                } else {
                    format!("{sign}integer_part: \"{integer}\" currency_maj: \"{major}\"")
                }
            };
            let sw = if singular { cost(-0.001) } else { 0 };

            if f.is_none() || f.is_some_and(|x| !x.is_empty() && x.chars().all(|c| matches!(c, '0' | '०'))) {
                emit(out, end, || format!("{}{suffix}", base()), ic + sw);
            }

            if let Some(f) = f {
                let ascii: Option<String> = f
                    .chars()
                    .map(|c| {
                        if c.is_ascii_digit() {
                            Some(c)
                        } else if ('०'..='९').contains(&c) {
                            char::from_u32('0' as u32 + c as u32 - '०' as u32)
                        } else {
                            None
                        }
                    })
                    .collect();

                if let Some(a) = ascii {
                    let significant = a.trim_end_matches('0');
                    if !significant.is_empty() && significant.len() <= 2 {
                        let canonical =
                            if significant.len() == 1 { format!("{significant}0") } else { significant.to_owned() };
                        let canonical = canonical.trim_start_matches('0');
                        let canonical = if canonical.is_empty() { "0" } else { canonical };

                        if let Some(&minor) = MIN_MAP.get(major) {
                            for (v, c) in numeric::cardinal(canonical) {
                                emit(
                                    out,
                                    end,
                                    || {
                                        if prefix {
                                            format!(
                                                "{sign}currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{v}\" currency_min: \"{minor}\"{suffix}"
                                            )
                                        } else {
                                            format!(
                                                "{sign}integer_part: \"{integer}\" fractional_part: \"{v}\" currency_maj: \"{major}\" currency_min: \"{minor}\"{suffix}"
                                            )
                                        }
                                    },
                                    ic + c + sw,
                                );
                            }
                        }
                    }

                    if !singular && significant.len() >= 3 {
                        if let Some(v) = numbers::digitwise(f) {
                            emit(
                                out,
                                end,
                                || {
                                    if prefix {
                                        format!(
                                            "{sign}currency_maj: \"{major}\" integer_part: \"{integer}\" fractional_part: \"{v}\"{suffix}"
                                        )
                                    } else {
                                        format!(
                                            "{sign}integer_part: \"{integer}\" fractional_part: \"{v}\" currency_maj: \"{major}\"{suffix}"
                                        )
                                    }
                                },
                                ic + cost(-0.0005),
                            );
                        }
                    }
                }
            }
        }

        if singular {
            continue;
        }

        for sep in ['-', '–', '—'] {
            if let Some((a, b)) = value.split_once(sep) {
                let a = a.strip_suffix(' ').unwrap_or(a);
                let b = b.strip_prefix(' ').unwrap_or(b);
                for (x, cx) in amount(a) {
                    for (y, cy) in amount(b) {
                        emit(
                            out,
                            end,
                            || {
                                if prefix {
                                    format!("{sign}currency_maj: \"{major}\" integer_part: \"{x} ते {y}\"{suffix}")
                                } else {
                                    format!("{sign}integer_part: \"{x} ते {y}\" currency_maj: \"{major}\"{suffix}")
                                }
                            },
                            cx + cy + cost(-0.0005),
                        );
                    }
                }
            }
        }
    }
}

fn repeated(s: &str, sign: &str, rate: Option<&str>, end: usize, out: &mut Vec<Candidate>) {
    for &(sym, major) in MAJOR_INDEX.starts(s) {
        if let Some(rest) = s.strip_prefix(sym) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            for sep in ['-', '–', '—'] {
                if let Some((a, b)) = rest.split_once(sep) {
                    let a = a.strip_suffix(' ').unwrap_or(a);
                    let b = b.strip_prefix(' ').unwrap_or(b);
                    if let Some(b) = b.strip_prefix(sym) {
                        let b = b.strip_prefix(' ').unwrap_or(b);
                        for (x, cx) in amount(a) {
                            for (y, cy) in amount(b) {
                                let suffix = super::lazy::tag(|| {
                                    rate.map(|v| format!(" morphosyntactic_features: \"{v}\"")).unwrap_or_default()
                                });
                                emit(
                                    out,
                                    end,
                                    || format!("{sign}currency_maj: \"{major}\" integer_part: \"{x} ते {y}\"{suffix}"),
                                    cx + cy + cost(-0.0005),
                                );
                            }
                        }
                    }
                }
            }
        }
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (s, sgn) = sign(s);
    let mut rate = None;
    let mut base = s;

    for (k, v) in PER {
        if let Some(p) = s.strip_suffix(k) {
            rate = Some(*v);
            base = p.strip_suffix(' ').unwrap_or(p);
            break;
        }
    }

    repeated(base, sgn, rate, end, out);

    // Prefix currency
    for &(sym, major) in MAJOR_INDEX.starts(base) {
        if let Some(num) = base.strip_prefix(sym) {
            let num = num.strip_prefix(' ').unwrap_or(num);
            body(num, major, false, true, sgn, rate, end, out);
        }
    }

    for &(sym, major) in SINGULAR_INDEX.starts(base) {
        if let Some(num) = base.strip_prefix(sym) {
            let num = num.strip_prefix(' ').unwrap_or(num);
            body(num, major, true, true, sgn, rate, end, out);
        }
    }

    // Suffix currency
    for &(sym, major) in MAJOR_INDEX.ends(base) {
        if let Some(num) = base.strip_suffix(sym) {
            let num = num.strip_suffix(' ').unwrap_or(num);
            body(num, major, false, false, sgn, rate, end, out);
        }
    }

    for &(sym, major) in SINGULAR_INDEX.ends(base) {
        if let Some(num) = base.strip_suffix(sym) {
            let num = num.strip_suffix(' ').unwrap_or(num);
            body(num, major, true, false, sgn, rate, end, out);
        }
    }
}

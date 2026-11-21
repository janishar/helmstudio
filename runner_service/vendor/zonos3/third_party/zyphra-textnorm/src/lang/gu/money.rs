//! Currency amounts, minor denominations, quantities and rates.
use super::{numbers, numeric};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};
const MAJ: &str = include_str!("../../../data/gu/money/currency.tsv");
const SING: &str = include_str!("../../../data/gu/money/currency_singular.tsv");
const MIN: &str = include_str!("../../../data/gu/money/major_minor_currencies.tsv");
const QTY: &str = include_str!("../../../data/gu/numbers/thousands.tsv");
const PER: &[(&str, &str)] = &[
    ("/કલાક", "પ્રતિ કલાક"),
    ("/h", "પ્રતિ કલાક"),
    ("/hr", "પ્રતિ કલાક"),
    ("/મિનિટ", "પ્રતિ મિનિટ"),
    ("/min", "પ્રતિ મિનિટ"),
    ("/સેકન્ડ", "પ્રતિ સેકન્ડ"),
    ("/s", "પ્રતિ સેકન્ડ"),
    ("/દિવસ", "પ્રતિ દિવસ"),
    ("/d", "પ્રતિ દિવસ"),
    ("/અઠવાડિયું", "પ્રતિ અઠવાડિયું"),
    ("/wk", "પ્રતિ અઠવાડિયું"),
    ("/મહિનો", "પ્રતિ મહિનો"),
    ("/mo", "પ્રતિ મહિનો"),
    ("/વર્ષ", "પ્રતિ વર્ષ"),
    ("/yr", "પ્રતિ વર્ષ"),
    ("/kg", "પ્રતિ કિલોગ્રામ"),
    ("/કિગ્રા", "પ્રતિ કિલોગ્રામ"),
    ("/g", "પ્રતિ ગ્રામ"),
    ("/t", "પ્રતિ ટન"),
    ("/l", "પ્રતિ લિટર"),
    ("/L", "પ્રતિ લિટર"),
    ("/ml", "પ્રતિ મિલિલિટર"),
    ("/mL", "પ્રતિ મિલિલિટર"),
    ("/m", "પ્રતિ મીટર"),
    ("/km", "પ્રતિ કિલોમીટર"),
    ("/m²", "પ્રતિ ચોરસ મીટર"),
    ("/m2", "પ્રતિ ચોરસ મીટર"),
    ("/m³", "પ્રતિ ઘન મીટર"),
    ("/m3", "પ્રતિ ઘન મીટર"),
    ("/ft²", "પ્રતિ ચોરસ ફૂટ"),
    ("/ac", "પ્રતિ એકર"),
    ("/kWh", "પ્રતિ કિલોવૉટ કલાક"),
];
fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}
pub(crate) fn possible_start(c: char) -> bool {
    if numbers::digit(c).is_some() || matches!(c, '-' | '−' | '~' | '±') {
        return true;
    }
    static INITIALS: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
        let mut chars: Vec<_> = rows(MAJ).chain(rows(SING)).filter_map(|(key, _)| key.chars().next()).collect();
        chars.sort_unstable();
        chars.dedup();
        chars
    });
    INITIALS.binary_search(&c).is_ok()
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
        (x.strip_prefix(' ').unwrap_or(x), "negative: \"આશરે\" ")
    } else if let Some(x) = s.strip_prefix('±') {
        (x.strip_prefix(' ').unwrap_or(x), "negative: \"વત્તા કે ઓછા\" ")
    } else {
        (s, "")
    }
}
fn amount(s: &str) -> Vec<(String, Cost)> {
    let mut out = numeric::cardinal(s);
    if let Some((a, b)) = s.split_once('.') {
        if let Some(f) = numbers::digitwise(b) {
            for (n, c) in numeric::cardinal(a) {
                out.push((super::lazy::tag(|| format!("{n} દશાંશ {f}")), c));
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
                out.push((super::lazy::tag(|| format!("{n} દશાંશ {f}")), c));
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
        ("k", "હજાર"),
        ("K", "હજાર"),
        ("M", "દસ લાખ"),
        ("m", "દસ લાખ"),
        ("B", "અબજ"),
        ("bn", "અબજ"),
        ("T", "ખરબ"),
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
        if singular && !matches!(i, "1" | "૧") {
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
            if f.is_none() || f.is_some_and(|x| !x.is_empty() && x.chars().all(|c| matches!(c, '0' | '૦'))) {
                emit(out, end, || format!("{}{suffix}", base()), ic + sw);
            }
            if let Some(f) = f {
                let ascii: Option<String> = f
                    .chars()
                    .map(|c| {
                        if c.is_ascii_digit() {
                            Some(c)
                        } else if ('૦'..='૯').contains(&c) {
                            char::from_u32('0' as u32 + c as u32 - '૦' as u32)
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
                        if let Some((_, minor)) = rows(MIN).find(|(k, _)| *k == major) {
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
                                    format!("{sign}currency_maj: \"{major}\" integer_part: \"{x} થી {y}\"{suffix}")
                                } else {
                                    format!("{sign}integer_part: \"{x} થી {y}\" currency_maj: \"{major}\"{suffix}")
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
    for (sym, major) in rows(MAJ) {
        if let Some(rest) = s.strip_prefix(sym) {
            let rest = rest.strip_prefix(' ').unwrap_or(rest);
            for sep in ['-', '–', '—'] {
                if let Some((a, b)) = rest.split_once(sep) {
                    let a = a.strip_suffix(' ').unwrap_or(a);
                    let b = b.strip_prefix(' ').unwrap_or(b);
                    for (other, _) in rows(MAJ).filter(|(_, m)| *m == major) {
                        if let Some(b) = b.strip_prefix(other) {
                            let b = b.strip_prefix(' ').unwrap_or(b);
                            for (x, cx) in amount(a) {
                                for (y, cy) in amount(b) {
                                    emit(
                                        out,
                                        end,
                                        || {
                                            let p = rate
                                                .map(|v| format!(" morphosyntactic_features: \"{v}\""))
                                                .unwrap_or_default();
                                            format!("{sign}currency_maj: \"{major}\" integer_part: \"{x} થી {y}\"{p}")
                                        },
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
}
struct CurrencyIndex {
    rows: Vec<(&'static str, &'static str, bool)>,
    first: std::collections::HashMap<char, Vec<usize>>,
    last: std::collections::HashMap<char, Vec<usize>>,
}
static CURRENCY_INDEX: std::sync::LazyLock<CurrencyIndex> = std::sync::LazyLock::new(|| {
    let mut index = CurrencyIndex { rows: Vec::new(), first: Default::default(), last: Default::default() };
    for (table, singular) in [(MAJ, false), (SING, true)] {
        for (key, value) in rows(table) {
            let (Some(first), Some(last)) = (key.chars().next(), key.chars().last()) else { continue };
            let id = index.rows.len();
            index.rows.push((key, value, singular));
            index.first.entry(first).or_default().push(id);
            index.last.entry(last).or_default().push(id);
        }
    }
    index
});
pub(crate) fn has_currency_prefix(s: &str) -> bool {
    let s = sign(s).0;
    s.chars()
        .next()
        .and_then(|first| CURRENCY_INDEX.first.get(&first))
        .is_some_and(|ids| ids.iter().any(|&id| s.starts_with(CURRENCY_INDEX.rows[id].0)))
}
pub(crate) fn possible_char(c: char) -> bool {
    struct Alphabet {
        ascii: [bool; 128],
        gujarati: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static ALPHABET: std::sync::LazyLock<Alphabet> = std::sync::LazyLock::new(|| {
        let mut a = Alphabet { ascii: [false; 128], gujarati: [false; 128], other: Default::default() };
        let keys = rows(MAJ)
            .chain(rows(SING))
            .chain(rows(QTY))
            .map(|(key, _)| key)
            .chain(PER.iter().map(|(key, _)| *key))
            .chain(["kKMmBbnT", "., -−–—~±"]);
        for c in keys.flat_map(str::chars) {
            let n = c as usize;
            if n < 128 {
                a.ascii[n] = true;
            } else if (0x0a80..=0x0aff).contains(&n) {
                a.gujarati[n - 0x0a80] = true;
            } else {
                a.other.insert(c);
            }
        }
        a
    });
    if numbers::digit(c).is_some() {
        return true;
    }
    let n = c as usize;
    if n < 128 {
        ALPHABET.ascii[n]
    } else if (0x0a80..=0x0aff).contains(&n) {
        ALPHABET.gujarati[n - 0x0a80]
    } else {
        ALPHABET.other.contains(&c)
    }
}
type Rates = std::collections::HashMap<char, Vec<(&'static str, &'static str)>>;
static RATE_ENDINGS: std::sync::LazyLock<Rates> = std::sync::LazyLock::new(|| {
    let mut endings = Rates::new();
    for &(key, value) in PER {
        if let Some(last) = key.chars().last() {
            endings.entry(last).or_default().push((key, value));
        }
    }
    endings
});

pub(crate) fn matching_suffix(s: &str, last: char) -> bool {
    CURRENCY_INDEX.last.get(&last).is_some_and(|ids| ids.iter().any(|&id| s.ends_with(CURRENCY_INDEX.rows[id].0)))
        || RATE_ENDINGS.get(&last).is_some_and(|rates| rates.iter().any(|(key, _)| s.ends_with(*key)))
}

pub(crate) fn possible_end(c: char) -> bool {
    struct Endings {
        ascii: [bool; 128],
        gujarati: [bool; 128],
        other: std::collections::HashSet<char>,
    }
    static ENDINGS: std::sync::LazyLock<Endings> = std::sync::LazyLock::new(|| {
        let mut index = Endings { ascii: [false; 128], gujarati: [false; 128], other: Default::default() };
        for c in rows(MAJ)
            .chain(rows(SING))
            .map(|(key, _)| key)
            .chain(PER.iter().map(|(key, _)| *key))
            .filter_map(|key| key.chars().last())
        {
            let n = c as usize;
            if n < 128 {
                index.ascii[n] = true;
            } else if (0x0a80..=0x0aff).contains(&n) {
                index.gujarati[n - 0x0a80] = true;
            } else {
                index.other.insert(c);
            }
        }
        index
    });
    let n = c as usize;
    if n < 128 {
        ENDINGS.ascii[n]
    } else if (0x0a80..=0x0aff).contains(&n) {
        ENDINGS.gujarati[n - 0x0a80]
    } else {
        ENDINGS.other.contains(&c)
    }
}
fn classify_rate(s: &str, sign: &str, rate: Option<&str>, end: usize, out: &mut Vec<Candidate>) {
    let Some(first) = s.chars().next() else { return };
    let Some(last) = s.chars().last() else { return };
    let empty = &[][..];
    let leading = CURRENCY_INDEX.first.get(&first).map(Vec::as_slice).unwrap_or(empty);
    let trailing = CURRENCY_INDEX.last.get(&last).map(Vec::as_slice).unwrap_or(empty);
    if leading.is_empty() && trailing.is_empty() {
        return;
    }
    if !leading.is_empty() && s.contains(|c| matches!(c, '-' | '–' | '—')) {
        repeated(s, sign, rate, end, out);
    }
    let (mut a, mut b) = (0, 0);
    while a < leading.len() || b < trailing.len() {
        let left = leading.get(a).copied().unwrap_or(usize::MAX);
        let right = trailing.get(b).copied().unwrap_or(usize::MAX);
        let id = left.min(right);
        if left == id {
            a += 1;
        }
        if right == id {
            b += 1;
        }
        let (sym, major, singular) = CURRENCY_INDEX.rows[id];
        if let Some(n) = s.strip_prefix(sym) {
            body(n.strip_prefix(' ').unwrap_or(n), major, singular, true, sign, rate, end, out);
        }
        if let Some(n) = s.strip_suffix(sym) {
            body(n.strip_suffix(' ').unwrap_or(n), major, singular, false, sign, rate, end, out);
        }
    }
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if s.chars()
        .all(|c| numbers::digit(c).is_some() || matches!(c, '.' | ',' | ' ' | '-' | '−' | '–' | '—' | '~' | '±'))
    {
        return;
    }
    let (s, sign) = sign(s);
    classify_rate(s, sign, None, end, out);
    // RATE_ENDINGS is also used by the dispatcher's exact suffix prefilter.
    if let Some(rates) = s.chars().last().and_then(|last| RATE_ENDINGS.get(&last)) {
        for &(key, value) in rates {
            if let Some(prefix) = s.strip_suffix(key) {
                classify_rate(prefix.strip_suffix(' ').unwrap_or(prefix), sign, Some(value), end, out);
            }
        }
    }
}

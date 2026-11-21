use super::{
    lookup,
    number::{cardinal, digits, grouped},
    numeric,
};
use std::sync::LazyLock;
static ABBR: LazyLock<[Vec<(&'static str, &'static str)>; 256]> = LazyLock::new(|| {
    let mut rows: [Vec<(&'static str, &'static str)>; 256] = std::array::from_fn(|_| Vec::new());
    for (key, value) in
        include_str!("../../../data/ms/money/quantity_abbr.tsv").lines().filter_map(|l| l.split_once('\t'))
    {
        if let Some(&last) = key.as_bytes().last() {
            rows[last as usize].push((key, value));
        }
    }
    rows
});
fn abbr_rows(s: &str) -> impl Iterator<Item = (&'static str, &'static str)> {
    let rows = s.as_bytes().last().map(|&last| ABBR[last as usize].as_slice()).unwrap_or(&[]);
    rows.iter().copied()
}
type Currency = (&'static str, &'static str, bool, bool);
struct CurrencyIndex {
    rows: Vec<Currency>,
    first: [Vec<usize>; 256],
    last: [Vec<usize>; 256],
}
static CURRENCIES: LazyLock<CurrencyIndex> = LazyLock::new(|| {
    let mut index = CurrencyIndex {
        rows: Vec::new(),
        first: std::array::from_fn(|_| Vec::new()),
        last: std::array::from_fn(|_| Vec::new()),
    };
    for (key, value) in
        include_str!("../../../data/ms/money/currency.tsv").lines().filter_map(|line| line.split_once('\t'))
    {
        let Some(first) = key.bytes().next() else { continue };
        let Some(last) = key.bytes().last() else { continue };
        let i = index.rows.len();
        index.first[first as usize].push(i);
        index.last[last as usize].push(i);
        index.rows.push((key, value, ["RM", "RM.", "MYR"].contains(&key), key.chars().all(char::is_alphabetic)));
    }
    index
});
struct CurrencyMatches {
    first: &'static [usize],
    last: &'static [usize],
    a: usize,
    b: usize,
}
impl Iterator for CurrencyMatches {
    type Item = Currency;
    fn next(&mut self) -> Option<Currency> {
        let a = self.first.get(self.a).copied().unwrap_or(usize::MAX);
        let b = self.last.get(self.b).copied().unwrap_or(usize::MAX);
        let i = a.min(b);
        if i == usize::MAX {
            return None;
        }
        if a == i {
            self.a += 1
        }
        if b == i {
            self.b += 1
        }
        Some(CURRENCIES.rows[i])
    }
}
fn currency_matches(s: &str) -> CurrencyMatches {
    let index = &*CURRENCIES;
    let first = s.as_bytes().first().map(|c| index.first[*c as usize].as_slice()).unwrap_or(&[]);
    let last = s.as_bytes().last().map(|c| index.last[*c as usize].as_slice()).unwrap_or(&[]);
    CurrencyMatches { first, last, a: 0, b: 0 }
}
fn nbsp(s: &str) -> String {
    s.replace(' ', "\u{a0}")
}
fn trim_one(s: &str) -> &str {
    s.strip_prefix([' ', '\u{a0}', '\u{202f}']).unwrap_or(s)
}
pub(crate) fn possible_end(c: char) -> bool {
    static ENDS: LazyLock<([bool; 128], Vec<char>)> = LazyLock::new(|| {
        let mut ascii = [false; 128];
        let mut unicode = Vec::new();
        for c in [
            include_str!("../../../data/ms/measure/measurements.tsv"),
            include_str!("../../../data/ms/measure/denominators.tsv"),
        ]
        .into_iter()
        .flat_map(|table| {
            table.lines().filter_map(|line| line.split_once('\t').and_then(|(key, _)| key.chars().last()))
        }) {
            if c.is_ascii() { ascii[c as usize] = true } else { unicode.push(c) }
        }
        unicode.sort_unstable();
        unicode.dedup();
        (ascii, unicode)
    });
    if c.is_ascii() { ENDS.0[c as usize] } else { ENDS.1.binary_search(&c).is_ok() }
}
struct UnitNode {
    next: [usize; 256],
    row: Option<usize>,
}
struct UnitIndex {
    nodes: Vec<UnitNode>,
    rows: Vec<(&'static str, &'static str)>,
}
static UNITS: LazyLock<UnitIndex> = LazyLock::new(|| {
    let node = || UnitNode { next: [usize::MAX; 256], row: None };
    let mut index = UnitIndex { nodes: vec![node()], rows: Vec::new() };
    for (key, value) in
        include_str!("../../../data/ms/measure/measurements.tsv").lines().filter_map(|l| l.split_once('\t'))
    {
        let mut current = 0;
        for byte in key.bytes().rev() {
            let next = index.nodes[current].next[byte as usize];
            current = if next == usize::MAX {
                let next = index.nodes.len();
                index.nodes.push(node());
                index.nodes[current].next[byte as usize] = next;
                next
            } else {
                next
            };
        }
        if index.nodes[current].row.is_none() {
            index.nodes[current].row = Some(index.rows.len());
            index.rows.push((key, value));
        }
    }
    index
});
#[derive(Clone, Copy)]
struct UnitSplit {
    start: usize,
    row: usize,
    denominator: Option<&'static str>,
    spaced: bool,
}
fn unit_splits(s: &str, end: usize, denominator: Option<&'static str>, out: &mut Vec<UnitSplit>) {
    let index = &*UNITS;
    let mut node = 0;
    for (offset, &byte) in s.as_bytes()[..end].iter().rev().enumerate() {
        let next = index.nodes[node].next[byte as usize];
        if next == usize::MAX {
            break;
        }
        node = next;
        let Some(row) = index.nodes[node].row else { continue };
        let pos = end - offset - 1;
        if pos == 0 || !s.is_char_boundary(pos) {
            continue;
        }
        if let Some((i, c)) = s[..pos].char_indices().next_back() {
            if i > 0 && matches!(c, ' ' | '\u{a0}' | '\u{202f}') {
                out.push(UnitSplit { start: i, row, denominator, spaced: true });
            }
        }
        out.push(UnitSplit { start: pos, row, denominator, spaced: false });
    }
}
pub(crate) fn measure(s: &str) -> Vec<(String, f64)> {
    let mut splits = Vec::new();
    unit_splits(s, s.len(), None, &mut splits);
    if let Some((before, after)) = s.rsplit_once('/') {
        if let Some(denominator) = lookup(include_str!("../../../data/ms/measure/denominators.tsv"), after) {
            unit_splits(s, before.len(), Some(denominator), &mut splits);
        }
    }
    // The original graph enumerates numeric prefixes from shortest to longest.
    splits.sort_by_key(|item| item.start);
    let mut out = Vec::new();
    for split in splits {
        let num = &s[..split.start];
        let (key, value) = UNITS.rows[split.row];
        if !split.spaced && split.denominator.is_none() && ["s", "t", "A", "J", "N", "cd"].contains(&key) {
            continue;
        }
        let unit = if super::lazy::recognizing() {
            String::new()
        } else if let Some(denominator) = split.denominator {
            format!("{}\u{a0}per\u{a0}{}", nbsp(value), nbsp(denominator))
        } else {
            nbsp(value)
        };
        if super::lazy::recognizing() {
            let mut accept = |w| out.push((String::new(), w + 0.01));
            numeric::cardinal_recognize(num, &mut accept);
            if num.contains(['.', ',']) || num.as_bytes().last().is_some_and(u8::is_ascii_alphabetic) {
                numeric::decimal_recognize(num, &mut accept);
            }
            if num.contains(['/', '⁄']) || num.chars().any(|c| "½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c))
            {
                numeric::fraction_recognize(num, &mut accept);
            }
            continue;
        }
        for (tag, w) in
            numeric::cardinal_tags(num).into_iter().chain(numeric::decimal_tags(num)).chain(numeric::fraction_tags(num))
        {
            let reading = format!("measure {{ {tag} units: \"{unit}\" preserve_order: true }}");
            out.push((reading, w + 0.01));
        }
    }
    out
}
fn endpoint(s: &str) -> Option<String> {
    let mut bases = vec![(s, None)];
    for (k, v) in numeric::quantity_rows(s).chain(abbr_rows(s)) {
        if let Some(r) = s.strip_suffix(k) {
            bases.push((r.strip_suffix(' ').unwrap_or(r), Some(v)));
        }
    }
    for (k, v) in [("k", "ribu"), ("K", "ribu"), ("M", "juta"), ("B", "bilion"), ("T", "trilion")] {
        if let Some(r) = s.strip_suffix(k) {
            bases.push((r.strip_suffix(' ').unwrap_or(r), Some(v)));
        }
    }
    for (base, q) in bases {
        let value = grouped(base).or_else(|| {
            base.char_indices()
                .filter(|(_, c)| *c == '.' || *c == ',')
                .find_map(|(i, _)| Some(format!("{} perpuluhan {}", grouped(&base[..i])?, digits(&base[i + 1..])?)))
        });
        if let Some(mut value) = value {
            if let Some(q) = q {
                value.push(' ');
                value.push_str(q);
            }
            return Some(value);
        }
    }
    None
}
fn endpoint_recognize(s: &str) -> bool {
    fn number(s: &str) -> bool {
        grouped(s).is_some()
            || s.char_indices()
                .any(|(i, c)| (c == '.' || c == ',') && grouped(&s[..i]).is_some() && digits(&s[i + 1..]).is_some())
    }
    if number(s) {
        return true;
    }
    for (key, _) in numeric::quantity_rows(s).chain(abbr_rows(s)) {
        if let Some(base) = s.strip_suffix(key) {
            if number(base.strip_suffix(' ').unwrap_or(base)) {
                return true;
            }
        }
    }
    for key in ["k", "K", "M", "B", "T"] {
        if let Some(base) = s.strip_suffix(key) {
            if number(base.strip_suffix(' ').unwrap_or(base)) {
                return true;
            }
        }
    }
    false
}
/// Amount recognition records only whether the minor-only branch matched.
fn amount_recognize(s: &str, rm: bool, mut emit: impl FnMut(bool, f64)) {
    if grouped(s).is_some() {
        emit(false, -0.01);
    }
    if let Some(r) = s
        .strip_suffix(".00")
        .or_else(|| s.strip_suffix(",00"))
        .or_else(|| s.strip_suffix(".-"))
        .or_else(|| s.strip_suffix(",-"))
    {
        if grouped(r).is_some() {
            emit(false, -0.01);
        }
    }
    for (i, c) in s.char_indices() {
        if c != '.' && c != ',' {
            continue;
        }
        let (a, b) = (&s[..i], &s[i + 1..]);
        if grouped(a).is_some() {
            if b.len() == 2
                && b != "00"
                && b.bytes().all(|c| c.is_ascii_digit())
                && !a.starts_with('0')
                && cardinal(b.strip_prefix('0').unwrap_or(b)).is_some()
            {
                emit(false, 0.0);
            }
            if b.len() != 2 && digits(b).is_some() {
                emit(false, 0.0);
            }
        }
    }
    if s.as_bytes().last().is_some_and(u8::is_ascii_alphabetic) {
        let base = numeric::quantity_rows(s);
        let local = abbr_rows(s).filter(|_| rm);
        let intl = [("k", "ribu"), ("K", "ribu"), ("M", "juta"), ("B", "bilion"), ("T", "trilion")]
            .into_iter()
            .filter(|_| !rm);
        for (key, _) in base.chain(local).chain(intl) {
            if let Some(stem) = s.strip_suffix(key) {
                let stem = stem.strip_suffix(' ').unwrap_or(stem);
                if grouped(stem).is_some() {
                    emit(false, 0.0);
                }
                for (i, c) in stem.char_indices() {
                    if (c == '.' || c == ',') && grouped(&stem[..i]).is_some() && digits(&stem[i + 1..]).is_some() {
                        emit(false, 0.0);
                    }
                }
            }
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if endpoint_recognize(a) && endpoint_recognize(b) {
                emit(false, -0.01);
            }
        }
    }
    if let Some(x) = s.strip_prefix("0.").or_else(|| s.strip_prefix("0,")) {
        if x.len() == 2 && x != "00" && cardinal(x.strip_prefix('0').unwrap_or(x)).is_some() {
            emit(true, 0.0);
        }
    }
}
fn amount(s: &str, rm: bool) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(v) = grouped(s) {
        out.push((format!("integer_part: \"{v}\""), -0.01));
    }
    if let Some(r) = s
        .strip_suffix(".00")
        .or_else(|| s.strip_suffix(",00"))
        .or_else(|| s.strip_suffix(".-"))
        .or_else(|| s.strip_suffix(",-"))
    {
        if let Some(v) = grouped(r) {
            out.push((format!("integer_part: \"{v}\""), -0.01));
        }
    }
    for (i, c) in s.char_indices() {
        if c != '.' && c != ',' {
            continue;
        }
        let (a, b) = (&s[..i], &s[i + 1..]);
        if let Some(n) = grouped(a) {
            if b.len() == 2 && b != "00" && b.bytes().all(|c| c.is_ascii_digit()) && !a.starts_with('0') {
                if let Some(m) = cardinal(b.strip_prefix('0').unwrap_or(b)) {
                    out.push((format!("integer_part: \"{n}\" fractional_part: \"{m}\" currency_min: \"sen\""), 0.0));
                }
            }
            if b.len() != 2 {
                if let Some(d) = digits(b) {
                    out.push((format!("integer_part: \"{n}\" fractional_part: \"{d}\""), 0.0));
                }
            }
        }
    }
    let base_quantities = numeric::quantity_rows(s);
    let rm_quantities = abbr_rows(s).filter(|_| rm);
    let intl_quantities =
        [("k", "ribu"), ("K", "ribu"), ("M", "juta"), ("B", "bilion"), ("T", "trilion")].into_iter().filter(|_| !rm);
    for (k, q) in base_quantities.chain(rm_quantities).chain(intl_quantities) {
        if let Some(base) = s.strip_suffix(k) {
            let base = base.strip_suffix(' ').unwrap_or(base);
            if let Some(v) = grouped(base) {
                out.push((format!("integer_part: \"{v}\" quantity: \"{q}\""), 0.0));
            }
            for (i, c) in base.char_indices() {
                if c == '.' || c == ',' {
                    if let (Some(a), Some(b)) = (grouped(&base[..i]), digits(&base[i + 1..])) {
                        out.push((format!("integer_part: \"{a}\" fractional_part: \"{b}\" quantity: \"{q}\""), 0.0));
                    }
                }
            }
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (endpoint(a), endpoint(b)) {
                out.push((format!("integer_part: \"{a} hingga {b}\""), -0.01));
            }
        }
    }
    if let Some(x) = s.strip_prefix("0.").or_else(|| s.strip_prefix("0,")) {
        if x.len() == 2 && x != "00" {
            if let Some(n) = cardinal(x.strip_prefix('0').unwrap_or(x)) {
                out.push((format!("fractional_part: \"{n}\" currency_min: \"sen\""), 0.0));
            }
        }
    }
    out
}
pub(crate) fn money(s: &str) -> Vec<(String, f64)> {
    let (s, sign) = if let Some(v) = s.strip_prefix(['-', '−']) {
        (v, Some("negatif"))
    } else if let Some(v) = s.strip_prefix('~') {
        (trim_one(v), Some("sekitar"))
    } else if let Some(v) = s.strip_prefix('±') {
        (trim_one(v), Some("lebih kurang"))
    } else {
        (s, None)
    };
    let sign = sign.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default();
    let rates = [
        ("/h", "per jam"),
        ("/jam", "per jam"),
        ("/j", "per jam"),
        ("/hr", "per jam"),
        ("/minit", "per minit"),
        ("/min", "per minit"),
        ("/mnt", "per minit"),
        ("/menit", "per minit"),
        ("/saat", "per saat"),
        ("/s", "per saat"),
        ("/sec", "per saat"),
        ("/detik", "per saat"),
        ("/hari", "per hari"),
        ("/d", "per hari"),
        ("/minggu", "per minggu"),
        ("/wk", "per minggu"),
        ("/bulan", "per bulan"),
        ("/bln", "per bulan"),
        ("/mo", "per bulan"),
        ("/tahun", "per tahun"),
        ("/thn", "per tahun"),
        ("/yr", "per tahun"),
        ("/kg", "per kilogram"),
        ("/g", "per gram"),
        ("/gr", "per gram"),
        ("/t", "per tan"),
        ("/ton", "per tan"),
        ("/tan", "per tan"),
        ("/l", "per liter"),
        ("/L", "per liter"),
        ("/ml", "per mililiter"),
        ("/mL", "per mililiter"),
        ("/m", "per meter"),
        ("/km", "per kilometer"),
        ("/m²", "per meter persegi"),
        ("/m2", "per meter persegi"),
        ("/m³", "per meter padu"),
        ("/m3", "per meter padu"),
        ("/kWh", "per kilowatt-jam"),
        ("/orang", "per orang"),
        ("/malam", "per malam"),
        ("/unit", "per unit"),
    ];
    let variants = std::iter::once((s, String::new())).chain(
        rates.into_iter().take(if s.contains('/') { usize::MAX } else { 0 }).filter_map(|(key, val)| {
            let base = s.strip_suffix(key)?;
            let rate = if super::lazy::recognizing() {
                String::new()
            } else {
                format!(" morphosyntactic_features: \"{}\"", nbsp(val))
            };
            Some((base.strip_suffix([' ', '\u{a0}', '\u{202f}']).unwrap_or(base), rate))
        }),
    );
    let mut out = Vec::new();
    for (s, rate) in variants {
        for (key, val, rm, alpha) in currency_matches(s) {
            if !s.starts_with(key) && !s.ends_with(key) {
                continue;
            }
            if super::lazy::recognizing() {
                let tag = || if sign.is_empty() { String::new() } else { String::from("negative:") };
                if let Some(rest) = s.strip_prefix(key) {
                    amount_recognize(trim_one(rest), rm, |minor, w| {
                        if !minor || key != "¢" {
                            out.push((tag(), w));
                        }
                    });
                }
                if let Some(rest) = s.strip_suffix(key) {
                    let spaced = rest.ends_with([' ', '\u{a0}', '\u{202f}']);
                    if !alpha || spaced {
                        let rest = rest.strip_suffix([' ', '\u{a0}', '\u{202f}']).unwrap_or(rest);
                        amount_recognize(rest, rm, |minor, w| {
                            if !(minor && key == "¢") && !(!minor && rm && key != "MYR") {
                                out.push((tag(), w + 0.01));
                            }
                        });
                    }
                }
                continue;
            }
            let currency = nbsp(val);
            if let Some(rest) = s.strip_prefix(key) {
                for (a, w) in amount(trim_one(rest), rm) {
                    let minor = a.starts_with("fractional_part");
                    if minor && key == "¢" {
                        continue;
                    }
                    let body = if minor { a } else { format!("currency_maj: \"{currency}\" {a}") };
                    out.push((format!("money {{ {sign}{body}{rate} }}"), w));
                }
            }
            if let Some(rest) = s.strip_suffix(key) {
                let spaced = rest.ends_with([' ', '\u{a0}', '\u{202f}']);
                if alpha && !spaced {
                    continue;
                }
                let rest = rest.strip_suffix([' ', '\u{a0}', '\u{202f}']).unwrap_or(rest);
                for (a, w) in amount(rest, rm) {
                    let minor = a.starts_with("fractional_part");
                    if (minor && key == "¢") || (!minor && rm && key != "MYR") {
                        continue;
                    }
                    let body = if minor { a } else { format!("{a} currency_maj: \"{currency}\"") };
                    out.push((format!("money {{ {sign}{body}{rate} }}"), w + 0.01));
                }
            }
        }
    }
    out
}

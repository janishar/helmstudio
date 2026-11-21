use super::{
    number::{Integer, cardinal, digits, grouped},
    numeric,
};
use std::sync::LazyLock;

static ABBR: LazyLock<[Vec<(&'static str, &'static str)>; 256]> = LazyLock::new(|| {
    let mut rows: [Vec<(&'static str, &'static str)>; 256] = std::array::from_fn(|_| Vec::new());
    for (key, value) in
        include_str!("../../../data/id/money/quantity_abbr.tsv").lines().filter_map(|l| l.split_once('\t'))
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
        include_str!("../../../data/id/money/currency.tsv").lines().filter_map(|line| line.split_once('\t'))
    {
        let Some(first) = key.bytes().next() else { continue };
        let Some(last) = key.bytes().last() else { continue };
        let i = index.rows.len();
        index.first[first as usize].push(i);
        index.last[last as usize].push(i);
        index.rows.push((key, value, ["Rp", "Rp.", "IDR"].contains(&key), key.chars().all(char::is_alphabetic)));
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
            self.a += 1;
        }
        if b == i {
            self.b += 1;
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

// Every money amount starts with an ASCII digit. A successful prefix span
// therefore has the same sign/currency/first-digit prefix in the full suffix.
// Check it once per lattice start without collecting another UTF-8 string.
pub(crate) fn money_start_at(text: &[char], start: usize) -> bool {
    let mut at = start;
    match text.get(at) {
        Some('-' | '−') => at += 1,
        Some('~' | '±') => {
            at += 1;
            if text.get(at) == Some(&' ') {
                at += 1;
            }
        }
        _ => {}
    }
    let Some(&first) = text.get(at) else { return false };
    if first.is_ascii_digit() {
        return true;
    }
    let mut bytes = [0; 4];
    let byte = first.encode_utf8(&mut bytes).as_bytes()[0];
    CURRENCIES.first[byte as usize].iter().any(|&index| {
        let mut input = text[at..].iter().copied();
        if !CURRENCIES.rows[index].0.chars().all(|c| input.next() == Some(c)) {
            return false;
        }
        let mut next = input.next();
        if matches!(next, Some(' ' | '\u{a0}' | '\u{202f}')) {
            next = input.next();
        }
        next.is_some_and(|c| c.is_ascii_digit())
    })
}

fn money_start(s: &str) -> bool {
    let raw = s
        .strip_prefix('-')
        .or_else(|| s.strip_prefix('−'))
        .or_else(|| s.strip_prefix('~').map(|s| s.strip_prefix(' ').unwrap_or(s)))
        .or_else(|| s.strip_prefix('±').map(|s| s.strip_prefix(' ').unwrap_or(s)))
        .unwrap_or(s);
    let Some(&first) = raw.as_bytes().first() else { return false };
    first.is_ascii_digit()
        || CURRENCIES.first[first as usize].iter().any(|&index| {
            raw.strip_prefix(CURRENCIES.rows[index].0)
                .is_some_and(|rest| trim_one(rest).as_bytes().first().is_some_and(u8::is_ascii_digit))
        })
}

fn nbsp(s: &str) -> String {
    if super::lazy::recognizing() { String::new() } else { s.replace(' ', "\u{a0}") }
}

fn trim_one(s: &str) -> &str {
    s.strip_prefix([' ', '\u{a0}', '\u{202f}']).unwrap_or(s)
}

pub(crate) fn possible_end(c: char) -> bool {
    static ENDS: LazyLock<([bool; 128], Vec<char>)> = LazyLock::new(|| {
        let mut ascii = [false; 128];
        let mut unicode = Vec::new();
        for c in [
            include_str!("../../../data/id/measure/measurements.tsv"),
            include_str!("../../../data/id/measure/denominators.tsv"),
        ]
        .into_iter()
        .flat_map(|table| {
            table.lines().filter_map(|line| line.split_once('\t').and_then(|(key, _)| key.chars().last()))
        }) {
            if c.is_ascii() {
                ascii[c as usize] = true;
            } else {
                unicode.push(c);
            }
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

impl UnitIndex {
    fn new(rows: impl IntoIterator<Item = (&'static str, &'static str)>) -> Self {
        let node = || UnitNode { next: [usize::MAX; 256], row: None };
        let mut index = Self { nodes: vec![node()], rows: Vec::new() };
        for (key, value) in rows {
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
    }

    fn first_suffix(&self, text: &str) -> Option<(&'static str, &'static str)> {
        let mut current = 0;
        let mut first = None;
        for byte in text.bytes().rev() {
            let next = self.nodes[current].next[byte as usize];
            if next == usize::MAX {
                break;
            }
            current = next;
            if let Some(row) = self.nodes[current].row {
                if first.is_none_or(|old| row < old) {
                    first = Some(row);
                }
            }
        }
        first.map(|row| self.rows[row])
    }
}

static UNITS: LazyLock<UnitIndex> = LazyLock::new(|| {
    UnitIndex::new(
        include_str!("../../../data/id/measure/measurements.tsv").lines().filter_map(|line| line.split_once('\t')),
    )
});

static PER_UNIT_INDEX: LazyLock<UnitIndex> = LazyLock::new(|| UnitIndex::new(PER_UNITS.iter().copied()));

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

fn lookup_denominator(key: &str) -> Option<&'static str> {
    for line in include_str!("../../../data/id/measure/denominators.tsv").lines() {
        if let Some((k, v)) = line.split_once('\t') {
            if k == key {
                return Some(v.trim_end_matches('\r'));
            }
        }
    }
    None
}

thread_local! {
    static MEASURE_SPLITS: std::cell::RefCell<Vec<UnitSplit>> = const { std::cell::RefCell::new(Vec::new()) };
}

pub(crate) fn selected_grouped_measure(s: &str) -> Option<String> {
    MEASURE_SPLITS.with_borrow_mut(|splits| {
        splits.clear();
        unit_splits(s, s.len(), None, splits);
        if let Some((before, after)) = s.rsplit_once('/') {
            if let Some(denominator) = lookup_denominator(after) {
                unit_splits(s, before.len(), Some(denominator), splits);
            }
        }
        splits.sort_by_key(|item| item.start);
        for &split in splits.iter() {
            let (key, unit) = UNITS.rows[split.row];
            if !split.spaced && split.denominator.is_none() && ["s", "t", "A", "J", "N", "cd"].contains(&key) {
                continue;
            }
            let (number, negative) = numeric::sign(&s[..split.start]);
            // An earlier non-grouped witness must retain the general source
            // enumeration order; this fast path handles its first scalar only.
            let value = Integer::grouped(number)?;
            let mut tag = String::with_capacity(
                number.len() * 24 + unit.len() * 2 + split.denominator.map_or(0, |value| value.len() * 2) + 128,
            );
            tag.push_str("measure { cardinal { ");
            if let Some(negative) = negative {
                tag.push_str("negative: \"");
                tag.push_str(negative);
                tag.push_str("\" ");
            }
            tag.push_str("integer: \"");
            value.write(&mut tag);
            tag.push_str("\" } units: \"");
            tag.extend(unit.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
            if let Some(denominator) = split.denominator {
                tag.push_str("\u{a0}per\u{a0}");
                tag.extend(denominator.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
            }
            tag.push_str("\" preserve_order: true }");
            return Some(tag);
        }
        None
    })
}

pub(crate) fn measure(s: &str, mut emit: impl FnMut(String, f64)) {
    if !s.chars().next().is_some_and(|c| c.is_ascii_digit() || "-−~±½¼¾⅓⅔⅕⅖⅗⅘⅙⅚⅛⅜⅝⅞⅐⅑⅒".contains(c))
    {
        return;
    }
    MEASURE_SPLITS.with_borrow_mut(|splits| {
        splits.clear();
        unit_splits(s, s.len(), None, splits);
        if let Some((before, after)) = s.rsplit_once('/') {
            if let Some(denominator) = lookup_denominator(after) {
                unit_splits(s, before.len(), Some(denominator), splits);
            }
        }
        splits.sort_by_key(|item| item.start);
        for &split in splits.iter() {
            let num = &s[..split.start];
            let (key, value) = UNITS.rows[split.row];
            if !split.spaced && split.denominator.is_none() && ["s", "t", "A", "J", "N", "cd"].contains(&key) {
                continue;
            }
            if super::lazy::recognizing() {
                let mut accept = |weight| emit(String::new(), weight + 0.01);
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
            let unit = if let Some(denominator) = split.denominator {
                format!("{}\u{a0}per\u{a0}{}", nbsp(value), nbsp(denominator))
            } else {
                nbsp(value)
            };
            for (tag, weight) in numeric::cardinal_tags(num)
                .into_iter()
                .chain(numeric::decimal_tags(num))
                .chain(numeric::fraction_tags(num))
            {
                emit(format!("measure {{ {tag} units: \"{unit}\" preserve_order: true }}"), weight + 0.01);
            }
        }
    });
}

const PER_UNITS: &[(&str, &str)] = &[
    ("/jam", "per jam"),
    ("/j", "per jam"),
    ("/h", "per jam"),
    ("/hr", "per jam"),
    ("/menit", "per menit"),
    ("/mnt", "per menit"),
    ("/min", "per menit"),
    ("/detik", "per detik"),
    ("/dtk", "per detik"),
    ("/s", "per detik"),
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
    ("/t", "per ton"),
    ("/ton", "per ton"),
    ("/l", "per liter"),
    ("/L", "per liter"),
    ("/ml", "per mililiter"),
    ("/mL", "per mililiter"),
    ("/m", "per meter"),
    ("/km", "per kilometer"),
    ("/m²", "per meter persegi"),
    ("/m2", "per meter persegi"),
    ("/m³", "per meter kubik"),
    ("/m3", "per meter kubik"),
    ("/kWh", "per kilowatt-jam"),
    ("/orang", "per orang"),
    ("/malam", "per malam"),
    ("/unit", "per unit"),
];

fn endpoint(s: &str) -> Option<String> {
    let mut bases = vec![(s, None)];
    for (k, v) in numeric::quantity_rows(s).chain(abbr_rows(s)) {
        if let Some(r) = s.strip_suffix(k) {
            bases.push((r.strip_suffix(' ').unwrap_or(r), Some(v)));
        }
    }
    for (k, v) in [("k", "ribu"), ("K", "ribu"), ("M", "juta"), ("B", "miliar"), ("T", "triliun")] {
        if let Some(r) = s.strip_suffix(k) {
            bases.push((r.strip_suffix(' ').unwrap_or(r), Some(v)));
        }
    }
    for (base, q) in bases {
        let value = grouped(base).or_else(|| {
            base.split_once(',').and_then(|(a, b)| Some(format!("{} koma {}", grouped(a)?, digits(b)?))).or_else(|| {
                base.split_once('.').and_then(|(a, b)| {
                    if b.len() != 3 { Some(format!("{} koma {}", cardinal(a)?, digits(b)?)) } else { None }
                })
            })
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
            || s.split_once(',').is_some_and(|(a, b)| grouped(a).is_some() && digits(b).is_some())
            || s.split_once('.').is_some_and(|(a, b)| b.len() != 3 && cardinal(a).is_some() && digits(b).is_some())
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

fn amount_recognize(s: &str, rp: bool, mut emit: impl FnMut(bool, f64)) {
    if grouped(s).is_some() {
        emit(false, 0.0);
        // Plain and three-digit-grouped integers contain none of the other
        // amount branches' decimal, quantity, range, or minor-only syntax.
        return;
    }
    if let Some(r) = s
        .strip_suffix(".00")
        .or_else(|| s.strip_suffix(",00"))
        .or_else(|| s.strip_suffix(".-"))
        .or_else(|| s.strip_suffix(",-"))
        .or_else(|| s.strip_suffix(".0"))
        .or_else(|| s.strip_suffix(",0"))
    {
        if grouped(r).is_some() {
            emit(false, 0.0);
        }
    }
    if let Some((a, b)) = s.split_once(',') {
        if grouped(a).is_some() {
            if b.len() == 2 && b != "00" && b.bytes().all(|c| c.is_ascii_digit()) && !a.starts_with('0') {
                emit(false, 0.0);
            }
            if b.len() != 2 && digits(b).is_some() {
                emit(false, 0.0);
            }
        }
    }
    if let Some((a, b)) = s.split_once('.') {
        if grouped(a).is_some() {
            if b.len() == 2 && b != "00" && b.bytes().all(|c| c.is_ascii_digit()) && !a.starts_with('0') {
                emit(false, 0.05);
            }
            if b.len() != 2 && b.len() != 3 && digits(b).is_some() {
                emit(false, 0.05);
            }
        }
    }
    if s.as_bytes().last().is_some_and(u8::is_ascii_alphabetic) {
        let base = numeric::quantity_rows(s);
        let local = abbr_rows(s).filter(|_| rp);
        let intl = [("k", "ribu"), ("K", "ribu"), ("M", "juta"), ("B", "miliar"), ("T", "triliun")]
            .into_iter()
            .filter(|_| !rp);
        for (key, _) in base.chain(local).chain(intl) {
            if let Some(stem) = s.strip_suffix(key) {
                let stem = stem.strip_suffix(' ').unwrap_or(stem);
                if grouped(stem).is_some() {
                    emit(false, 0.0);
                }
                if let Some((a, b)) = stem.split_once(',') {
                    if grouped(a).is_some() && digits(b).is_some() {
                        emit(false, 0.0);
                    }
                }
                if let Some((a, b)) = stem.split_once('.') {
                    if b.len() != 3 && cardinal(a).is_some() && digits(b).is_some() {
                        emit(false, 0.05);
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
    if let Some(x) = s.strip_prefix("0,").or_else(|| s.strip_prefix("0.")) {
        if x.len() == 2 && x != "00" && cardinal(x.strip_prefix('0').unwrap_or(x)).is_some() {
            emit(true, 0.0);
        }
    }
}

fn amount(s: &str, rp: bool) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(v) = grouped(s) {
        out.push((format!("integer_part: \"{v}\""), 0.0));
        return out;
    }
    if let Some(r) = s
        .strip_suffix(".00")
        .or_else(|| s.strip_suffix(",00"))
        .or_else(|| s.strip_suffix(".-"))
        .or_else(|| s.strip_suffix(",-"))
        .or_else(|| s.strip_suffix(".0"))
        .or_else(|| s.strip_suffix(",0"))
    {
        if let Some(v) = grouped(r) {
            out.push((format!("integer_part: \"{v}\""), 0.0));
        }
    }
    if let Some((a, b)) = s.split_once(',') {
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
    if let Some((a, b)) = s.split_once('.') {
        if b.len() != 3
            && let Some(n) = cardinal(a)
        {
            if b.len() == 2 && b != "00" && b.bytes().all(|c| c.is_ascii_digit()) && !a.starts_with('0') {
                if let Some(m) = cardinal(b.strip_prefix('0').unwrap_or(b)) {
                    out.push((format!("integer_part: \"{n}\" fractional_part: \"{m}\" currency_min: \"sen\""), 0.05));
                }
            }
            if b.len() != 2 && b.len() != 3 {
                if let Some(d) = digits(b) {
                    out.push((format!("integer_part: \"{n}\" fractional_part: \"{d}\""), 0.05));
                }
            }
        }
    }
    let base_quantities = numeric::quantity_rows(s);
    let rp_quantities = abbr_rows(s).filter(|_| rp);
    let intl_quantities =
        [("k", "ribu"), ("K", "ribu"), ("M", "juta"), ("B", "miliar"), ("T", "triliun")].into_iter().filter(|_| !rp);
    for (k, q) in base_quantities.chain(rp_quantities).chain(intl_quantities) {
        if let Some(base) = s.strip_suffix(k) {
            let base = base.strip_suffix(' ').unwrap_or(base);
            if let Some(v) = grouped(base) {
                out.push((format!("integer_part: \"{v}\" quantity: \"{q}\""), 0.0));
            }
            if let Some((a, b)) = base.split_once(',') {
                if let (Some(a), Some(b)) = (grouped(a), digits(b)) {
                    out.push((format!("integer_part: \"{a}\" fractional_part: \"{b}\" quantity: \"{q}\""), 0.0));
                }
            }
            if let Some((a, b)) = base.split_once('.') {
                if b.len() != 3 {
                    if let (Some(a), Some(b)) = (cardinal(a), digits(b)) {
                        out.push((format!("integer_part: \"{a}\" fractional_part: \"{b}\" quantity: \"{q}\""), 0.05));
                    }
                }
            }
        }
    }
    for sep in [" - ", " – ", "-", "–"] {
        if let Some((a, b)) = s.split_once(sep) {
            if let (Some(a), Some(b)) = (endpoint(a), endpoint(b)) {
                out.push((format!("integer_part: \"{a} sampai {b}\""), -0.01));
            }
        }
    }
    if let Some(x) = s.strip_prefix("0,").or_else(|| s.strip_prefix("0.")) {
        if x.len() == 2 && x != "00" {
            if let Some(m) = cardinal(x.strip_prefix('0').unwrap_or(x)) {
                out.push((format!("fractional_part: \"{m}\" currency_min: \"sen\""), 0.0));
            }
        }
    }
    out
}

fn money_parts(s: &str) -> (&str, Option<&'static str>, Option<&'static str>) {
    let (per, base) = if let Some((key, value)) = PER_UNIT_INDEX.first_suffix(s) {
        (Some(value), &s[..s.len() - key.len()])
    } else {
        (None, s)
    };
    let (core, negative) = if let Some(rest) = base.strip_prefix('-').or_else(|| base.strip_prefix('−')) {
        (rest, Some("negatif"))
    } else if let Some(rest) = base.strip_prefix('~') {
        (rest.strip_prefix(' ').unwrap_or(rest), Some("sekitar"))
    } else if let Some(rest) = base.strip_prefix('±') {
        (rest.strip_prefix(' ').unwrap_or(rest), Some("kurang lebih"))
    } else {
        (base, None)
    };
    (core, negative, per)
}

pub(crate) fn selected_grouped_money(s: &str, wanted: crate::text::Cost) -> Option<String> {
    let (core, negative, per) = money_parts(s);
    for (symbol, major, is_rp, is_code) in currency_matches(core) {
        for leading in [true, false] {
            let number = if leading {
                let Some(stem) = core.strip_prefix(symbol) else { continue };
                trim_one(stem)
            } else {
                if is_rp && symbol != "IDR" {
                    continue;
                }
                let Some(stem) = core.strip_suffix(symbol) else { continue };
                if (is_code || is_rp) && !stem.ends_with([' ', '\u{a0}', '\u{202f}']) {
                    continue;
                }
                stem.trim_end_matches([' ', '\u{a0}', '\u{202f}'])
            };
            let weight = if leading { 0.0 } else { 0.01 };
            if let Some(value) = Integer::grouped(number) {
                if crate::text::cost(1.09 + weight) != wanted {
                    continue;
                }
                let mut tag = String::with_capacity(number.len() * 24 + major.len() * 2 + 128);
                tag.push_str("money { ");
                if let Some(negative) = negative {
                    tag.push_str("negative: \"");
                    tag.push_str(negative);
                    tag.push_str("\" ");
                }
                if leading {
                    tag.push_str("currency_maj: \"");
                    tag.extend(major.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
                    tag.push_str("\" ");
                }
                tag.push_str("integer_part: \"");
                value.write(&mut tag);
                tag.push('"');
                if !leading {
                    tag.push_str(" currency_maj: \"");
                    tag.extend(major.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
                    tag.push('"');
                }
                if let Some(per) = per {
                    tag.push_str(" morphosyntactic_features: \"");
                    tag.extend(per.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
                    tag.push('"');
                }
                tag.push_str(" }");
                return Some(tag);
            }
            // A non-integer path at this earlier source row may be the selected
            // witness. Replay the general renderer rather than changing order.
            let mut earlier = false;
            super::lazy::recognize(|| {
                amount_recognize(number, is_rp, |minor, amount_weight| {
                    if (!leading || !minor || symbol != "¢")
                        && crate::text::cost(1.09 + (amount_weight + weight)) == wanted
                    {
                        earlier = true;
                    }
                });
            });
            if earlier {
                return None;
            }
        }
    }
    None
}

pub(crate) fn money(s: &str, mut emit: impl FnMut(String, f64)) {
    if !money_start(s) {
        return;
    }
    let (s_core, neg, per) = money_parts(s);
    let per = per.map(nbsp);
    let per_tag = if super::lazy::recognizing() {
        String::new()
    } else {
        per.map(|v| format!(" morphosyntactic_features: \"{v}\"")).unwrap_or_default()
    };
    let neg_tag = if super::lazy::recognizing() {
        String::new()
    } else {
        neg.map(|v| format!("negative: \"{v}\" ")).unwrap_or_default()
    };

    for (sym, maj, is_rp, is_code) in currency_matches(s_core) {
        if !s_core.starts_with(sym) && !s_core.ends_with(sym) {
            continue;
        }
        let maj = nbsp(maj);
        if let Some(stem) = s_core.strip_prefix(sym) {
            let num = trim_one(stem);
            if super::lazy::recognizing() {
                amount_recognize(num, is_rp, |minor, w| {
                    if !minor || sym != "¢" {
                        emit(String::new(), w);
                    }
                });
            } else {
                for (amt, w) in amount(num, is_rp) {
                    let reading = if amt.starts_with("fractional_part") && !amt.contains("integer_part") {
                        if sym == "¢" {
                            continue;
                        }
                        format!("money {{ {neg_tag}{amt}{per_tag} }}")
                    } else {
                        format!("money {{ {neg_tag}currency_maj: \"{maj}\" {amt}{per_tag} }}")
                    };
                    emit(reading, w);
                }
            }
        }
        if let Some(stem) = s_core.strip_suffix(sym) {
            if is_rp && sym != "IDR" {
                continue;
            }
            let is_req_space = is_code || (is_rp && sym == "IDR");
            let has_space = stem.ends_with([' ', '\u{a0}', '\u{202f}']);
            if is_req_space && !has_space {
                continue;
            }
            let num = stem.trim_end_matches([' ', '\u{a0}', '\u{202f}']);
            if super::lazy::recognizing() {
                amount_recognize(num, is_rp, |_, w| emit(String::new(), w + 0.01));
            } else {
                for (amt, w) in amount(num, is_rp) {
                    let reading = if amt.starts_with("fractional_part") && !amt.contains("integer_part") {
                        format!("money {{ {neg_tag}{amt}{per_tag} }}")
                    } else {
                        format!("money {{ {neg_tag}{amt} currency_maj: \"{maj}\"{per_tag} }}")
                    };
                    emit(reading, w + 0.01);
                }
            }
        }
    }
}

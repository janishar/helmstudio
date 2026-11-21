//! `es/taggers/money.py`.

use super::{cardinal, decimals};
use crate::lang::es::{FirstChars, Lazy, Sink, Stash, ok_end, prefix_static};
use crate::text::{Cost, Row, cost, is_alpha, is_digit, is_nemo_space};

crate::tsv!(MAJ, "es/money/currency_major.tsv");
crate::tsv!(MIN, "es/money/currency_minor.tsv");
crate::tsv!(FEM_PLURAL, "es/money/currency_plural_fem.tsv");
crate::tsv!(MASC_PLURAL, "es/money/currency_plural_masc.tsv");

/// A small static key → values map: sorted keys, binary search (cheaper than hashing for these tables).
struct Map<V>(Vec<(&'static str, Vec<V>)>);

impl<V> Map<V> {
    fn new() -> Self {
        Map(Vec::new())
    }
    fn entry_push(&mut self, k: &'static str, v: V) {
        match self.0.binary_search_by(|(x, _)| x.cmp(&k)) {
            Ok(i) => self.0[i].1.push(v),
            Err(i) => self.0.insert(i, (k, vec![v])),
        }
    }
    fn get(&self, k: &str) -> Option<&Vec<V>> {
        self.0.binary_search_by(|(x, _)| (*x).cmp(k)).ok().map(|i| &self.0[i].1)
    }
}

/// singular → plurals (`fem_plural | masc_plural`, fem rows first).
static PLURALS: std::sync::LazyLock<Map<(&'static str, Cost)>> = std::sync::LazyLock::new(|| {
    let (f, m): (&'static crate::text::Table, &'static crate::text::Table) = (&FEM_PLURAL, &MASC_PLURAL);
    let mut map: Map<(&'static str, Cost)> = Map::new();
    for r in f.rows().iter().chain(m.rows()) {
        map.entry_push(r.key.as_str(), (r.value.as_str(), r.cost));
    }
    map
});

/// symbol → rows of `table`, file order.
fn by_key(table: &'static crate::text::Table) -> Map<&'static Row> {
    let mut map: Map<&'static Row> = Map::new();
    for r in table.rows() {
        map.entry_push(r.key.as_str(), r);
    }
    map
}

/// `x @ (fem_plural | masc_plural)`: every plural of a singular name.
fn plurals(s: &str) -> &'static [(&'static str, Cost)] {
    PLURALS.get(s).map_or(&[], Vec::as_slice)
}

/// What the readings of one currency_major row need, looked up once per row instead of by string per reading.
struct MajInfo {
    /// Index of the first row with this row's key (the symbol's identity).
    key_id: usize,
    /// Plurals of the row's value.
    plurals: &'static [(&'static str, Cost)],
    /// Every currency_major row with this key, file order.
    same_key: Vec<&'static Row>,
    /// currency_minor rows with this key and the plurals of each value.
    min: Vec<(&'static Row, &'static [(&'static str, Cost)])>,
}

static MAJ_INFO: std::sync::LazyLock<Vec<MajInfo>> = std::sync::LazyLock::new(|| {
    let maj_by_key = by_key(&MAJ);
    let min_by_key = by_key(&MIN);
    let rows: &'static [Row] = MAJ.rows();
    rows.iter()
        .map(|r| MajInfo {
            key_id: rows.iter().position(|x| x.key == r.key).expect("own row"),
            plurals: plurals(&r.value),
            same_key: maj_by_key.get(&r.key).cloned().unwrap_or_default(),
            min: min_by_key
                .get(&r.key)
                .map_or(&[][..], Vec::as_slice)
                .iter()
                .map(|m| (*m, plurals(&m.value)))
                .collect(),
        })
        .collect()
});

/// The [`MAJ_INFO`] entry of a currency_major row.
fn info(row: &'static Row) -> &'static MajInfo {
    let rows = MAJ.rows();
    &MAJ_INFO[(row as *const Row as usize - rows.as_ptr() as usize) / std::mem::size_of::<Row>()]
}

fn skip_ws(text: &[char], mut p: usize) -> usize {
    while p < text.len() && is_nemo_space(text[p]) {
        p += 1;
    }
    p
}

/// Positions after `closure(delete_space, 0, 1)` = any whitespace run (or none).
fn ws(text: &[char], p: usize) -> std::ops::RangeInclusive<usize> {
    p..=skip_ws(text, p)
}

/// Currency symbols at `p` (rows of currency_major): file order within one key, shorter keys first.
fn symbols(text: &[char], p: usize) -> impl Iterator<Item = (&'static Row, usize)> + '_ {
    prefix_static(&MAJ, text, p)
}

/// Whether a currency symbol starts at `p`.
fn symbol_at(text: &[char], p: usize) -> bool {
    MAJ_FIRST.at(text, p) && symbols(text, p).next().is_some()
}

/// First chars of the currency_major keys.
static MAJ_FIRST: std::sync::LazyLock<FirstChars> = std::sync::LazyLock::new(|| FirstChars::of_table(&MAJ));

/// `currency_maj` values: the singular, or every plural (with its cost).
fn maj(row: &'static Row, plural: bool) -> impl Iterator<Item = (&'static str, Cost)> {
    let sing = (!plural).then_some((row.value.as_str(), row.cost));
    let plur = if plural { info(row).plurals } else { &[] };
    sing.into_iter().chain(plur.iter().map(move |&(p, c)| (p, row.cost + c)))
}

/// An amount body: `integer_part: "un"`, a cardinal span or a decimal reading, written only for returned tags.
#[derive(Clone, Copy)]
enum Amount {
    One,
    /// Cardinal words of `text[start..end]`.
    Int(usize, usize),
    /// A `wo_negative` reading at a start.
    Decimal(usize, decimals::Dec),
}

impl Amount {
    fn write(&self, text: &[char], s: &mut String) {
        match *self {
            Amount::One => s.push_str("integer_part: \"un\""),
            Amount::Int(a, b) => {
                s.push_str("integer_part: \"");
                s.push_str(&cardinal::words_at(text, a, b));
                s.push('"');
            }
            Amount::Decimal(a, d) => d.write(text, a, s),
        }
    }
}

/// Integer amount spans at `p`: `f(end, amount, cost, is_one)`.
fn integers(text: &[char], p: usize, f: &mut dyn FnMut(usize, Amount, Cost, bool)) {
    if text.get(p) == Some(&'1') && !text.get(p + 1).is_some_and(|c| is_digit(*c)) {
        f(p + 1, Amount::One, 0, true);
    }
    for e in cardinal::number_ends(text, p) {
        if e == p + 1 && text[p] == '1' {
            continue;
        }
        if let Some(c) = cardinal::cost_at(text, p, e) {
            f(e, Amount::Int(p, e), c, false);
        }
    }
}

/// Decimal amounts: `f(end, amount, cost, kind)`, kind 0 = singular "1,", 1 = plural, 2 = with quantity.
fn decimals_at(text: &[char], p: usize, f: &mut dyn FnMut(usize, Amount, Cost, u8)) {
    if !text.get(p).is_some_and(|c| is_digit(*c)) {
        return;
    }
    for &d in decimals::wo_negative(text, p).iter() {
        let (e, c) = (d.end, d.cost);
        let span = &text[p..e];
        let one = span.len() >= 2 && span[0] == '1' && span[1] == ',';
        let am = Amount::Decimal(p, d);
        if span.contains(&',') && !one {
            f(e, am, c, 1);
        }
        if one {
            f(e, am, c, 0);
        }
        if is_alpha(text[e - 1]) {
            f(e, am, c, 2);
        }
    }
}

/// `two_digits_fractional_part`: trailing zeros deleted, then `0X → X`, `X → X0`, `XY → XY` (X nonzero).
fn two_digits(d: &[char]) -> Option<[u8; 2]> {
    let mut n = d.len();
    while n > 0 && d[n - 1] == '0' {
        n -= 1;
    }
    match d[..n] {
        [x] => Some([x as u8, b'0']),
        ['0', y] => Some([y as u8, 0]),
        [x, y] => Some([x as u8, y as u8]),
        _ => None,
    }
}

/// The digits of a `two_digits` result.
fn two_slice(t: &[u8; 2]) -> &[u8] {
    if t[1] == 0 { &t[..1] } else { &t[..] }
}

/// One money reading, built into tag text only when returned.
#[derive(Clone, Copy)]
struct M {
    end: usize,
    cost: Cost,
    rank: i32,
    /// `currency_maj` before the amount (`$5`) or after it (`5 $`).
    maj_first: bool,
    maj: Option<&'static str>,
    amount: Option<Amount>,
    /// `fractional_part` words and the `currency_min` value (if any) of a minor-unit reading.
    frac: Option<(&'static str, Option<&'static str>)>,
}

impl M {
    fn tag(&self, text: &[char]) -> String {
        let mut s = String::with_capacity(128);
        s.push_str("money { ");
        let maj = |s: &mut String| {
            if let Some(m) = self.maj {
                s.push_str("currency_maj: \"");
                s.push_str(m);
                s.push('"');
            }
        };
        if let Some((f, m)) = self.frac {
            // decimal_graph_with_minor: integer_part currency_maj fractional_part currency_min? preserve_order
            if let Some(a) = &self.amount {
                a.write(text, &mut s);
                s.push(' ');
                maj(&mut s);
                s.push(' ');
            }
            s.push_str("fractional_part: \"");
            s.push_str(f);
            s.push('"');
            if let Some(m) = m {
                s.push_str(" currency_min: \"");
                s.push_str(m);
                s.push('"');
            }
            s.push_str(" preserve_order: true");
        } else if self.maj_first {
            maj(&mut s);
            s.push(' ');
            self.amount.as_ref().unwrap().write(text, &mut s);
        } else {
            self.amount.as_ref().unwrap().write(text, &mut s);
            s.push(' ');
            maj(&mut s);
        }
        s.push_str(" }");
        s
    }
}

pub struct Money;

impl Money {
    fn graph(&self, text: &[char], start: usize, emit: &mut dyn FnMut(M)) {
        let starts_symbol = symbol_at(text, start);
        if is_digit(text[start]) {
            // amount-first spans need a currency symbol after the amount (and an optional quantity word)
            let mut q = start;
            while q < text.len() && (is_digit(text[q]) || matches!(text[q], '.' | ',') || is_nemo_space(text[q])) {
                q += 1;
            }
            if let Some((_, e)) = decimals::QUANTITIES.prefix_matches(text, q).last() {
                q = e;
                while q < text.len() && is_nemo_space(text[q]) {
                    q += 1;
                }
            }
            if !starts_symbol && !(start..=q.min(text.len() - 1)).any(|p| symbol_at(text, p)) {
                return;
            }
        } else if !starts_symbol {
            return;
        }
        // every symbol match from `start` up to the end of the money-shaped run (digits, separators, whitespace and
        // a trailing quantity word), computed once: (position, row, end). A symbol at `start` moves the run start
        // past it (`€ 5,32`).
        let lead = symbols(text, start).map(|(_, e)| e).max().unwrap_or(start);
        let mut scan_end = lead;
        loop {
            while scan_end < text.len()
                && (is_digit(text[scan_end]) || matches!(text[scan_end], '.' | ',') || is_nemo_space(text[scan_end]))
            {
                scan_end += 1;
            }
            if scan_end == lead {
                break;
            }
            match decimals::QUANTITIES.prefix_matches(text, scan_end).last() {
                Some((_, e)) if e > scan_end => scan_end = e,
                _ => break,
            }
        }
        let mut sym_at: Vec<(usize, &'static Row, usize)> = Vec::new();
        let mut q = start;
        loop {
            if symbol_at(text, q) {
                for (row, se) in symbols(text, q) {
                    sym_at.push((q, row, se));
                }
            }
            // past the run, only a symbol directly after a previous symbol's end matters (amounts follow symbols)
            if q >= scan_end.max(lead) || q >= text.len() {
                break;
            }
            q += 1;
        }
        let syms = |p: usize| sym_at.iter().filter(move |(q, _, _)| *q == p).map(|&(_, r, e)| (r, e));
        let has = |p: usize| sym_at.iter().any(|(q, _, _)| *q == p);
        let m = |end, cost, rank, maj_first, maj, amount| M {
            end,
            cost,
            rank,
            maj_first,
            maj: Some(maj),
            amount: Some(amount),
            frac: None,
        };
        // symbol first
        for (row, se) in syms(start) {
            for p in ws(text, se) {
                if !text.get(p).is_some_and(|c| is_digit(*c)) {
                    continue;
                }
                integers(text, p, &mut |e, a, c, one| {
                    for (mj, mc) in maj(row, !one) {
                        emit(m(e, c + mc, 0, true, mj, a));
                    }
                });
                decimals_at(text, p, &mut |e, a, c, kind| {
                    for (mj, mc) in maj(row, kind != 0) {
                        emit(m(e, c + mc, 1, true, mj, a));
                    }
                });
            }
        }
        // amount first
        if is_digit(text[start]) {
            let sym_after = |e: usize| (e..=skip_ws(text, e)).any(has);
            let any_int =
                cardinal::number_ends(text, start).any(sym_after) || (text[start] == '1' && sym_after(start + 1));
            if any_int {
                integers(text, start, &mut |e, a, c, one| {
                    for p in ws(text, e) {
                        for (row, se) in syms(p) {
                            for (mj, mc) in maj(row, !one) {
                                emit(m(se, c + mc, 0, false, mj, a));
                            }
                        }
                    }
                });
            }
            // a decimal amount ends at a digit after its ","; a symbol must follow that end
            let mut dec_sym = false;
            let mut q = start;
            while q < text.len() && (is_digit(text[q]) || matches!(text[q], '.' | ',') || is_nemo_space(text[q])) {
                q += 1;
                if q > start && is_digit(text[q - 1]) && text[start..q].contains(&',') && sym_after(q) {
                    dec_sym = true;
                    break;
                }
            }
            if dec_sym {
                decimals_at(text, start, &mut |e, a, c, kind| {
                    if kind == 2 {
                        return;
                    }
                    for p in ws(text, e) {
                        for (row, se) in syms(p) {
                            for (mj, mc) in maj(row, kind != 0) {
                                emit(m(se, c + mc, 1, false, mj, a));
                            }
                        }
                    }
                });
            }
        }
        // with minor
        let w = cost(-0.001);
        for (row, se) in syms(start) {
            for p in ws(text, se) {
                self.minor(text, p, row, &|e| ok_end(text, e), &mut |mut x: M| {
                    x.cost += w;
                    emit(x);
                });
            }
        }
        // amount first: only symbols that occur right after the amount's digit/comma/whitespace run can close it
        if !is_digit(text[start]) {
            return;
        }
        // shape: digits "," digits, whitespace, symbol; the symbol can only start after the fraction digits
        let mut d = start;
        while d < text.len() && is_digit(text[d]) {
            d += 1;
        }
        if text.get(d) != Some(&',') {
            return;
        }
        let mut f = d + 1;
        while f < text.len() && is_digit(text[f]) {
            f += 1;
        }
        // the minor reading ends inside the fraction digits; the symbol follows after optional whitespace:
        // `(row, position, symbol end)` without repeated `(key, position, end)`, keys in order of first appearance
        let mut keyed: Vec<(&'static Row, usize, usize)> = Vec::new();
        for p in d + 2..=skip_ws(text, f).min(text.len()) {
            for (row, se) in syms(p) {
                let k = info(row).key_id;
                if !keyed.iter().any(|&(r, q, e)| info(r).key_id == k && q == p && e == se) {
                    keyed.push((row, p, se));
                }
            }
        }
        // one `minor` run per symbol key (its first row), closed by every match of that key
        for (i, &(row, _, _)) in keyed.iter().enumerate() {
            let k = info(row).key_id;
            if keyed[..i].iter().any(|&(r, _, _)| info(r).key_id == k) {
                continue;
            }
            let at = || keyed.iter().filter(move |&&(r, _, _)| info(r).key_id == k).map(|&(_, p, se)| (p, se));
            let keep = |e: usize| at().any(|(p, _)| p >= e && p <= skip_ws(text, e));
            self.minor(text, start, row, &keep, &mut |x: M| {
                // `ws(x.end)` positions: x.end itself, then each position inside the following whitespace run
                let ws_end = skip_ws(text, x.end);
                for (p, se) in at() {
                    if p >= x.end && p <= ws_end {
                        emit(M { end: se, cost: x.cost + w, ..x });
                    }
                }
            });
        }
    }

    /// `decimal_graph_with_minor_curr` for one symbol (without the symbol itself).
    fn minor(&self, text: &[char], p: usize, sym: &'static Row, keep: &dyn Fn(usize) -> bool, emit: &mut dyn FnMut(M)) {
        // integer digits (no separators) then ","
        let mut d = p;
        while d < text.len() && is_digit(text[d]) {
            d += 1;
        }
        if d == p || text.get(d) != Some(&',') {
            return;
        }
        let info = info(sym);
        let maj_rows: &[&'static Row] = &info.same_key;
        let mins: &[(&'static Row, &'static [(&'static str, Cost)])] = &info.min;
        let frac =
            |fs: usize, emit: &mut dyn FnMut(M), head: Option<(Amount, &'static str)>, pc: Cost, allow_plain: bool| {
                let mut fe = fs;
                while fe < text.len() && is_digit(text[fe]) {
                    fe += 1;
                    if !keep(fe) {
                        continue;
                    }
                    let Some(two) = two_digits(&text[fs..fe]) else {
                        continue;
                    };
                    let tb = two_slice(&two);
                    let (amount, maj) = match head {
                        Some((a, m)) => (Some(a), Some(m)),
                        None => (None, None),
                    };
                    let mk = |cost, f: &'static str, min| M {
                        end: fe,
                        cost,
                        rank: 2,
                        maj_first: false,
                        maj,
                        amount,
                        frac: Some((f, min)),
                    };
                    if tb == b"1" {
                        for &(r, _) in mins {
                            emit(mk(pc + r.cost, "un", Some(r.value.as_str())));
                        }
                    } else if let Some(w) = cardinal::two_digit_non_zero_str(tb) {
                        for &(r, pls) in mins {
                            for &(pl, plc) in pls {
                                emit(mk(pc + r.cost + plc, w, Some(pl)));
                            }
                        }
                    }
                    if allow_plain {
                        if let Some(w) = cardinal::two_digit_non_zero_str(tb) {
                            emit(mk(pc + cost(0.0001), w, None));
                        }
                    }
                }
            };
        // integer_plus_maj: (DIGIT* - "0") digits
        for e in p + 1..=d {
            if e != d || (e == p + 1 && text[p] == '0') {
                continue;
            }
            let (a, ic, one) = if e == p + 1 && text[p] == '1' {
                (Amount::One, 0, true)
            } else {
                match cardinal::cost_at(text, p, e) {
                    Some(c) => (Amount::Int(p, e), c, false),
                    None => continue,
                }
            };
            for r in maj_rows {
                for (mj, mc) in maj(r, !one) {
                    frac(e + 1, emit, Some((a, mj)), ic + mc, true);
                }
            }
        }
        if text.get(p) == Some(&'0') && text.get(p + 1) == Some(&',') {
            frac(p + 2, emit, None, 0, false);
        }
    }
}

thread_local! {
    /// The readings explored on the current text (tags built only for the best-path ones).
    static STASH: Stash<M> = const { Stash::new() };
}

/// Forget the previous text's readings.
pub fn reset() {
    STASH.with(Stash::clear);
}

impl Lazy for Money {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let exploring = out.exploring();
        self.graph(text, start, &mut |h: M| {
            if h.end > start && ok_end(text, h.end) {
                let c = cost(1.09) + h.cost;
                if exploring {
                    STASH.with(|s| s.put(start, h.end, c, h.rank, h));
                }
                out.push(h.end, c, h.rank, || h.tag(text));
            }
        });
    }

    fn recall(&self, text: &[char], start: usize, end: usize, cost: Cost, rank: i32) -> Option<String> {
        STASH.with(|s| s.find(start, end, cost, rank)).map(|h| h.tag(text))
    }
}

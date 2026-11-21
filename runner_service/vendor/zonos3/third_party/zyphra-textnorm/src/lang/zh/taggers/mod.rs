//! `zh/taggers/*.py` (`preprocessor.py` is not wired into `ClassifyFst` and is not ported).

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod punctuation;
pub mod time;
pub mod whitelist;
pub mod word;

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, match_str};

/// End of the ASCII digit run starting at `i`.
pub fn digit_end(t: &[char], i: usize) -> usize {
    let mut e = i;
    while e < t.len() && t[e].is_ascii_digit() {
        e += 1;
    }
    e
}

/// The sorted first chars of a table's keys (a cheap reject before any table walk).
pub fn first_chars(table: &crate::text::Table) -> Vec<char> {
    let mut v: Vec<char> = table.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v.dedup();
    v
}

/// The char after the digit run that follows an optional sign at `p` (any number of `-`, or one `负`/`負`).
pub fn after_signed_digits(t: &[char], p: usize) -> Option<char> {
    let q = match t.get(p) {
        Some('负' | '負') => p + 1,
        _ => p + t[p..].iter().take_while(|&&c| c == '-').count(),
    };
    let r = digit_end(t, q);
    if r == q {
        return None;
    }
    t.get(r).copied()
}

/// Every end `e` such that `t[p..e]` is a `just_cardinals` input, ascending: one digit, or 2-12 digits without a
/// leading `0` (the graph reads exactly those; the unit test `cardinal::tests::domain` pins it).
pub fn card_ends(t: &[char], p: usize) -> std::ops::Range<usize> {
    match t.get(p) {
        Some('0') => p + 1..p + 2,
        Some(c) if c.is_ascii_digit() => p + 1..digit_end(t, p).min(p + cardinal::MAX_DIGITS) + 1,
        _ => p + 1..p + 1,
    }
}

/// The end of the whole digit run at `p` when it is a `just_cardinals` input. Every branch that needs a non-digit
/// after its cardinal can only use this end (a shorter cardinal is followed by a digit).
pub fn card_run(t: &[char], p: usize) -> Option<usize> {
    let r = digit_end(t, p);
    card_ends(t, p).contains(&r).then_some(r)
}

/// `just_cardinals` of `t[p..e]` (an end from [`card_ends`]) appended to `out`.
pub fn card(t: &[char], p: usize, e: usize, out: &mut String) {
    let ok = cardinal::just_cardinal(&t[p..e], out);
    debug_assert!(ok);
}
/// The `suffix` union of `decimal.py` / `money.py` (quantities after a number, passed through).
pub const QUANTITY: &[&str] = &[
    "万", "十万", "百万", "千万", "亿", "十亿", "百亿", "千亿", "萬", "十萬", "百萬", "千萬", "億", "十億", "百億",
    "千億", "拾萬", "佰萬", "仟萬", "拾億", "佰億", "仟億", "拾万", "佰万", "仟万", "仟亿", "佰亿", "万亿", "萬億",
];

/// Every `(end, word)` of [`QUANTITY`] matching at `p`, in list order.
pub fn quantities(t: &[char], p: usize, mut f: impl FnMut(usize, &'static str)) {
    if !matches!(t.get(p), Some('万' | '十' | '百' | '千' | '亿' | '萬' | '億' | '拾' | '佰' | '仟')) {
        return;
    }
    for q in QUANTITY {
        if let Some(n) = match_str(t, p, q) {
            f(p + n, q);
        }
    }
}

/// `closure(digit | zero, 1)` over `s`: each digit by name.
pub fn digit_names(s: &[char], out: &mut String) {
    for &c in s {
        out.push_str(cardinal::digit_name(c));
    }
}

/// `closure(just_cardinals, 1)` over a digit slice. The graph reads any split of the digits into cardinals at the same
/// cost; the reading with the fewest pieces (the whole number when it fits) is the numerically right one, so that is
/// the one emitted (ties: longer first piece). `false` only for an empty slice.
pub fn cardinals(s: &[char], out: &mut String) -> bool {
    if s.is_empty() {
        return false;
    }
    if cardinal::just_cardinal(s, out) {
        return true;
    }
    // best[i]: (pieces, first piece length) for s[i..]
    let n = s.len();
    let mut best: Vec<(usize, usize)> = vec![(usize::MAX, 0); n + 1];
    best[n] = (0, 0);
    let mut buf = String::new();
    for i in (0..n).rev() {
        for len in (1..=(n - i).min(cardinal::MAX_DIGITS)).rev() {
            if best[i + len].0 == usize::MAX {
                continue;
            }
            buf.clear();
            if cardinal::just_cardinal(&s[i..i + len], &mut buf) && best[i + len].0 + 1 < best[i].0 {
                best[i] = (best[i + len].0 + 1, len);
            }
        }
    }
    let mut i = 0;
    while i < n {
        let len = best[i].1;
        cardinal::just_cardinal(&s[i..i + len], out);
        i += len;
    }
    true
}

/// One `classify` branch: pushes every span it accepts at `start` into a [`Sink`]. Readings are plain `Copy` data;
/// tag text is written only inside the `Sink::raw` closures, which run only for the candidate the lattice kept.
pub trait Gen: Send + Sync {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink);
}

/// Tie-break among equal-cost paths (same cost, same number of digit-holding tokens), read off the oracle:
/// - a number token that consumes a leading `-` loses to the punctuation run taking it (`--419` → `name "--"` +
///   `cardinal 419`, not `name "-"` + `cardinal -419`; `(-7/72` → `name "(-"` + fraction; `\-88/93\` → `name "\-"`);
/// - `N分` reads as time (`minutes: "N分"`), not money (`currency_min: "分"`) (`5分`; set in [`money`]).
pub const LOSES: i32 = 1_000_000;

/// `classify` of `tokenize_and_classify.py` as one lattice classifier: every branch in union order with its weight.
/// A candidate's rank is `1000 × branch index` (the lattice's own per-classifier rank) plus the branch's tie ranks
/// (multiples of [`LOSES`]), so the branch is read back from the rank in [`Classifier::materialize`]. Candidates
/// carry an empty tag; `materialize` reruns the one branch and writes the tag of the kept candidate.
pub struct Union {
    date: date::Date,
    fraction: fraction::Fraction,
    money: money::Money,
    measure: measure::Measure,
    time: time::Time,
    whitelist: whitelist::WhiteList,
    cardinal: cardinal::Cardinal,
    decimal: decimal::Decimal,
    ordinal: ordinal::Ordinal,
    punctuation: punctuation::Punctuation,
    word: word::Word,
}

/// Branch weights in union order (`tokenize_and_classify.py:80-95`).
const COSTS: [Cost; 11] = [
    cost(1.1),
    cost(1.0),
    cost(1.1),
    cost(1.05),
    cost(1.1),
    cost(1.1),
    cost(1.1),
    cost(3.05),
    cost(1.1),
    cost(1.0),
    cost(100.0),
];

/// Number branches: a candidate starting at `-` ranks [`LOSES`] behind.
const NUMBER: [bool; 11] = [true, true, true, true, true, false, true, true, true, false, false];

/// CJK ideographs some branch other than the word can start with (every other ideograph is a one-char word only:
/// no punctuation mark, whitelist key or currency starts with one; unit test `ideograph_dispatch`).
const IDEOGRAPH_STARTS: &[char] =
    &['从', '第', '公', '西', '纪', '差', '负', '負', '分', '百', '千', '万', '十', '亿', '萬', '億', '拾', '佰', '仟'];

/// Which branches can accept a span starting at `start`, as a bit set over union indices. A digit-first span of
/// a number branch continues its digit run with a follower that branch's own reject test requires; testing each
/// branch's follower predicate here once spares the calls. Unit test `dispatch`.
fn branches_at(t: &[char], start: usize) -> u16 {
    const ALL: u16 = (1 << 11) - 1;
    let c = t[start];
    if c.is_ascii_digit() {
        // cardinal and word always; no whitelist key, ordinal or punctuation mark starts with a digit
        let mut set = 1 << 6 | 1 << 10;
        if let Some(&f) = t.get(digit_end(t, start)) {
            let branches = [
                (0, date::digit_follower(f)),
                (1, fraction::digit_follower(f)),
                (2, money::digit_follower(f)),
                (3, f == '.' || measure::unit_first(f)),
                (4, time::digit_follower(f)),
                (7, f == '.'),
            ];
            for (i, on) in branches {
                set |= u16::from(on) << i;
            }
        }
        return set;
    }
    if is_ideograph(c) && !IDEOGRAPH_STARTS.contains(&c) { 1 << 10 } else { ALL }
}

fn is_ideograph(c: char) -> bool {
    ('\u{4e00}'..='\u{9fff}').contains(&c)
}

impl Union {
    pub fn new() -> Union {
        Union {
            date: date::Date,
            fraction: fraction::Fraction,
            money: money::Money,
            measure: measure::Measure,
            time: time::Time,
            whitelist: whitelist::WhiteList,
            cardinal: cardinal::Cardinal,
            decimal: decimal::Decimal,
            ordinal: ordinal::Ordinal,
            punctuation: punctuation::Punctuation::new(),
            word: word::Word,
        }
    }

    fn branch(&self, i: usize, t: &[char], start: usize, sink: &mut Sink) {
        sink.cost = COSTS[i];
        sink.base = 1000 * i as i32 + if NUMBER[i] && t[start] == '-' { LOSES } else { 0 };
        match i {
            0 => self.date.run(t, start, sink),
            1 => self.fraction.run(t, start, sink),
            2 => self.money.run(t, start, sink),
            3 => self.measure.run(t, start, sink),
            4 => self.time.run(t, start, sink),
            5 => self.whitelist.run(t, start, sink),
            6 => self.cardinal.run(t, start, sink),
            7 => self.decimal.run(t, start, sink),
            8 => self.ordinal.run(t, start, sink),
            9 => self.punctuation.run(t, start, sink),
            _ => self.word.run(t, start, sink),
        }
    }
}

impl Classifier for Union {
    fn candidates(&self, t: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            STASH.with_borrow_mut(Vec::clear);
        }
        let set = branches_at(t, start);
        if set == 1 << 10 {
            // a plain ideograph: the one-char word only (its tag is written by `materialize`)
            out.push(Candidate { end: start + 1, cost: COSTS[10], rank: 10_000, tag: String::new() });
            return;
        }
        STASH.with_borrow_mut(|stash| {
            let from = out.len();
            let mut sink = Sink {
                start,
                stash: Some(stash),
                from,
                cost: 0,
                base: 0,
                out: Some(out),
                want: (0, 0, 0),
                found: None,
            };
            for i in 0..COSTS.len() {
                if set & 1 << i != 0 {
                    self.branch(i, t, start, &mut sink);
                }
            }
        });
    }

    fn materialize(&self, t: &[char], start: usize, c: &mut Candidate) {
        let branch = (c.rank % LOSES / 1000) as usize;
        let mut tag = String::with_capacity(64);
        match branch {
            // word, punctuation (marks or an emphasis tag): `name: "<span>"`
            9 | 10 => {
                tag.push_str("name: \"");
                tag.extend(&t[start..c.end]);
                tag.push('"');
            }
            // cardinal: the sign is the first char, the digits the rest of the span
            6 => cardinal::write_tag(t, start, c.end, &mut tag),
            _ => {
                let key = (start, c.end, c.cost, c.rank);
                let recall = STASH.with_borrow(|st| {
                    st.iter().find(|&&(s, e, co, r, _)| (s, e, co, r) == key).map(|&(.., recall)| recall)
                });
                match recall {
                    Some(recall) => recall.write(t, &mut tag),
                    None => {
                        let mut sink = Sink {
                            start,
                            stash: None,
                            from: 0,
                            cost: 0,
                            base: 0,
                            out: None,
                            want: (c.end, c.cost, c.rank),
                            found: None,
                        };
                        self.branch(branch, t, start, &mut sink);
                        tag = sink.found.expect("materialize: the branch pushes the kept candidate again");
                    }
                }
            }
        }
        c.tag = tag;
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    /// `branches_at` drops exactly the branches that push nothing at such a char.
    #[test]
    fn ideograph_dispatch() {
        let u = Union::new();
        let probe = |c: char, text: &str| {
            let t: Vec<char> = std::iter::once(c).chain(text.chars()).collect();
            let mut v = Vec::new();
            let mut out = Vec::new();
            for i in 0..COSTS.len() {
                out.clear();
                let mut sink = Sink {
                    start: 0,
                    stash: None,
                    from: 0,
                    cost: 0,
                    base: 0,
                    out: Some(&mut out),
                    want: (0, 0, 0),
                    found: None,
                };
                u.branch(i, &t, 0, &mut sink);
                if !out.is_empty() {
                    v.push(i);
                }
            }
            v
        };
        for cp in 0x4e00..=0x9fffu32 {
            let c = char::from_u32(cp).unwrap();
            if !IDEOGRAPH_STARTS.contains(&c) {
                assert_eq!(probe(c, "12年3月"), [10], "{c}");
            }
        }
        for c in '0'..='9' {
            for rest in [
                "",
                "年",
                ".5%",
                "月",
                "号",
                "点",
                ":30",
                "元",
                "万元",
                "/3",
                "km",
                "分之一",
                "又1/2",
                "-3",
                "%",
                "美元",
                "°C",
                "a.d.",
                "·3·4",
                "差",
                "块钱",
                "毛",
                "亿",
                "kg",
                "ad",
                "BC",
                "日元",
                "日",
                "号",
                "分钟",
                "刻",
                "秒",
                "个小时",
            ] {
                let t: Vec<char> = std::iter::once(c).chain(rest.chars()).collect();
                let got = probe(c, rest);
                assert!(got.iter().all(|i| branches_at(&t, 0) & 1 << i != 0), "{c}{rest}: {got:?}");
            }
        }
    }
}

/// A kept reading whose tag [`Classifier::materialize`] writes without re-running its branch.
#[derive(Clone, Copy)]
pub enum Recall {
    Date(date::Reading),
    Time(time::Reading),
}

impl Recall {
    fn write(&self, t: &[char], out: &mut String) {
        match self {
            Recall::Date(r) => r.write(t, out),
            Recall::Time(r) => r.write(out),
        }
    }
}

thread_local! {
    /// `(start, end, cost, rank, reading)` of every [`Sink::keep`] reading the lattice kept for the current text, in
    /// push order. Cleared when a text starts (position 0 is always queried first).
    static STASH: std::cell::RefCell<Vec<(usize, usize, Cost, i32, Recall)>> =
        const { std::cell::RefCell::new(Vec::new()) };
}

/// [`Table::prefix_matches`](crate::text::Table::prefix_matches) on a static table, with the rows' `'static`
/// lifetime (the walk borrows `text` only).
pub fn prefix_static<'t>(
    table: &'static crate::text::Table,
    text: &'t [char],
    start: usize,
) -> impl Iterator<Item = (&'static crate::text::Row, usize)> + 't {
    let rows = table.rows();
    table.prefix_matches(text, start).map(move |(r, e)| {
        let i = (r as *const crate::text::Row as usize - rows.as_ptr() as usize) / size_of::<crate::text::Row>();
        (&rows[i], e)
    })
}

/// Candidate sink of one classifier call. Collecting, it records `(end, cost, rank)` with an empty tag (all the
/// lattice reads); finding, it writes the tag of the first push with the wanted `(end, cost, rank)`: among equal keys
/// from one classifier the lattice keeps the first pushed.
pub struct Sink<'a> {
    /// The queried position.
    start: usize,
    /// Collecting: the [`STASH`] of the current text (borrowed once per call).
    stash: Option<&'a mut Vec<(usize, usize, Cost, i32, Recall)>>,
    /// Candidates of this call start at `out[from]`.
    from: usize,
    cost: Cost,
    base: i32,
    out: Option<&'a mut Vec<Candidate>>,
    want: (usize, Cost, i32),
    found: Option<String>,
}

impl Sink<'_> {
    /// A span at the classifier's cost plus `extra`; `tag` writes the whole tag.
    #[inline]
    pub fn raw(&mut self, end: usize, extra: Cost, rank: i32, tag: impl FnOnce(&mut String)) {
        let cost = self.cost + extra;
        let rank = self.base + rank;
        match &mut self.out {
            Some(out) => {
                // Same start and end means the same digit count, so the lattice only ever uses the lowest
                // `(cost, rank)` of a span (the first pushed among equals): keep just that one.
                match out[self.from..].iter_mut().find(|c| c.end == end) {
                    Some(c) if (c.cost, c.rank) <= (cost, rank) => {}
                    Some(c) => (c.cost, c.rank) = (cost, rank),
                    None => out.push(Candidate { end, cost, rank, tag: String::new() }),
                }
            }
            None => {
                if self.found.is_none() && self.want == (end, cost, rank) {
                    let mut s = String::with_capacity(64);
                    tag(&mut s);
                    self.found = Some(s);
                }
            }
        }
    }

    /// Like [`raw`](Self::raw) for a reading that writes its own tag: when collecting and the candidate is kept (new
    /// span, or cheaper than the one held), the reading goes to [`STASH`] for [`Classifier::materialize`].
    #[inline]
    pub fn keep(&mut self, t: &[char], end: usize, extra: Cost, rank: i32, recall: Recall) {
        let cost = self.cost + extra;
        let rank = self.base + rank;
        match &mut self.out {
            Some(out) => {
                let kept = match out[self.from..].iter_mut().find(|c| c.end == end) {
                    Some(c) if (c.cost, c.rank) <= (cost, rank) => false,
                    Some(c) => {
                        (c.cost, c.rank) = (cost, rank);
                        true
                    }
                    None => {
                        out.push(Candidate { end, cost, rank, tag: String::new() });
                        true
                    }
                };
                if kept && let Some(stash) = &mut self.stash {
                    stash.push((self.start, end, cost, rank, recall));
                }
            }
            None => {
                if self.found.is_none() && self.want == (end, cost, rank) {
                    let mut s = String::with_capacity(64);
                    recall.write(t, &mut s);
                    self.found = Some(s);
                }
            }
        }
    }

    /// `name { body }`.
    #[inline]
    pub fn push(&mut self, end: usize, extra: Cost, rank: i32, name: &str, body: impl FnOnce(&mut String)) {
        self.raw(end, extra, rank, |s| {
            s.push_str(name);
            s.push_str(" { ");
            body(s);
            s.push_str(" }");
        });
    }
}

/// A stack-first growable list of `Copy` readings (frontiers and chain arenas; spills to the heap past `N`). The
/// inline slots are `Option`s so that creating one writes only their `None` tags, not `N` whole readings.
pub struct Buf<T, const N: usize> {
    arr: [Option<T>; N],
    n: usize,
    spill: Vec<T>,
}

impl<T: Copy, const N: usize> Buf<T, N> {
    pub fn new() -> Self {
        Buf { arr: [None; N], n: 0, spill: Vec::new() }
    }

    pub fn len(&self) -> usize {
        self.n + self.spill.len()
    }

    /// Appends `x`; returns its index.
    pub fn push(&mut self, x: T) -> usize {
        let i = self.len();
        if self.n < N {
            self.arr[self.n] = Some(x);
            self.n += 1;
        } else {
            self.spill.push(x);
        }
        i
    }

    pub fn pop(&mut self) -> Option<T> {
        if let Some(x) = self.spill.pop() {
            return Some(x);
        }
        if self.n == 0 {
            return None;
        }
        self.n -= 1;
        self.arr[self.n]
    }

    pub fn get(&self, i: usize) -> T {
        if i < self.n { self.arr[i].expect("filled slot") } else { self.spill[i - self.n] }
    }
}

impl<T: Copy, const N: usize> Default for Buf<T, N> {
    fn default() -> Self {
        Self::new()
    }
}

/// No parent in a chain arena.
pub const ROOT: u32 = u32::MAX;

/// `pynutil.insert(key + ": \"") + value + insert("\"")` appended to `out`.
pub fn field(out: &mut String, key: &str, value: &str) {
    out.push_str(key);
    out.push_str(": \"");
    out.push_str(value);
    out.push('"');
}

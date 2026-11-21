//! `text_normalization/ko` (NeMo r1.2.0), built `lower_cased` by the wrapper (ko's `ClassifyFst` ignores
//! `input_case`). Each tagger file enumerates every `(span, tag, cost)` its upstream FST accepts at a position.
//! The `classify` union is one classifier: a per-call scan gives every position a gate mask (necessary conditions
//! per branch), so positions no branch can match cost almost nothing; the frequent branches stream their spans
//! and write a tag only for the candidate on the best path (`Classifier::materialize`).

mod graph_utils;
mod number;
mod taggers;
mod verbalizers;

use std::cell::RefCell;
use std::sync::LazyLock;

use graph_utils::Outs;

use crate::lattice::{Candidate, Classifier, Shape};
use crate::permute;
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, cost, is_nemo_space};

/// Tie-break among equal-cost paths, read off the oracle (its choice falls out of the optimized FST's f32 arc
/// weights, so these rules are fitted to every golden tie):
/// - earlier token boundaries win: every token ranks `end * BOUNDARY` (`3개?` → `cardinal 3` + `name 개?`, not
///   `3개` counter + `?`; `131벌` → `1` + `31벌`, not `13` + `1벌`; `5원이에요` → `cardinal 5` + `name 원이에요`;
///   `Dh 9959242.05` → money `Dh 9` + decimal, `$3040341.09` → money `$30` + decimal);
/// - money ranks `-2 * start * BOUNDARY` extra, so the shortest money span wins against a longer one ending at
///   the same place (`[HK$3503` → `name "[HK"` + money `$3503` (달러), not `name "["` + money `HK$3503`);
/// - a number token consuming a leading `-`/`+` loses outright (`\-14/78` → `name "\-"` + fraction; `-290154s` →
///   whitelist `-` + measure; `+1365011.8` → whitelist `+` + decimal);
/// - money whose amount verbalizes to ≤ 2 chars and is not followed by a digit loses to the word + cardinal split
///   (`[chf18 이` → `name "[chf"` + `십팔`; `[KRW6[`, `[US$5[`, `[krw 8[` likewise; `[KRW63]` stays money);
/// - a two-digit native counter number `2x` right after a digit loses (`824포기` → `82` + `4포기`, `722사람`,
///   `925장`), while `3x` wins (`131벌` → `1` + `31벌`).
#[derive(Clone, Copy, PartialEq)]
enum Tie {
    Plain,
    /// May consume a sign (decimal, fraction, measure).
    Signed,
    Cardinal,
    Money,
}

const BOUNDARY: i32 = 10_000;
const LOSES: i32 = 1_000_000_000;

/// `flagged`: the tagger's mark for the output a class-specific rule may rank last (cardinal: a counter reading;
/// money: an amount of at most two chars not followed by a digit).
fn tie_rank(tie: Tie, text: &[char], start: usize, end: usize, flagged: bool) -> i32 {
    let mut rank = (end as i32).saturating_mul(BOUNDARY);
    match tie {
        Tie::Plain => {}
        Tie::Signed | Tie::Cardinal => {
            if matches!(text[start], '-' | '+') {
                rank = rank.saturating_add(LOSES);
            }
            if tie == Tie::Cardinal
                && flagged
                && start > 0
                && text[start - 1].is_ascii_digit()
                && text[start] == '2'
                && text.get(start + 1).is_some_and(char::is_ascii_digit)
            {
                rank = rank.saturating_add(LOSES);
            }
        }
        Tie::Money => {
            rank -= 2 * (start as i32) * BOUNDARY;
            if flagged {
                rank = rank.saturating_add(LOSES);
            }
        }
    }
    rank
}

enum Tagger {
    /// Streams `(end, cost, flagged, body)`; bodies are written only for the candidate on the best path.
    Stream(fn(&[char], usize, graph_utils::TagSink<'_>)),
    /// Returns every `(end, body, cost)` with the body built.
    Collect(fn(&[char], usize) -> Outs),
    /// `word`: spans only; the tag is the span.
    Word,
}

/// One weighted branch of `classify`.
struct Branch {
    tagger: Tagger,
    class: &'static str,
    cost: Cost,
    tie: Tie,
    /// This branch's bit in the gate mask.
    gate: u16,
}

/// Branch bits of a position's gate mask, in union order: a set bit is a necessary condition for that branch to
/// accept a span starting there (the taggers recheck what they need).
mod bit {
    pub const CARDINAL: u16 = 1 << 0;
    pub const DATE: u16 = 1 << 1;
    pub const TIME: u16 = 1 << 2;
    pub const FRACTION: u16 = 1 << 3;
    pub const ORDINAL: u16 = 1 << 4;
    pub const DECIMAL: u16 = 1 << 5;
    pub const WORD: u16 = 1 << 6;
    pub const MONEY: u16 = 1 << 7;
    pub const MEASURE: u16 = 1 << 8;
    pub const PUNCTUATION: u16 = 1 << 9;
    pub const WHITELIST: u16 = 1 << 10;
    pub const TELEPHONE: u16 = 1 << 11;
    pub const ELECTRONIC: u16 = 1 << 12;
}

/// Gate bits a non-digit start char opens on its own (ASCII array, sorted list for the rest).
struct LeadTable {
    ascii: [u16; 128],
    other: Vec<(char, u16)>,
}

impl LeadTable {
    fn get(&self, c: char) -> u16 {
        if c.is_ascii() {
            return self.ascii[c as usize];
        }
        self.other.binary_search_by_key(&c, |&(k, _)| k).map_or(0, |i| self.other[i].1)
    }
}

static LEAD: LazyLock<LeadTable> = LazyLock::new(|| {
    use taggers::*;
    let lists: [(u16, Vec<char>); 10] = [
        (bit::CARDINAL, vec!['-', '+', '(']),
        (bit::DATE, date::lead_chars()),
        (bit::TIME, time::lead_chars()),
        (bit::DECIMAL, vec!['-', '마']),
        (bit::MONEY, money::lead_chars()),
        (bit::MEASURE, vec!['-', '마', '√']),
        (bit::PUNCTUATION, vec!['〜', '부']),
        (bit::WHITELIST, whitelist::lead_chars()),
        (bit::TELEPHONE, vec!['+', '(']),
        (bit::ELECTRONIC, electronic::lead_chars()),
    ];
    let mut t = LeadTable { ascii: [0; 128], other: Vec::new() };
    for (b, chars) in lists {
        for c in chars {
            if c.is_ascii() {
                t.ascii[c as usize] |= b;
            } else {
                match t.other.iter_mut().find(|(k, _)| *k == c) {
                    Some(e) => e.1 |= b,
                    None => t.other.push((c, b)),
                }
            }
        }
    }
    t.other.sort_unstable();
    t
});

/// The gate mask at one position.
fn gate_mask(a: &At) -> u16 {
    use taggers::*;
    let mut m = 0;
    if a.flags & FRACTION != 0 {
        m |= bit::FRACTION;
    }
    if !a.digit {
        m |= LEAD.get(a.c);
        if !is_nemo_space(a.c) {
            m |= bit::WORD;
        } else if !matches!(a.t.get(graph_utils::run(a.t, a.p, is_nemo_space)), Some('(' | '（')) {
            // from whitespace a date is only a bracketed week
            m &= !bit::DATE;
        }
        if a.flags & DOTTED != 0 {
            m |= bit::ELECTRONIC;
        }
        return m;
    }
    m |= bit::CARDINAL;
    for (b, open) in [
        (bit::DATE, date::digit_gate(a)),
        (bit::TIME, time::digit_gate(a)),
        (bit::ORDINAL, a.u == Some('번')),
        (bit::DECIMAL, a.u == Some('.')),
        (bit::MONEY, money::digit_gate(a)),
        (bit::MEASURE, measure::digit_gate(a)),
        (bit::TELEPHONE, a.d == a.p + 3 && matches!(a.u, Some('-' | '.'))),
        (bit::ELECTRONIC, a.flags & DOTTED != 0 || (a.d == a.p + 4 && matches!(a.u, Some('-' | ' ')))),
    ] {
        if open {
            m |= b;
        }
    }
    m
}

/// [`At::flags`]: the non-space run from the position holds a `.` followed by an ASCII letter or digit (every
/// electronic span but the card ones: domains need `.` + ALNUM{2,}, extensions are `.` + letters).
pub const DOTTED: u8 = 1;
/// [`At::flags`]: a fraction may start here ([`taggers::fraction::possible`]).
pub const FRACTION: u8 = 2;
/// Internal to [`scan`]: the fraction-alphabet run from here ends at `/` or `분의`.
const RUN_OK: u8 = 4;

/// The per-call scan of the text being tagged: flags and the gate mask per position.
struct Scan {
    text: Vec<char>,
    addr: usize,
    flags: Vec<u8>,
    masks: Vec<u16>,
}

thread_local! {
    /// The lattice queries position 0 of every text first: the scan is checked against the text there (a new
    /// text may reuse the old one's address) and whenever the address or length changes; `materialize` runs on
    /// the same text afterwards and reuses it.
    static SCAN: RefCell<Scan> =
        const { RefCell::new(Scan { text: Vec::new(), addr: 0, flags: Vec::new(), masks: Vec::new() }) };
}

/// The gate mask of `t` at `p`.
fn mask_at(t: &[char], p: usize) -> u16 {
    SCAN.with(|s| {
        let mut s = s.borrow_mut();
        let same = s.addr == t.as_ptr() as usize && s.text.len() == t.len() && (p != 0 || s.text == t);
        if !same {
            s.addr = t.as_ptr() as usize;
            let Scan { text, flags, masks, .. } = &mut *s;
            text.clear();
            text.extend_from_slice(t);
            scan(t, flags, masks);
        }
        s.masks[p]
    })
}

/// Fill `flags` (one backward pass) and `masks` for every position of `t`.
fn scan(t: &[char], flags: &mut Vec<u8>, masks: &mut Vec<u16>) {
    use taggers::fraction;
    flags.clear();
    flags.resize(t.len() + 1, 0);
    for p in (0..t.len()).rev() {
        let (c, next) = (t[p], flags[p + 1]);
        let mut f = 0;
        let dot_alnum = c == '.' && t.get(p + 1).is_some_and(char::is_ascii_alphanumeric);
        if !is_nemo_space(c) && (dot_alnum || next & DOTTED != 0) {
            f |= DOTTED;
        }
        let run_ok = if fraction::in_run(c) { next & RUN_OK != 0 } else { fraction::run_end_ok(t, p) };
        if run_ok {
            f |= RUN_OK;
            if fraction::starts(c) {
                f |= FRACTION;
            }
        }
        flags[p] = f;
    }
    masks.clear();
    let mut d = 0;
    for p in 0..t.len() {
        if d <= p {
            d = graph_utils::run(t, p, |c| c.is_ascii_digit());
        }
        let e = d.max(p);
        masks.push(gate_mask(&At { t, p, c: t[p], digit: e > p, d: e, u: t.get(e).copied(), flags: flags[p] }));
    }
}

/// What the gates look at.
pub struct At<'a> {
    pub t: &'a [char],
    pub p: usize,
    pub c: char,
    pub digit: bool,
    /// End of the ASCII digit run from `p` (`p` when `c` is not a digit).
    pub d: usize,
    /// The char at `d`.
    pub u: Option<char>,
    /// [`DOTTED`], [`FRACTION`].
    pub flags: u8,
}

impl Branch {
    fn tag(&self, body: impl FnOnce(&mut String)) -> String {
        let mut tag = String::with_capacity(64);
        if !self.class.is_empty() {
            tag.push_str(self.class);
            tag.push_str(" { ");
        }
        body(&mut tag);
        if !self.class.is_empty() {
            tag.push_str(" }");
        }
        tag
    }

    /// Every candidate of this branch at `start` in the tagger's order, as `(end, cost, rank, tag)`; the caller
    /// builds the tag only for the candidate it keeps.
    fn each(
        &self,
        text: &[char],
        start: usize,
        index: usize,
        f: &mut dyn FnMut(usize, Cost, i32, &dyn Fn() -> String),
    ) {
        let rank = |end: usize, flagged: bool| {
            tie_rank(self.tie, text, start, end, flagged).saturating_add(index as i32 * BRANCH)
        };
        match self.tagger {
            Tagger::Stream(tag) => tag(text, start, &mut |end, c, flagged, body| {
                if end > start {
                    f(end, self.cost + c, rank(end, flagged), &|| self.tag(body));
                }
            }),
            Tagger::Collect(tag) => {
                for (end, body, c) in tag(text, start) {
                    if end > start {
                        f(end, self.cost + c, rank(end, false), &|| self.tag(|o| o.push_str(&body)));
                    }
                }
            }
            Tagger::Word => taggers::word::ends(text, start, |end| {
                f(end, self.cost, rank(end, false), &|| taggers::word::tag(text, start, end))
            }),
        }
    }
}

/// The rank step between union branches (the lattice's per-classifier step, kept from when each branch was its
/// own classifier; the fitted tie rules depend on it).
const BRANCH: i32 = 1000;

/// The `classify` union as one classifier: the gate mask skips the branches that cannot start at a position, and
/// tag bodies are written only for the candidates on the best path ([`Classifier::materialize`]).
struct Union {
    branches: Vec<Branch>,
}

impl Union {
    fn each(&self, text: &[char], start: usize, f: &mut dyn FnMut(usize, Cost, i32, &dyn Fn() -> String)) {
        // branch `i` has gate bit `1 << i`: visit the set bits in union order
        let mut mask = mask_at(text, start);
        while mask != 0 {
            let i = mask.trailing_zeros() as usize;
            self.branches[i].each(text, start, i, f);
            mask &= mask - 1;
        }
    }
}

impl Classifier for Union {
    /// Only the first lowest `(cost, rank)` candidate per end is pushed: from one start the lattice compares
    /// same-end candidates by exactly that key (their digit counts are equal) and keeps the first of equals.
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let base = out.len();
        self.each(text, start, &mut |end, cost, rank, _| match out[base..].iter_mut().find(|c| c.end == end) {
            Some(c) => {
                if (cost, rank) < (c.cost, c.rank) {
                    (c.cost, c.rank) = (cost, rank);
                }
            }
            None => out.push(Candidate { end, cost, rank, tag: String::new() }),
        });
    }

    /// The first candidate with the same `(end, cost, rank)`: the lattice keeps the first of equal-key candidates,
    /// so that is the one on the path. Only open branches whose rank formula can give the candidate's rank are
    /// re-run, in union order, until the tag is found.
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut mask = mask_at(text, start);
        let mut found = None;
        while mask != 0 && found.is_none() {
            let i = mask.trailing_zeros() as usize;
            mask &= mask - 1;
            let b = &self.branches[i];
            let rank = |flagged| tie_rank(b.tie, text, start, candidate.end, flagged).saturating_add(i as i32 * BRANCH);
            if rank(false) != candidate.rank && rank(true) != candidate.rank {
                continue;
            }
            b.each(text, start, i, &mut |end, cost, rank, tag| {
                if found.is_none() && (end, cost, rank) == (candidate.end, candidate.cost, candidate.rank) {
                    found = Some(tag());
                }
            });
        }
        candidate.tag = found.expect("materialize: the candidate was pushed at this start");
    }
}

struct Ko {
    classifiers: Vec<Box<dyn Classifier>>,
}

fn branches() -> Vec<Branch> {
    use Tagger::{Collect, Stream, Word};
    use taggers::*;
    let b = |tagger: Tagger, class: &'static str, w: f64, tie: Tie, gate: u16| Branch {
        tagger,
        class,
        cost: cost(w),
        tie,
        gate,
    };
    vec![
        b(Stream(cardinal::tag), "", 1.1, Tie::Cardinal, bit::CARDINAL),
        b(Stream(date::tag), "date", 1.1, Tie::Plain, bit::DATE),
        b(Stream(time::tag), "time", 1.1, Tie::Plain, bit::TIME),
        b(Collect(fraction::tag), "fraction", 1.0, Tie::Signed, bit::FRACTION),
        b(Collect(ordinal::tag), "ordinal", 1.1, Tie::Plain, bit::ORDINAL),
        b(Collect(decimal::tag), "decimal", 1.05, Tie::Signed, bit::DECIMAL),
        b(Word, "", 100.0, Tie::Plain, bit::WORD),
        b(Stream(money::tag), "money", 1.1, Tie::Money, bit::MONEY),
        b(Collect(measure::tag), "measure", 1.1, Tie::Signed, bit::MEASURE),
        b(Stream(punctuation::tag), "", 1.0, Tie::Plain, bit::PUNCTUATION),
        b(Collect(whitelist::tag), "", 1.1, Tie::Plain, bit::WHITELIST),
        b(Collect(telephone::tag), "telephone", 1.1, Tie::Plain, bit::TELEPHONE),
        b(Collect(electronic::tag), "electronic", 1.11, Tie::Plain, bit::ELECTRONIC),
    ]
}

fn build() -> Ko {
    let branches = branches();
    debug_assert!(branches.iter().enumerate().all(|(i, b)| b.gate == 1 << i), "gate bits follow union order");
    Ko { classifiers: vec![Box::new(Union { branches })] }
}

static GRAMMAR: LazyLock<Ko> = LazyLock::new(build);

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

impl Grammar for Ko {
    fn code(&self) -> &'static str {
        "ko"
    }
    fn shape(&self) -> Shape {
        Shape::Ko
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        None
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalizers::verbalize(token)
    }
    /// The default (each token's first verbalizable ordering, joined with `" "`), written into one buffer.
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i > 0 {
                out.push(' ');
            }
            permute::first_ordering(body, &mut |o: &Dict| verbalizers::verbalize_into(o, &mut out).then_some(()))?;
        }
        Some(out)
    }
}

#[cfg(test)]
mod tests {
    /// Oracle-pinned tie-breaks (see [`super::Tie`]); each input names the alternative the oracle rejected.
    #[test]
    fn oracle_ties() {
        let g = super::grammar();
        for (input, want) in [
            // name "\-" + fraction, not name "\" + negative fraction
            ("\\-50/23\\", "\\-이십삼분의 오십\\"),
            // whitelist "-" + measure 290154s, not cardinal -29015 + measure 4s
            ("-290154s", "마이너스 이십구만백오십사 초"),
            // name "[HK" + money $3503, not name "[" + money HK$3503
            ("[HK$3503 오늘", "[HK 삼천오백삼 달러 오늘"),
            // name "[chf" + cardinal 18, not name "[" + money chf18
            ("[chf18 이", "[chf 십팔 이"),
            // cardinal 5 + name 원이에요, not money 5원 + name 이에요
            ("5원이에요", "오 원이에요"),
            // money Dh 9 + decimal, not money Dh 99 + decimal
            ("Dh 9959242.05", "구 디르함 구십오만구천이백사십이점영오"),
            // 82 + 4포기, not 8 + 24포기; but 1 + 31벌, not 13 + 1벌
            ("824포기", "팔십이 네포기"),
            ("131벌", "일 서른한벌"),
        ] {
            assert_eq!(crate::pipeline::normalize(g, input, true).as_deref(), Ok(want), "{input}");
        }
    }
}

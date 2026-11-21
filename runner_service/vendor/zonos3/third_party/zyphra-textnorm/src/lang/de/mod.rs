//! `text_normalization/de` (NeMo r1.2.0, deterministic, cased): `ClassifyFst` + `VerbalizeFinalFst`.

pub mod taggers {
    pub mod cardinal;
    pub mod date;
    pub mod decimal;
    pub mod electronic;
    pub mod fraction;
    pub mod measure;
    pub mod money;
    pub mod ordinal;
    pub mod telephone;
    pub mod time;
    pub mod whitelist;
    pub mod word;
}
mod crossing;
pub mod verbalizers;

use std::sync::LazyLock;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, cost, is_nemo_space};

struct German {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

/// `VerbalizeFst | WordFst` on one token body: the cheapest output with its weight.
fn verbalize_body(token: &Dict) -> Option<(String, Cost)> {
    if let Some(s) = common::verbalize_name(token) {
        return Some((s, 0));
    }
    let (class, Value::Dict(fields)) = common::class(token)? else { return None };
    match class {
        "cardinal" => verbalizers::cardinal::verbalize(fields),
        "measure" => verbalizers::measure::verbalize(fields),
        "decimal" => verbalizers::decimal::verbalize(fields),
        "ordinal" => verbalizers::ordinal::verbalize(fields),
        "date" => verbalizers::date::verbalize(fields),
        "electronic" => verbalizers::electronic::verbalize(fields),
        "money" => verbalizers::money::verbalize(fields),
        "fraction" => verbalizers::fraction::verbalize(fields),
        "telephone" => verbalizers::telephone::verbalize(fields),
        "time" => verbalizers::time::verbalize(fields),
        _ => None,
    }
}

impl Grammar for German {
    fn code(&self) -> &'static str {
        "de"
    }
    fn shape(&self) -> Shape {
        Shape::De
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalize_body(token).map(|(s, _)| if s.contains('\u{a0}') { s.replace('\u{a0}', " ") } else { s })
    }
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        if split.iter().filter(|t| fraction_fields(t).is_some()).count() < 2 {
            let mut out = String::with_capacity(16 * split.len());
            for (i, t) in split.iter().enumerate() {
                let [(k, Value::Dict(body))] = t.as_slice() else { return None };
                if k != "tokens" {
                    return None;
                }
                if i > 0 {
                    out.push(' ');
                }
                out.push_str(&crate::permute::first_ordering(body, &mut |o: &Dict| self.verbalize(o))?);
            }
            return Some(out);
        }
        crate::permute::split_orderings(split, &mut |ordering: &[Dict]| crossing::verbalize(ordering))
    }
}

/// The fraction fields of a parsed `[("tokens", {"fraction": {...}})]` item.
fn fraction_fields<'t, 'a>(t: &'t Dict<'a>) -> Option<&'t Dict<'a>> {
    let [(k, Value::Dict(body))] = t.as_slice() else { return None };
    if k != "tokens" {
        return None;
    }
    match body.as_slice() {
        [(c, Value::Dict(f))] if c == "fraction" => Some(f),
        _ => None,
    }
}

/// Whether some non-word item can start at `e` inside a non-space run: a digit, a sign, or any position after or at a
/// non-alphanumeric char.
pub fn item_may_start(text: &[char], e: usize) -> bool {
    let c = text[e];
    let p = text[e - 1];
    c.is_ascii_digit() || !c.is_alphanumeric() || !p.is_alphanumeric() || p.is_ascii_digit()
}

/// A classifier body that builds tag strings only on request. `spans` pushes through a [`Sink`]: while the lattice
/// explores, every candidate goes out with an empty tag; [`Classify::materialize`] runs `spans` again at the winner's
/// start in find mode, where the first candidate with the winner's `(end, cost)` builds its tag. The pushes (ends,
/// costs, order) must not depend on the mode.
pub trait Spans: Send + Sync {
    /// Whether any span may start on `c` (a cheap gate before `spans`).
    fn may_start(&self, c: char) -> bool;
    fn spans(&self, text: &[char], start: usize, out: &mut Sink);
}

/// Where [`Spans::spans`] pushes: the lattice's candidate list (no tags) or the search for one candidate's tag.
pub enum Sink<'a> {
    Explore(&'a mut Vec<Candidate>),
    Find { end: usize, cost: Cost, tag: Option<String> },
}

impl Sink<'_> {
    /// One accepted span; `tag` runs only for the candidate being materialized.
    #[inline]
    pub fn push(&mut self, end: usize, cost: Cost, tag: impl FnOnce() -> String) {
        match self {
            Sink::Explore(out) => out.push(Candidate { end, cost, rank: 0, tag: String::new() }),
            Sink::Find { end: e, cost: c, tag: t } => {
                if t.is_none() && *e == end && *c == cost {
                    *t = Some(tag());
                }
            }
        }
    }
}

/// One branch of `classify`: its spans, whether a glued leading `-` loses (`sign`), whether it may start on a letter
/// glued to a letter or digit (`glued`).
struct Branch {
    spans: Box<dyn Spans>,
    sign: bool,
    glued: bool,
}

/// The `classify` union as one lattice classifier (one call per position).
///
/// Tie-break fitted to the oracle (its choice among equal-cost paths falls out of the optimized FST's arc order):
/// earlier token boundaries win, so every candidate ranks by its end (`7.545,9 billionen` → ordinal `7.` + decimal,
/// not date `7.5` + decimal; `57 mro` → cardinal + word, not measure `57 m` + word `ro`; `₩3884906 trillion` →
/// money `₩3884` + decimal `906 trillion`), then by its branch's source position. Ranks depend only on the start, the
/// end and the branch, so `(end, cost)` searched in branch order identifies the lattice's pick (the first pushed among
/// equals) for `materialize`.
struct Classify {
    branches: Vec<Branch>,
    /// Per ASCII char, the branches whose `may_start` accepts it (bit = branch index).
    ascii: [u16; 128],
}

impl Classify {
    fn new(branches: Vec<Branch>) -> Classify {
        assert!(branches.len() <= 16);
        let mut ascii = [0u16; 128];
        for (c, m) in ascii.iter_mut().enumerate() {
            for (bi, b) in branches.iter().enumerate() {
                if b.spans.may_start(c as u8 as char) {
                    *m |= 1 << bi;
                }
            }
        }
        Classify { branches, ascii }
    }
}

impl Classifier for Classify {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            crossing_reset(text);
        }
        // every item starts on a non-space char
        if is_nemo_space(text[start]) {
            return;
        }
        // no non-word item starts on a letter glued to a preceding letter or digit (`3D-Drucker`, `mro6.035` start
        // at their first char)
        let alnum = |c: char| if c.is_ascii() { c.is_ascii_alphanumeric() } else { c.is_alphanumeric() };
        let glued = start > 0 && alnum(text[start - 1]) && {
            let c = text[start];
            if c.is_ascii() { c.is_ascii_alphabetic() } else { c.is_alphabetic() }
        };
        // a number glued to punctuation leaves its `-` to the punctuation run (`[-45/19` → `name "[-"` + fraction,
        // `{-90,15` → `name "{-"` + decimal; after whitespace `-53` stays negative; measure keeps its sign: `[-231μs`,
        // `--64,8mc` → `name "-"` + negative measure)
        let glued_sign = text[start] == '-' && start > 0 && !is_nemo_space(text[start - 1]);
        let c0 = text[start];
        let mask = if c0.is_ascii() { self.ascii[c0 as usize] } else { u16::MAX };
        let first = out.len();
        for (bi, b) in self.branches.iter().enumerate() {
            if mask & (1 << bi) == 0 || (glued && !b.glued) || (!c0.is_ascii() && !b.spans.may_start(c0)) {
                continue;
            }
            let n = out.len();
            b.spans.spans(text, start, &mut Sink::Explore(out));
            let loses = if b.sign && glued_sign { LOSES } else { 0 };
            for c in &mut out[n..] {
                c.rank =
                    (c.end as i32).saturating_mul(BOUNDARY).saturating_add(loses).saturating_add(bi as i32 * BRANCH);
            }
        }
        if out.len() > first {
            let run_end = text[start..].iter().position(|&c| is_nemo_space(c)).map_or(text.len(), |i| start + i);
            CROSSING.with(|cr| {
                let ends = &mut cr.borrow_mut().1;
                for c in &out[first..] {
                    if c.end > run_end {
                        ends[c.end] = true;
                    }
                }
            });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let find = |b: &Branch| {
            let mut sink = Sink::Find { end: candidate.end, cost: candidate.cost, tag: None };
            b.spans.spans(text, start, &mut sink);
            match sink {
                Sink::Find { tag, .. } => tag,
                Sink::Explore(_) => None,
            }
        };
        // the rank holds the branch (unless it saturated on a very long text): try that branch first
        let bi = (candidate.rank as i64 - candidate.end as i64 * BOUNDARY as i64) % LOSES as i64 / BRANCH as i64;
        let tag = usize::try_from(bi)
            .ok()
            .and_then(|bi| self.branches.get(bi))
            .and_then(find)
            .or_else(|| self.branches.iter().find_map(find))
            .expect("materialize: candidate not pushed again");
        candidate.tag = tag;
    }
}

const BOUNDARY: i32 = 10_000;
/// The lattice's per-classifier rank step (`classifier index × 1000`), applied here per branch.
const BRANCH: i32 = 1000;
const LOSES: i32 = 1_000_000_000;

thread_local! {
    /// The text being tagged and, per position, whether an item crossing whitespace ends there (filled as the lattice
    /// explores positions left to right; reset at position 0 of a new text).
    static CROSSING: std::cell::RefCell<(Vec<char>, Vec<bool>)> = const { std::cell::RefCell::new((Vec::new(), Vec::new())) };
}

fn crossing_reset(text: &[char]) {
    CROSSING.with(|c| {
        let (t, ends) = &mut *c.borrow_mut();
        if t.as_slice() != text {
            t.clear();
            t.extend_from_slice(text);
            ends.clear();
            ends.resize(text.len() + 1, false);
        }
    });
}

/// Whether an item crossing whitespace and ending at `e` was pushed at an earlier position of the text being tagged.
pub fn crossing_ends_at(e: usize) -> bool {
    CROSSING.with(|c| c.borrow().1.get(e).copied().unwrap_or(false))
}

/// Whether an item crossing the whitespace after the non-space run `text[e..run_end]` may start at `e`. Every such
/// item starts with its number (a digit, `-`, or telephone's `+`/`(`), a currency key, or measure's `alpha-` before
/// a decimal with a quantity.
pub fn crossing_may_start(text: &[char], e: usize, run_end: usize) -> bool {
    if run_end == text.len() {
        return false;
    }
    let c = text[e];
    if c.is_ascii_digit() || matches!(c, '-' | '+' | '(') || taggers::money::currency_at(text, e) {
        return true;
    }
    let a = text[e..run_end].iter().position(|c| !c.is_ascii_alphabetic()).map_or(run_end, |i| e + i);
    a > e && text.get(a) == Some(&'-') && text.get(a + 1).is_some_and(char::is_ascii_digit)
}

static GRAMMAR: LazyLock<German> = LazyLock::new(|| {
    use taggers::*;
    let b = |spans: Box<dyn Spans>, sign: bool, glued: bool| Branch { spans, sign, glued };
    German {
        // source order of `classify` in de/taggers/tokenize_and_classify.py; weights inside each classifier
        classifiers: vec![Box::new(Classify::new(vec![
            b(Box::new(whitelist::WhiteList), true, false),
            b(Box::new(time::Time), true, false),
            b(Box::new(measure::Measure), false, false),
            b(Box::new(cardinal::Cardinal), true, false),
            b(Box::new(fraction::Fraction), true, false),
            b(Box::new(date::Date), true, false),
            b(Box::new(ordinal::Ordinal), true, false),
            b(Box::new(decimal::Decimal), true, false),
            b(Box::new(money::Money), true, false),
            b(Box::new(telephone::Telephone), true, false),
            b(Box::new(electronic::Electronic), true, false),
            b(Box::new(word::Word), true, true),
        ]))],
        punct: Punctuation::en(cost(1.1)),
    }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

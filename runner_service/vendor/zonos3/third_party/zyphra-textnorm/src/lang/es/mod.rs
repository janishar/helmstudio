//! `text_normalization/es` (NeMo r1.2.0, deterministic, cased, LOCALIZATION "eu").

pub mod graph_utils;
pub mod taggers;
pub mod verbalizers;

use std::sync::LazyLock;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, Row, Table, cost};

struct Es {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for Es {
    fn code(&self) -> &'static str {
        "es"
    }
    fn shape(&self) -> Shape {
        Shape::En
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(("name", _)) = common::class(token) {
            return common::verbalize_name(token);
        }
        let (k, Value::Dict(d)) = common::class(token)? else {
            return None;
        };
        use verbalizers as v;
        let out = match k {
            "cardinal" => v::cardinal::verbalize(d),
            "measure" => v::measure::verbalize(d),
            "decimal" => v::decimals::verbalize(d),
            "ordinal" => v::ordinal::verbalize(d),
            "date" => v::date::verbalize(d),
            "electronic" => v::electronic::verbalize(d),
            "money" => v::money::verbalize(d),
            "fraction" => v::fraction::verbalize(d),
            "telephone" => v::telephone::verbalize(d),
            "time" => v::time::verbalize(d),
            _ => None,
        }?;
        // delete_tokens maps NBSP → space
        Some(if out.contains('\u{a0}') { out.replace('\u{a0}', " ") } else { out })
    }
}

/// `ok_end` for ASCII chars (bit `c`): `NEMO_WHITE_SPACE` or an en punctuation mark (checked in `ends_tests`).
const ASCII_ENDS: u128 = 0x2800000138000000dc00738700002600;

/// Whether a token may end at `e`: in the `En` shape a token is followed by the end, whitespace or a punctuation
/// mark (never by another token), so spans ending elsewhere can never be on a path and are not emitted.
pub fn ok_end(text: &[char], e: usize) -> bool {
    let Some(&c) = text.get(e) else { return true };
    if (c as u32) < 128 {
        return ASCII_ENDS >> (c as u32) & 1 == 1;
    }
    crate::text::is_nemo_space(c) || GRAMMAR.punct.is_mark(c)
}

/// Where a classifier pushes its readings. Exploration records only `(end, cost, rank)`; the `materialize` re-run
/// looks for one reading (`target`) and builds the tag of the first match only.
pub struct Sink<'a> {
    out: &'a mut Vec<Candidate>,
    /// `(end, cost, rank)` of the best-path reading being materialized.
    target: Option<(usize, Cost, i32)>,
    found: Option<String>,
}

impl Sink<'_> {
    #[inline]
    pub fn push(&mut self, end: usize, cost: Cost, rank: i32, tag: impl FnOnce() -> String) {
        match self.target {
            None => self.out.push(Candidate { end, cost, rank, tag: String::new() }),
            Some(t) => {
                if self.found.is_none() && t == (end, cost, rank) {
                    self.found = Some(tag());
                }
            }
        }
    }

    /// Whether this is the exploration run (not the `materialize` re-run): per-text memos reset here.
    pub fn exploring(&self) -> bool {
        self.target.is_none()
    }
}

/// [`Table::prefix_matches`] on a static table, with the rows' `'static` lifetime (the trie borrows `text` only
/// for the walk).
pub fn prefix_static<'t>(
    table: &'static Table,
    text: &'t [char],
    start: usize,
) -> impl Iterator<Item = (&'static Row, usize)> + 't {
    let rows = table.rows();
    table.prefix_matches(text, start).map(move |(r, e)| {
        let i = (r as *const Row as usize - rows.as_ptr() as usize) / std::mem::size_of::<Row>();
        (&rows[i], e)
    })
}

/// The first chars of a set of keys, for a test that rejects most positions before any trie walk.
pub struct FirstChars {
    ascii: u128,
    /// Sorted.
    other: Vec<char>,
}

impl FirstChars {
    pub fn of<'a>(keys: impl IntoIterator<Item = &'a str>) -> FirstChars {
        let mut f = FirstChars { ascii: 0, other: Vec::new() };
        for c in keys.into_iter().filter_map(|k| k.chars().next()) {
            f.add(c);
        }
        f.other.sort_unstable();
        f.other.dedup();
        f
    }

    pub fn of_table(t: &Table) -> FirstChars {
        FirstChars::of(t.rows().iter().map(|r| r.key.as_str()))
    }

    fn add(&mut self, c: char) {
        if c.is_ascii() { self.ascii |= 1 << c as u32 } else { self.other.push(c) }
    }

    #[inline]
    pub fn has(&self, c: char) -> bool {
        if c.is_ascii() { self.ascii >> c as u32 & 1 == 1 } else { self.other.binary_search(&c).is_ok() }
    }

    /// Whether some key could start at `text[p]`.
    #[inline]
    pub fn at(&self, text: &[char], p: usize) -> bool {
        text.get(p).is_some_and(|&c| self.has(c))
    }
}

/// A tagger branch whose readings go to a [`Sink`].
pub trait Lazy: Send + Sync {
    fn run(&self, text: &[char], start: usize, out: &mut Sink);
    /// The tag of the first reading pushed at `start` with this `(end, cost, rank)`, from what the exploration run
    /// recorded ([`Stash`]); `None` makes `materialize` re-run the branch.
    fn recall(&self, _text: &[char], _start: usize, _end: usize, _cost: Cost, _rank: i32) -> Option<String> {
        None
    }
}

/// The readings a branch pushed during exploration of the current text, in push order, so the best-path ones get
/// their tags without re-running the branch. Cleared at position 0 of every text ([`taggers::reset`]).
pub struct Stash<T>(std::cell::RefCell<Vec<(usize, usize, Cost, i32, T)>>);

impl<T: Copy> Stash<T> {
    pub const fn new() -> Self {
        Stash(std::cell::RefCell::new(Vec::new()))
    }

    pub fn clear(&self) {
        self.0.borrow_mut().clear();
    }

    pub fn put(&self, start: usize, end: usize, cost: Cost, rank: i32, t: T) {
        self.0.borrow_mut().push((start, end, cost, rank, t));
    }

    /// The first reading recorded with this key.
    pub fn find(&self, start: usize, end: usize, cost: Cost, rank: i32) -> Option<T> {
        let key = (start, end, cost, rank);
        self.0.borrow().iter().find(|&&(s, e, c, r, _)| (s, e, c, r) == key).map(|x| x.4)
    }
}

/// The `classify` union as one lattice classifier (the members in source order). In the `En` shape a token starts
/// at the text start, after whitespace or after a punctuation mark and ends before one of them (never next to another
/// token), so spans violating that are neither explored nor pushed. Each member's readings carry
/// `index * 1000 + rank`, the rank the lattice would assign to the members listed separately (every member rank is
/// below 1000). Tags are built only for best-path readings: `materialize` takes the member's recorded reading
/// ([`Lazy::recall`]) or re-runs the member (`rank / 1000`), which pushes the same readings in the same order; the
/// first one with the item's `(end, cost, rank)` is the one the lattice kept (it keeps the first of equal keys).
struct Union {
    members: [&'static dyn Lazy; 12],
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            // a new text: per-text memos and stashes start over
            taggers::reset(text.len());
        } else if !ok_end(text, start - 1) {
            return;
        }
        for (index, member) in self.members.iter().enumerate() {
            let n = out.len();
            member.run(text, start, &mut Sink { out: &mut *out, target: None, found: None });
            let mut k = n;
            for i in n..out.len() {
                if ok_end(text, out[i].end) {
                    out[i].rank += index as i32 * 1000;
                    out.swap(k, i);
                    k += 1;
                }
            }
            out.truncate(k);
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let index = (candidate.rank / 1000) as usize;
        let rank = candidate.rank - index as i32 * 1000;
        let member = self.members[index];
        if let Some(tag) = member.recall(text, start, candidate.end, candidate.cost, rank) {
            candidate.tag = tag;
            return;
        }
        let mut none = Vec::new();
        let mut sink = Sink { out: &mut none, target: Some((candidate.end, candidate.cost, rank)), found: None };
        member.run(text, start, &mut sink);
        candidate.tag = sink.found.expect("materialize: the re-run pushes the explored reading");
    }
}

static GRAMMAR: LazyLock<Es> = LazyLock::new(|| {
    use taggers::*;
    Es {
        // source order of `classify` in tokenize_and_classify.py; weights inside each classifier
        classifiers: vec![Box::new(Union {
            members: [
                &whitelist::Whitelist,
                &time::Time,
                &measure::Measure,
                &cardinal::Cardinal,
                &fraction::Fraction,
                &date::Date,
                &ordinal::Ordinal,
                &decimals::Decimal,
                &money::Money,
                &telephone::Telephone,
                &electronic::Electronic,
                &word::Word,
            ],
        })],
        punct: Punctuation::en(cost(2.1)),
    }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

#[cfg(test)]
mod ends_tests {
    #[test]
    fn ascii_ends_mask_matches_punctuation() {
        let mask = (0..128u8)
            .filter(|&c| crate::text::is_nemo_space(c as char) || super::GRAMMAR.punct.is_mark(c as char))
            .fold(0u128, |m, c| m | 1 << c);
        assert_eq!(mask, super::ASCII_ENDS);
    }
}

#[cfg(test)]
pub(crate) mod test_util {
    /// The tagger's chosen tagging of `s` (the oracle's `select_tag`).
    pub fn tags(s: &str) -> String {
        let chars: Vec<char> = s.chars().collect();
        crate::pipeline::tagged_string(super::grammar(), &chars).unwrap()
    }

    /// The full grammar output of `s` (no punctuation post-processing: es has none).
    pub fn normalize(s: &str) -> String {
        crate::pipeline::normalize(super::grammar(), s, false).unwrap()
    }
}

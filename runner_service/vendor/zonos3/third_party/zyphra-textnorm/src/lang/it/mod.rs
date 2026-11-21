//! `text_normalization/it` (NeMo r1.2.0), cased, deterministic. The es helpers it imports
//! (`es/graph_utils.py`: `cardinal_separator` `.`/space, `decimal_separator` `,`, `strip_cardinal_apocope`,
//! `shift_cardinal_gender`) are reproduced privately in `taggers/cardinal.rs` and `taggers/decimals.rs`.

mod taggers;
mod verbalizers;

use std::sync::LazyLock;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use crate::text::cost;

struct It {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for It {
    fn code(&self) -> &'static str {
        "it"
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
        verbalizers::verbalize(token)
    }
}

/// `it/taggers/tokenize_and_classify.py`.
static GRAMMAR: LazyLock<It> = LazyLock::new(|| {
    // Rank order (equal-cost tie-break, lower first) differs from the source union order where the oracle shows
    // a stable preference: time over cardinal (`10` → `time { hours: "10" }`); cardinal over money
    // (`la$ 215 $la` → word `la$` + cardinal); money over electronic (`eur2.772.907` → money, not a domain); cardinal over
    // electronic ("number" ties: the oracle splits `687.346.382` → cardinal vs `413.969` → domain at equal cost).
    // `true`: the member gets the digit-boundary tie-break.
    let members: Vec<(Box<dyn Classifier>, bool)> = vec![
        (Box::new(taggers::whitelist::Whitelist { cost: cost(0.0) }), false),
        (Box::new(taggers::time::Time { cost: cost(1.1) }), true),
        (Box::new(taggers::cardinal::Cardinal { cost: cost(1.1) }), true),
        (Box::new(taggers::decimals::Decimal { cost: cost(1.1) }), true),
        (Box::new(taggers::money::Money { cost: cost(1.1) }), true),
        (Box::new(taggers::electronic::Electronic { cost: cost(1.1) }), true),
        (Box::new(taggers::measure::Measure { cost: cost(1.1) }), true),
        (Box::new(taggers::word::Word { cost: cost(100.0) }), false),
    ];
    It { classifiers: vec![Box::new(Union { members })], punct: Punctuation::en(cost(1.1)) }
});

/// The `classify` union as one lattice classifier: member `i`'s candidates carry `i * 1000 + rank`, exactly the
/// rank the lattice gives the members listed separately. Tags are written only for best-path items
/// ([`taggers::with_tags`]).
struct Union {
    members: Vec<(Box<dyn Classifier>, bool)>,
}

impl Union {
    /// Member `index`'s candidates at `start`, with their union ranks.
    fn member(&self, index: usize, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let (member, boundary) = &self.members[index];
        let first = out.len();
        member.candidates(text, start, out);
        for c in &mut out[first..] {
            c.rank += index as i32 * 1000;
            // Equal-cost tie-break ("number" reason): a token that stops inside a digit run (`+90 599 797 2996`
            // read as `90 599 797 299` + `6`) loses against one that stops at the run's end (`90 599 797` +
            // `2996`).
            if *boundary
                && c.end > start
                && text[c.end - 1].is_ascii_digit()
                && text.get(c.end).is_some_and(char::is_ascii_digit)
            {
                c.rank += 100_000;
            }
        }
    }
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // the lattice queries position 0 first: a new input
        if start == 0 {
            taggers::decimals::reset(text.len());
            taggers::money::reset(text);
        }
        // no member accepts a span starting with whitespace (whitelist keys, currency keys and units never do;
        // word, time, cardinal, decimal, electronic and measure need a non-space first char)
        if crate::text::is_nemo_space(text[start]) {
            return;
        }
        for index in 0..self.members.len() {
            self.member(index, text, start, out);
        }
    }

    /// Re-runs the member that pushed the candidate (its index is the rank's thousands; members' own ranks lie in
    /// -1..=501) with tags on and takes the first push with the same end, cost and rank: the one the lattice kept.
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let index = ((candidate.rank % 100_000 + 1) / 1000) as usize;
        let (member, _) = &self.members[index];
        let mut all = Vec::new();
        taggers::with_tags(candidate.end, || self.member(index, text, start, &mut all));
        let c = all
            .iter_mut()
            .find(|c| c.end == candidate.end && c.cost == candidate.cost && c.rank == candidate.rank)
            .expect("materialized candidate is pushed again");
        if c.tag.is_empty() {
            // a member that writes its tag in `materialize` (word)
            member.materialize(text, start, c);
        }
        candidate.tag = std::mem::take(&mut c.tag);
    }
}

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

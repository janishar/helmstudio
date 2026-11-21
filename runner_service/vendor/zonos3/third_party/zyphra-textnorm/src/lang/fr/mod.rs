//! `text_normalization/fr` (NeMo r1.2.0, deterministic, cased): `ClassifyFst` + `VerbalizeFinalFst`.

pub mod taggers {
    pub mod cardinal;
    pub mod date;
    pub mod decimals;
    pub mod fraction;
    pub mod ordinal;
    pub mod whitelist;
    pub mod word;
}
mod lazy;
pub mod verbalizers {
    pub mod cardinal;
    pub mod date;
    pub mod decimals;
    pub mod fraction;
    pub mod ordinal;
}

use std::sync::LazyLock;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::cost;

struct French {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punct,
}

/// `PunctuationFst` with a reject for ASCII letters, digits and spaces (never marks, never start an emphasis tag).
struct Punct(Punctuation);

impl Classifier for Punct {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let c = text[start];
        if c.is_ascii_alphanumeric() || c == ' ' {
            return;
        }
        self.0.candidates(text, start, out);
    }
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        self.0.materialize(text, start, c);
    }
}

impl Grammar for French {
    fn code(&self) -> &'static str {
        "fr"
    }
    fn shape(&self) -> Shape {
        Shape::Fr
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        if let Some(s) = common::verbalize_name(token) {
            return Some(s);
        }
        let (class, Value::Dict(fields)) = common::class(token)? else {
            return None;
        };
        match class {
            "cardinal" => verbalizers::cardinal::verbalize(fields),
            "decimal" => verbalizers::decimals::verbalize(fields),
            "ordinal" => verbalizers::ordinal::verbalize(fields),
            "fraction" => verbalizers::fraction::verbalize(fields),
            "date" => verbalizers::date::verbalize(fields),
            _ => None,
        }
    }
}

/// The `classify` union (source order of fr/taggers/tokenize_and_classify.py) as one lattice classifier, so a
/// position pays one call instead of one per member and members that cannot start at its char are skipped. Each
/// member's candidates carry `index * 1000 + rank`, exactly the rank the lattice would assign to the members listed
/// separately (the lattice adds `0 * 1000` for this single entry), in the same order.
struct Union;

/// Members in union order.
const WHITELIST: i32 = 0;
const DATE: i32 = 1;
const CARDINAL: i32 = 2;
const FRACTION: i32 = 3;
const ORDINAL: i32 = 4;
const DECIMAL: i32 = 5;
const WORD: i32 = 6;

impl Union {
    /// The member `index` (one of the constants above).
    fn member(&self, index: i32) -> &'static dyn Classifier {
        match index {
            WHITELIST => &taggers::whitelist::WhiteList,
            DATE => &taggers::date::Date,
            CARDINAL => &taggers::cardinal::Cardinal,
            FRACTION => &taggers::fraction::Fraction,
            ORDINAL => &taggers::ordinal::Ordinal,
            DECIMAL => &taggers::decimals::Decimal,
            _ => &taggers::word::Word,
        }
    }

    /// Members that can accept a span starting with `c`, in union order: date spans start with a digit or "le "/
    /// "les "; cardinal, fraction and decimal spans with a digit or `-`; ordinal spans with a digit.
    fn members(c: char) -> &'static [i32] {
        match c {
            '0'..='9' => &[WHITELIST, DATE, CARDINAL, FRACTION, ORDINAL, DECIMAL, WORD],
            '-' => &[WHITELIST, CARDINAL, FRACTION, DECIMAL, WORD],
            'l' => &[WHITELIST, DATE, WORD],
            _ => &[WHITELIST, WORD],
        }
    }
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        for &index in Self::members(text[start]) {
            let from = out.len();
            self.member(index).candidates(text, start, out);
            for c in &mut out[from..] {
                c.rank += index * 1000;
            }
        }
    }

    /// The lazy members (date, cardinal, decimal) push ranks in `0..1000`, so `rank / 1000` names the member; the word
    /// is the only member at its cost. Eager members already wrote the tag.
    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        if !c.tag.is_empty() {
            return;
        }
        let index = if c.cost == taggers::word::WEIGHT { WORD } else { c.rank.div_euclid(1000) };
        c.rank -= index * 1000;
        self.member(index).materialize(text, start, c);
        c.rank += index * 1000;
    }
}

static GRAMMAR: LazyLock<French> =
    LazyLock::new(|| French { classifiers: vec![Box::new(Union)], punct: Punct(Punctuation::en(cost(2.1))) });

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

#[cfg(test)]
mod tests {
    use crate::pipeline::normalize;

    /// `premier | cardinal_graph` tie on day "1": oracle picks the cardinal ("1-1-1843" →
    /// `day: "un"`, alternative `day: "premier"`).
    #[test]
    fn day_one_is_cardinal() {
        assert_eq!(
            normalize(super::grammar(), "1-1-1843", false).unwrap(),
            "un janvier mille huit cent quarante-trois"
        );
    }

    /// Verbalizer `"un" -> "prem"` at -0.0001 beats the plain "un" + suffix ("1e" → "premième", not "unième").
    #[test]
    fn ordinal_un_is_prem() {
        assert_eq!(normalize(super::grammar(), "1e", false).unwrap(), "premième");
    }
}

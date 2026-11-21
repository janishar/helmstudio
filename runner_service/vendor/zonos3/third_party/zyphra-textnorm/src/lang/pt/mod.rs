//! `text_normalization/pt` (NeMo r1.2.0, deterministic, cased). `pt/taggers/tokenize_and_classify.py` has no
//! `token_plus_punct`: a token glued to punctuation only matches when one classifier accepts the whole run.

pub mod graph_utils;
pub mod taggers;
pub mod verbalizers;

use std::sync::LazyLock;

use graph_utils::Spans;
use taggers as t;

use crate::lang::common::{self, Punctuation};
use crate::lattice::{Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::cost;

crate::labels!(WHITELIST_SYMBOLS, "pt/whitelist/symbol.tsv");

/// `pt/taggers/punctuation.py`: the en marks minus the first column of `pt/data/whitelist/symbol.tsv`.
static PUNCT: LazyLock<Punctuation> = LazyLock::new(|| {
    let symbols = WHITELIST_SYMBOLS.iter().filter_map(|r| r.first()).map(String::as_str);
    Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, symbols, cost(2.1))
});

struct Pt {
    classifiers: Vec<Box<dyn Classifier>>,
}

impl Grammar for Pt {
    fn code(&self) -> &'static str {
        "pt"
    }
    fn shape(&self) -> Shape {
        Shape::Pt
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&*PUNCT)
    }
    fn verbalize(&self, token: &Dict) -> Option<String> {
        let (k, v) = common::class(token)?;
        if k == "name" {
            return common::verbalize_name(token);
        }
        let Value::Dict(d) = v else { return None };
        let expanded;
        let d = if needs_expand(d) {
            expanded = verbalizers::expand(d);
            &expanded
        } else {
            d
        };
        use verbalizers as vb;
        let out = match k {
            "fraction" => vb::fraction::verbalize(d),
            "decimal" => vb::decimal::verbalize(d),
            "date" => vb::date::verbalize(d),
            "time" => vb::time::verbalize(d),
            "measure" => vb::measure::verbalize(d),
            "money" => vb::money::verbalize(d),
            "ordinal" => vb::ordinal::verbalize(d),
            "cardinal" => vb::cardinal::verbalize(d),
            "telephone" => vb::telephone::verbalize(d),
            "electronic" => vb::electronic::verbalize(d),
            _ => None,
        }?;
        // delete_tokens maps NBSP → space
        Some(if out.contains('\u{a0}') { out.replace('\u{a0}', " ") } else { out })
    }
}

fn needs_expand(d: &Dict) -> bool {
    d.iter().any(|(_, v)| match v {
        Value::Str(s) => s.contains('"'),
        Value::Dict(inner) => needs_expand(inner),
        _ => false,
    })
}

static GRAMMAR: LazyLock<Pt> = LazyLock::new(|| {
    fn b<T: graph_utils::SpanTagger + 'static>(tagger: T, w: f64) -> Box<dyn Classifier> {
        Box::new(Spans { tagger, weight: cost(w) })
    }
    Pt {
        classifiers: vec![
            b(t::whitelist::Whitelist, 1.01),
            b(t::date::Date, 1.1),
            b(t::time::Time, 1.1),
            b(t::measure::Measure, 1.1),
            b(t::fraction::Fraction, 1.1),
            b(t::decimal::Decimal, 1.1),
            b(t::ordinal::Ordinal, 1.1),
            b(t::Cardinal, 1.1),
            b(t::money::Money, 1.1),
            b(t::telephone::Telephone, 1.11),
            b(t::electronic::Electronic, 1.11),
            b(t::word::Word { punct: &PUNCT }, 100.0),
        ],
    }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

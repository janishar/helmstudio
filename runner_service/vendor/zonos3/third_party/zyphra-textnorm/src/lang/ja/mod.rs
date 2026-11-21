//! `text_normalization/ja` (NeMo r1.2.0), `deterministic=True`, built `cased` (ja ignores `input_case`).
//!
//! `tokenize_and_classify.py`: `closure(tokens { classify } , 1)` with no whitespace handling, so an input space
//! has no path unless a classifier consumes it (date brackets, the fraction's `integer " " numerator`).

mod taggers;
mod verbalizers;

use std::sync::LazyLock;

use crate::lattice::{Classifier, Shape};
use crate::permute;
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};

struct Ja {
    /// The whole `classify` union as one classifier (`taggers::tokenize_and_classify`).
    classifiers: [Box<dyn Classifier>; 1],
}

fn build() -> Ja {
    Ja { classifiers: [Box::new(taggers::tokenize_and_classify::Union::new())] }
}

impl Grammar for Ja {
    fn code(&self) -> &'static str {
        "ja"
    }
    fn shape(&self) -> Shape {
        Shape::Cjk
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
    /// The default (each token's first verbalizable ordering, joined with `""`), written into one buffer.
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for token in split {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            permute::first_ordering(body, &mut |o: &Dict| verbalizers::verbalize_into(o, &mut out).then_some(()))?;
        }
        Some(out)
    }
    fn token_join(&self) -> &'static str {
        ""
    }
}

static GRAMMAR: LazyLock<Ja> = LazyLock::new(build);

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

#[cfg(test)]
mod tests {
    use crate::pipeline::tagged_string;

    fn tags(s: &str) -> String {
        let chars: Vec<char> = s.chars().collect();
        tagged_string(super::grammar(), &chars).unwrap()
    }

    /// `taggers::cardinal::rank_signed`: equal cost (punct 1.0 + number), the oracle prefers the longer punctuation
    /// run over a signed number (golden `--332.55kg`, `\-8 98/46\`; the alternatives are `name: "-"` + `decimal {
    /// negative: "マイナス" … }` and `name: "\"` + `fraction { negative: "マイナス" … }`).
    #[test]
    fn signed_numbers_rank_after_punctuation_runs() {
        assert_eq!(
            tags("--332.55kg"),
            "tokens { name: \"--\" } tokens { decimal { integer_part: \"三百三十二\" fractional_part: \"五五\" } } \
             tokens { name: \"k\" } tokens { name: \"g\" }"
        );
        assert_eq!(
            tags("\\-8 98/46\\"),
            "tokens { name: \"\\-\" } tokens { fraction { integer_part: \"八\" numerator: \"九十八\" denominator: \
             \"四十六\" } } tokens { name: \"\\\" }"
        );
    }
}

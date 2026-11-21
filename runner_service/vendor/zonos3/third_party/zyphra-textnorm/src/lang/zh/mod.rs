//! `text_normalization/zh` (NeMo r1.2.0), `deterministic=True`, built `cased`.
//!
//! `tokenize_and_classify.py`: `closure(tokens { classify } , 1)` with no whitespace handling; the word classifier
//! takes exactly one `NEMO_NOT_QUOTE` char, so an input space becomes a `name: " "` token.

mod taggers;
mod verbalizers;

use std::sync::LazyLock;

use crate::lattice::{Classifier, Shape};
use crate::permute;
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};

struct Zh {
    classifiers: Vec<Box<dyn Classifier>>,
}

fn build() -> Zh {
    Zh { classifiers: vec![Box::new(taggers::Union::new())] }
}

impl Grammar for Zh {
    fn code(&self) -> &'static str {
        "zh"
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
        let mut out = String::new();
        verbalizers::verbalize_into(token, &mut out).then_some(out)
    }
    /// The default's result (each token's first verbalizable ordering, joined with ""), written into one string.
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for token in split {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            permute::first_ordering(body, &mut |ordering: &Dict| {
                verbalizers::verbalize_into(ordering, &mut out).then_some(())
            })?;
        }
        Some(out)
    }
    fn token_join(&self) -> &'static str {
        ""
    }
}

static GRAMMAR: LazyLock<Zh> = LazyLock::new(build);

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

#[cfg(test)]
mod tests {
    fn tags(s: &str) -> String {
        let t: Vec<char> = s.chars().collect();
        crate::pipeline::tagged_string(super::grammar(), &t).unwrap()
    }

    /// Oracle (`triage.py --raw`): `--419` → `name: "--"` + `cardinal 419`; the equal-cost alternative is
    /// `name: "-"` + `cardinal { negative: "-" integer: "四百一十九" }`.
    #[test]
    fn sign_goes_to_punctuation() {
        assert_eq!(tags("--419"), "tokens { name: \"--\" } tokens { cardinal { integer: \"四百一十九\" } }");
    }

    /// Oracle: `5分` → `time { minutes: "五分" }`; the equal-cost alternative is
    /// `money { integer_part: "五" currency_min: "分" }`.
    #[test]
    fn fen_is_minutes() {
        assert_eq!(tags("5分"), "tokens { time { minutes: \"五分\" } }");
    }
}

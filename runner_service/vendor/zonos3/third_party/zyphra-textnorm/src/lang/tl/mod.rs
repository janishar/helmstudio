//! Tagalog text normalization grammar.
mod graph_utils;
mod taggers;
mod verbalizers;

use crate::lang::common::Punctuation;
use crate::lattice::{Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::cost;
use std::sync::LazyLock;

struct Tagalog {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}
impl Grammar for Tagalog {
    fn code(&self) -> &'static str {
        "tl"
    }
    fn shape(&self) -> Shape {
        Shape::Separated
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
    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(key, Value::Str(value))] = body.as_slice() {
                if key == "name" && !value.contains(' ') {
                    if value.contains('\u{a0}') {
                        out.extend(value.chars().map(|c| if c == '\u{a0}' { ' ' } else { c }));
                    } else {
                        out.push_str(value);
                    }
                    continue;
                }
            }
            let value = crate::permute::first_ordering(body, &mut |ordering: &Dict| self.verbalize(ordering))?;
            out.push_str(&value);
        }
        Some(out)
    }
}
static GRAMMAR: LazyLock<Tagalog> =
    LazyLock::new(|| Tagalog { classifiers: vec![Box::new(taggers::Taggers)], punct: Punctuation::en(cost(1.1)) });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

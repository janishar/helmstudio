// During exploration validate every argument, but defer native text allocation.
macro_rules! spoken {
    ($($arg:tt)*) => {{
        if writing_tags() {
            format!($($arg)*)
        } else {
            let _ = format_args!($($arg)*);
            String::new()
        }
    }};
}

mod number;
mod tables;
mod taggers;
mod verbalizers;
use taggers::*;

use crate::lattice::{Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::Dict;
use std::sync::LazyLock;

struct Greek {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: GreekPunctuation,
}
impl Grammar for Greek {
    fn code(&self) -> &'static str {
        "el"
    }
    fn shape(&self) -> Shape {
        Shape::SeparatedOrPunct
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
static GRAMMAR: LazyLock<Greek> =
    LazyLock::new(|| Greek { classifiers: vec![Box::new(Union)], punct: GreekPunctuation });
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

thread_local! { static WRITE_TAGS: std::cell::Cell<bool> = const { std::cell::Cell::new(false) }; }
fn writing_tags() -> bool {
    WRITE_TAGS.with(|v| v.get())
}

struct RestoreWriting(bool);
impl Drop for RestoreWriting {
    fn drop(&mut self) {
        WRITE_TAGS.with(|flag| flag.set(self.0));
    }
}

use crate::lattice::{Candidate, Classifier};
use crate::text::cost;

#[inline]
pub(in crate::lang::el) fn is_mark(c: char) -> bool {
    "!#$%&'()*+,-./:;<=>?@^_`{|}~\"··;«»„“”‘’–—…[]".contains(c)
}

pub(in crate::lang::el) struct GreekPunctuation;
impl Classifier for GreekPunctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if let Some(c) = text.get(start) {
            if is_mark(*c) {
                out.push(Candidate { end: start + 1, cost: cost(1.1), rank: 0, tag: String::new() });
            }
        }
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        candidate.tag = format!("name: \"{}\"", text[start]);
    }
}

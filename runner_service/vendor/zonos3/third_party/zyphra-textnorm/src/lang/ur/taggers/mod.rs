//! Urdu classifier modules.

use crate::lattice::Candidate;
use crate::text::Cost;

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod punctuation;
pub mod roman;
pub mod serial;
pub mod telephone;
pub mod time;
pub mod whitelist;
pub mod word;

/// Where a classifier pushes its candidates.
pub struct Sink<'a> {
    pub out: &'a mut Vec<Candidate>,
    pub target: Option<(usize, Cost, i32)>,
    pub found: Option<String>,
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

    #[inline]
    pub fn exploring(&self) -> bool {
        self.target.is_none()
    }
}

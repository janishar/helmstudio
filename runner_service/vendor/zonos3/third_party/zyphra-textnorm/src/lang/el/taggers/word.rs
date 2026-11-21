use crate::lattice::Candidate;
use crate::text::cost;

pub(in crate::lang::el) fn word(end: usize, out: &mut Vec<Candidate>) {
    out.push(Candidate { end, cost: cost(100.0), rank: 13, tag: String::new() });
}

//! `ko/graph_utils.py` (and the `ja/graph_utils.py` classes ko imports), plus the two output conventions of the
//! ko sub-grammars:
//! - streamed: a grammar calls a sink once per accepted `(end, cost)` with a [`Render`] that writes its output on
//!   demand, so exploring a span costs no string building; sequences nest the sinks;
//! - collected ([`Outs`]): every `(end, output, cost)` in the order the union/sequence lists them, equal
//!   `(end, output)` pairs merged at their first position with the lower cost (the rare branches keep this form).

use crate::text::{Cost, match_str};

macro_rules! data {
    ($p:literal) => {
        include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/ko/", $p))
    };
}
pub(crate) use data;

/// Writes one output (tag body or field value) on demand.
pub type Render<'a> = &'a dyn Fn(&mut String);
/// Receives every accepted `(end, cost, output)` of a sub-grammar.
pub type Sink<'a> = &'a mut dyn FnMut(usize, Cost, Render<'_>);
/// Receives every accepted `(end, cost, loses, tag body)` of a top-level tagger; `loses` marks the outputs a fitted
/// tie rule ranks last (see `Tie` in mod.rs).
pub type TagSink<'a> = &'a mut dyn FnMut(usize, Cost, bool, Render<'_>);
/// A sub-grammar fixed at its start position: streams its outputs into the sink.
pub type Gen<'a> = &'a dyn Fn(Sink<'_>);

pub type Outs = Vec<(usize, String, Cost)>;

pub fn add(out: &mut Outs, end: usize, s: String, c: Cost) {
    if let Some(r) = out.iter_mut().find(|r| r.0 == end && r.1 == s) {
        r.2 = r.2.min(c);
    } else {
        out.push((end, s, c));
    }
}

/// `closure(x, 0, 1)`: absent first.
pub fn opt(p: usize, a: Outs) -> Outs {
    let mut r = vec![(p, String::new(), 0)];
    for (e, s, c) in a {
        add(&mut r, e, s, c);
    }
    r
}

pub fn alt(parts: impl IntoIterator<Item = Outs>) -> Outs {
    let mut r = Outs::new();
    for part in parts {
        for (e, s, c) in part {
            add(&mut r, e, s, c);
        }
    }
    r
}

/// End of the run of chars satisfying `f` from `p`.
pub fn run(t: &[char], p: usize, f: impl Fn(char) -> bool) -> usize {
    let mut e = p;
    while e < t.len() && f(t[e]) {
        e += 1;
    }
    e
}

pub fn is_digit(c: char) -> bool {
    c.is_ascii_digit()
}

/// `key: "<value>"`.
pub fn field(s: &mut String, key: &str, value: Render<'_>) {
    s.push_str(key);
    s.push_str(": \"");
    value(s);
    s.push('"');
}

/// `pynini.cross(s, o)` streamed.
pub fn cross(t: &[char], p: usize, s: &str, o: &str, f: Sink<'_>) {
    if let Some(n) = match_str(t, p, s) {
        f(p + n, 0, &|b: &mut String| b.push_str(o));
    }
}

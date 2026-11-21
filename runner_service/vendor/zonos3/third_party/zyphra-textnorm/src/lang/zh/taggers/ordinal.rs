//! `zh/taggers/ordinal.py`: `integer: "第<cardinal>"` | `[range: "从" ] integer: "第…" range: "<goal>" integer:
//! "<第…|cardinal>"`.

use super::{Gen, Sink, card, card_ends};
use crate::text::match_str;

const GOAL: &[&str] = &["-", "~", "——", "—", "到", "至"];

/// A `[第]cardinal` value over digits `t[p..e]`.
#[derive(Clone, Copy)]
struct Num {
    di: bool,
    p: usize,
    e: usize,
}

impl Num {
    fn write(&self, t: &[char], out: &mut String) {
        if self.di {
            out.push('第');
        }
        card(t, self.p, self.e, out);
    }
}

/// `第 + just_cardinals` readings at `p`.
fn ordinals(t: &[char], p: usize) -> impl Iterator<Item = Num> {
    let ends = if t.get(p) == Some(&'第') { card_ends(t, p + 1) } else { 0..0 };
    ends.map(move |e| Num { di: true, p: p + 1, e })
}

pub struct Ordinal;

impl Gen for Ordinal {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let (source, p) = match t[start] {
            '第' => (false, start),
            '从' => (true, start + 1),
            _ => return,
        };
        for o in ordinals(t, p) {
            if !source {
                sink.push(o.e, 0, 0, "ordinal", |s| {
                    s.push_str("integer: \"");
                    o.write(t, s);
                    s.push('"');
                });
            }
            for goal in GOAL {
                let Some(n) = match_str(t, o.e, goal) else { continue };
                let q = o.e + n;
                let cards = card_ends(t, q).map(|e| Num { di: false, p: q, e });
                for second in ordinals(t, q).chain(cards) {
                    sink.push(second.e, 0, 0, "ordinal", |s| {
                        if source {
                            s.push_str("range: \"从\" ");
                        }
                        s.push_str("integer: \"");
                        o.write(t, s);
                        s.push_str("\" range: \"");
                        s.push_str(goal);
                        s.push_str("\" integer: \"");
                        second.write(t, s);
                        s.push('"');
                    });
                }
            }
        }
    }
}

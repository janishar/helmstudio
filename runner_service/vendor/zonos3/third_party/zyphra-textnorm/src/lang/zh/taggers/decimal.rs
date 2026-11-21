//! `zh/taggers/decimal.py`: `[sign] integer_part: "<cardinal>" fractional_part: "<digits>" [quantity: "<suffix>"]`.
//! The `-` sign is a `closure` of `negative: "负`, closed by one `"` (so `--` gives `negative: "负negative: "负"`);
//! its zero-repetition form only adds a stray `"` and is never the oracle's pick, so it is not emitted.

use super::{Gen, Sink, card, card_ends, digit_end, digit_names, quantities};

/// The sign of a decimal reading.
#[derive(Clone, Copy, Default)]
pub enum Sign {
    #[default]
    None,
    /// `n` ASCII `-`.
    Dashes(usize),
    /// `负` / `負`.
    Fu,
}

/// One `DecimalFst.decimal` reading: sign, integer digits `t[p..ie]`, fractional digits `t[ie + 1..e]`, quantity.
#[derive(Clone, Copy, Default)]
pub struct Dec {
    sign: Sign,
    p: usize,
    ie: usize,
    e: usize,
    quantity: Option<&'static str>,
    pub end: usize,
}

impl Dec {
    /// The fields as the tagger writes them inside `decimal { … }`.
    pub fn write(&self, t: &[char], out: &mut String) {
        match self.sign {
            Sign::None => {}
            Sign::Dashes(n) => {
                for _ in 0..n {
                    out.push_str("negative: \"负");
                }
                out.push_str("\" ");
            }
            Sign::Fu => out.push_str("negative: \"负\" "),
        }
        out.push_str("integer_part: \"");
        card(t, self.p, self.ie, out);
        out.push_str("\" fractional_part: \"");
        digit_names(&t[self.ie + 1..self.e], out);
        out.push('"');
        if let Some(q) = self.quantity {
            out.push_str(" quantity: \"");
            out.push_str(q);
            out.push('"');
        }
    }
}

/// `DecimalFst.decimal` readings at `p` (sign, `graph_decimal` with every fractional length, optional quantity),
/// each quantity form before the plain one.
pub fn decimal(t: &[char], p: usize, mut f: impl FnMut(Dec)) {
    let (sign, p) = match t.get(p) {
        Some('-') => {
            let mut e = p;
            while t.get(e) == Some(&'-') {
                e += 1;
            }
            (Sign::Dashes(e - p), e)
        }
        Some('负' | '負') => (Sign::Fu, p + 1),
        _ => (Sign::None, p),
    };
    let ie = digit_end(t, p);
    if ie == p || t.get(ie) != Some(&'.') || !card_ends(t, p).contains(&ie) {
        return;
    }
    let fe = digit_end(t, ie + 1);
    for e in ie + 2..=fe {
        let d = Dec { sign, p, ie, e, quantity: None, end: e };
        quantities(t, e, |qe, q| f(Dec { quantity: Some(q), end: qe, ..d }));
        f(d);
    }
}

pub struct Decimal;

impl Gen for Decimal {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        if !matches!(t[start], '0'..='9' | '-' | '负' | '負') {
            return;
        }
        decimal(t, start, |d| sink.push(d.end, 0, 0, "decimal", |s| d.write(t, s)));
    }
}

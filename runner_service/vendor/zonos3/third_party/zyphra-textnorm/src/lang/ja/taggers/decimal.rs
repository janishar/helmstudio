//! `ja/taggers/decimal.py`: optional sign (`-` → マイナス, or マイナス), cardinal, `.` deleted, one or more digits
//! spelled digit by digit (`digit.tsv | zero.tsv`).

use super::Sink;
use super::cardinal::{SIGNED_RANK, digit_run, digit_word, ends, sign_end, spell_into};
use crate::text::Cost;

pub struct Decimal {
    pub cost: Cost,
}

impl Decimal {
    fn unsigned<S: Sink>(&self, t: &[char], q: usize, negative: bool, out: &mut S) {
        // the integer part must end right before '.', so at most one integer span qualifies
        let Some(dot) = ends(t, q).find(|&e| t.get(e) == Some(&'.')) else { return };
        let run = digit_run(t, dot + 1);
        for end in dot + 2..=run {
            out.push(end, self.cost, if negative { SIGNED_RANK } else { 0 }, |tag| {
                tag.push_str("decimal { ");
                if negative {
                    tag.push_str("negative: \"マイナス\" ");
                }
                tag.push_str("integer_part: \"");
                spell_into(&t[q..dot], tag);
                tag.push_str("\" fractional_part: \"");
                for &c in &t[dot + 1..end] {
                    tag.push_str(digit_word(c));
                }
                tag.push_str("\" }");
            });
        }
    }

    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        self.unsigned(t, start, false, out);
        if let Some(q) = sign_end(t, start) {
            self.unsigned(t, q, true, out);
        }
    }
}

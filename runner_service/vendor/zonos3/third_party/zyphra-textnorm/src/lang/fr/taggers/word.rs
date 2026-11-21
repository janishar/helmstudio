//! `fr/taggers/word.py`: `name: "<NOT_SPACE+>"` at 200. Tags are written only for the words on the best path
//! ([`Classifier::materialize`]).

use std::cell::RefCell;

use super::{date, decimals, whitelist};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_nemo_space};

pub struct Word;

pub const WEIGHT: Cost = cost(200.0);

/// Which words of one text are useful, computed once per text (the lattice queries every position of the same
/// text in turn, starting at 0).
#[derive(Default)]
struct Plan {
    /// Identity of the text the plan was built for.
    key: (usize, usize),
    /// `start[i]`: a word may start at `i`.
    start: Vec<bool>,
    /// `cut[e]`: a word may end at `e` inside its non-space run.
    cut: Vec<bool>,
    /// `run_end[i]`: end of the non-space run holding `i`.
    run_end: Vec<u32>,
}

thread_local! {
    static PLAN: RefCell<Plan> = RefCell::new(Plan::default());
}

impl Word {
    fn plan(&self, text: &[char], p: &mut Plan) {
        let n = text.len();
        p.key = (text.as_ptr() as usize, n);
        p.start.clear();
        p.start.resize(n, false);
        p.cut.clear();
        p.cut.resize(n + 1, false);
        p.run_end.clear();
        p.run_end.resize(n, 0);
        let mut run_start = 0;
        // end of the previous non-space run and its start
        let mut prev: Option<(usize, usize)> = None;
        while run_start < n {
            if is_nemo_space(text[run_start]) {
                run_start += 1;
                continue;
            }
            let run_end = text[run_start..].iter().position(|&c| is_nemo_space(c)).map_or(n, |i| run_start + i);
            for r in &mut p.run_end[run_start..run_end] {
                *r = run_end as u32;
            }
            // A word starting inside a run follows an item ending there. If that item starts in the same run, the
            // word covering both (200) beats item + word (200 + a positive cost), so the word is only useful after
            // an item that crosses the whitespace before this run and ends at `start`.
            p.start[run_start] = true;
            if let Some((ps, pe)) = prev {
                let prev = &text[ps..pe];
                let prev_digit = prev.last().is_some_and(char::is_ascii_digit);
                let mut numeric_ctx: Option<bool> = None;
                let mut numeric_chars = true;
                for start in run_start + 1..run_end {
                    let c = text[start - 1];
                    numeric_chars &= c.is_ascii_digit() || matches!(c, ',' | '.' | '/' | '-');
                    let s = &text[run_start..start];
                    p.start[start] = crosses(prev, prev_digit, &mut numeric_ctx, numeric_chars, s);
                }
            }
            if run_end < n {
                // A word ending at `e` inside the run is only useful when the item starting at `e` leaves the run:
                // otherwise the word covering the whole run (200) beats word + anything (200 + a positive cost).
                // Items that cross the following whitespace start with the run's suffix `text[e..run_end]`: a digit
                // group of a cardinal, decimal integer part, fraction integer part or date day / day range ("1 250",
                // "1 1/2", "2,5 millions", "15 mai", "17-18 juin"; a digit or `-` first, then digits and
                // `, . / -`), the date's "le "/"les ", or a whitelist key with a space. A numeric item crossing the
                // whitespace continues with digits, a month name or a quantity; "le "/"les " is followed by a day.
                let next_start = text[run_end..].iter().position(|&c| !is_nemo_space(c)).map_or(n, |i| run_end + i);
                let next_digit = text.get(next_start).is_some_and(char::is_ascii_digit);
                let mut numeric_crosses: Option<bool> = next_digit.then_some(true);
                let mut numeric = true;
                let mut has_digit = false;
                for e in (run_start + 1..run_end).rev() {
                    let c = text[e];
                    numeric &= c.is_ascii_digit() || matches!(c, ',' | '.' | '/' | '-');
                    has_digit |= c.is_ascii_digit();
                    let suffix = &text[e..run_end];
                    p.cut[e] = (numeric
                        && has_digit
                        && (c.is_ascii_digit() || c == '-')
                        && *numeric_crosses.get_or_insert_with(|| {
                            date::month_name_at(text, next_start) || decimals::quantity_at(text, next_start)
                        }))
                        || (next_digit && (suffix == ['l', 'e'] || suffix == ['l', 'e', 's']))
                        || whitelist::spaced_key_at(text, e);
                }
            }
            prev = Some((run_start, run_end));
            run_start = run_end;
        }
    }
}

/// Whether an item that crosses the whitespace after the non-space run `prev` can end after `s` (a prefix of the
/// next run). Such an item ends `prev` with a digit (a cardinal/decimal/fraction digit group "1 250", "1 1/2",
/// "-3 702,7", or a day before a month name "15 mai", or a number before a quantity "2 millions"), with a month name
/// (a year "mai 2021"), or with "le"/"les" (a day "le 15"); or it is a whitelist key with a space ("av. J.-C.").
/// `numeric_chars`: `s` holds only digits and `, . / -`; `numeric_ctx` caches the `prev` test of numeric items.
fn crosses(prev: &[char], prev_digit: bool, numeric_ctx: &mut Option<bool>, numeric_chars: bool, s: &[char]) -> bool {
    // numeric items end with a digit
    let numeric = numeric_chars && s.last().is_some_and(char::is_ascii_digit);
    let crossing = if numeric {
        *numeric_ctx.get_or_insert_with(|| {
            prev_digit
                || prev.ends_with(&['l', 'e'])
                || prev.ends_with(&['l', 'e', 's'])
                || date::ends_with_month_name(prev)
        })
    } else {
        prev_digit
            && s.first().is_some_and(char::is_ascii_lowercase)
            && (date::is_month_name(s) || decimals::is_quantity(s))
    };
    crossing || whitelist::is_spaced_key_tail(s)
}

impl Classifier for Word {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        PLAN.with(|p| {
            let mut p = p.borrow_mut();
            if start == 0 || p.key != (text.as_ptr() as usize, text.len()) {
                self.plan(text, &mut p);
            }
            if !p.start[start] {
                return;
            }
            let run_end = p.run_end[start] as usize;
            out.push(candidate(start, run_end));
            for e in (start + 1..run_end).rev() {
                if p.cut[e] {
                    out.push(candidate(start, e));
                }
            }
        });
    }

    fn materialize(&self, text: &[char], start: usize, c: &mut Candidate) {
        let mut tag = String::with_capacity(8 + 2 * (c.end - start));
        tag.push_str("name: \"");
        tag.extend(&text[start..c.end]);
        tag.push('"');
        c.tag = tag;
    }
}

/// The word `text[start..end]`, its tag left for [`Classifier::materialize`].
fn candidate(start: usize, end: usize) -> Candidate {
    Candidate { end, cost: WEIGHT, rank: -((end - start) as i32), tag: String::new() }
}

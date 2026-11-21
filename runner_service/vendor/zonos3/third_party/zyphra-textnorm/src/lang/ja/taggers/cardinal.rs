//! `ja/taggers/cardinal.py`.
//!
//! `just_cardinals` is the union of twelve fixed-length digit graphs (`NEMO_DIGIT**n @ …`) plus `graph_all` and
//! `zero.tsv`. Enumerating that union exhaustively (every 1-12 digit string over `{0,1,5,9}` up to 8 digits and
//! `{0,1,5}`/`{0,1}` up to 11/12, plus 30 000 random strings) shows it is functional and equals the closed form in
//! [`spell_into`]: no output for a leading `0` (except `"0"` → 零) or more than 12 digits; otherwise 億/万 groups of
//! four digits where a group's 千 keeps its 一 except in the lowest group (`一千万`, `一億`, but `千`), and 百/十
//! never take 一.

use super::Sink;
use crate::text::{Cost, Table, is_digit};

const DIGITS: [&str; 10] = ["", "一", "二", "三", "四", "五", "六", "七", "八", "九"];

/// `digit.tsv | zero.tsv` on one ASCII digit.
pub fn digit_word(c: char) -> &'static str {
    if c == '0' { "零" } else { DIGITS[(c as u8 - b'0') as usize] }
}

/// End of the ASCII digit run starting at `p` (`p` itself when there is none).
pub fn digit_run(t: &[char], p: usize) -> usize {
    let mut e = p;
    while e < t.len() && is_digit(t[e]) {
        e += 1;
    }
    e
}

/// `prefix_matches` of a table whose keys are exactly `1`, `2`, …, `N` (N < 100) in file order (`month.tsv`,
/// `day.tsv`, `hour.tsv`, `minute.tsv`, `second.tsv`), by arithmetic: the one-digit key, then the two-digit key.
pub fn small_number<'a>(table: &'a Table, t: &[char], p: usize, mut f: impl FnMut(usize, &'a str)) {
    let rows = table.rows();
    let Some(d1) = t.get(p).and_then(|c| c.to_digit(10)) else { return };
    if d1 == 0 {
        return;
    }
    let d1 = d1 as usize;
    if d1 <= rows.len() {
        f(p + 1, &rows[d1 - 1].value);
    }
    if let Some(d2) = t.get(p + 1).and_then(|c| c.to_digit(10)) {
        let n = d1 * 10 + d2 as usize;
        if n <= rows.len() {
            f(p + 2, &rows[n - 1].value);
        }
    }
}

fn group(out: &mut String, n: u64, one_thousand: bool) {
    let (th, h, te, o) = (n / 1000, n / 100 % 10, n / 10 % 10, n % 10);
    if th > 0 {
        if th > 1 || one_thousand {
            out.push_str(DIGITS[th as usize]);
        }
        out.push('千');
    }
    if h > 0 {
        if h > 1 {
            out.push_str(DIGITS[h as usize]);
        }
        out.push('百');
    }
    if te > 0 {
        if te > 1 {
            out.push_str(DIGITS[te as usize]);
        }
        out.push('十');
    }
    out.push_str(DIGITS[o as usize]);
}

/// `just_cardinals` on exactly `d` (ASCII digits), appended to `out`; `false` when there is no path.
pub fn spell_into(d: &[char], out: &mut String) -> bool {
    if d.is_empty() || d.len() > 12 || !d.iter().all(|&c| is_digit(c)) {
        return false;
    }
    if d[0] == '0' {
        if d.len() == 1 {
            out.push('零');
            return true;
        }
        return false;
    }
    let v = d.iter().fold(0u64, |a, &c| a * 10 + (c as u8 - b'0') as u64);
    let (oku, man, low) = (v / 100_000_000, v / 10_000 % 10_000, v % 10_000);
    if oku > 0 {
        group(out, oku, true);
        out.push('億');
    }
    if man > 0 {
        group(out, man, true);
        out.push('万');
    }
    group(out, low, false);
    true
}

/// Every span end `just_cardinals` accepts at `p`, ascending.
pub fn ends(t: &[char], p: usize) -> std::ops::Range<usize> {
    if p >= t.len() || !is_digit(t[p]) {
        return p..p;
    }
    if t[p] == '0' {
        return p + 1..p + 2;
    }
    let run = digit_run(t, p).min(p + 12);
    p + 1..run + 1
}

/// The one end of [`ends`] at `p` not followed by a digit (the end of the digit run), if `just_cardinals` accepts
/// it: every other end is followed by a digit, so only this one can precede a non-digit marker.
pub fn run_end(t: &[char], p: usize) -> Option<usize> {
    ends(t, p).last().filter(|&e| !t.get(e).is_some_and(|&c| is_digit(c)))
}

/// The sign of `optional_sign`: `-` or `マイナス`, as the end of the sign.
pub fn sign_end(t: &[char], p: usize) -> Option<usize> {
    if t.get(p) == Some(&'-') {
        return Some(p + 1);
    }
    crate::text::match_str(t, p, "マイナス").map(|n| p + n)
}

/// Signed readings (`negative: …`) rank after unsigned ones (rank 1). The only equal-cost competitor of a signed
/// number token is a longer punctuation run in front of the unsigned token (`--5`: `name: "-"` + signed vs
/// `name: "--"` + unsigned, both one punct + one number); the oracle takes the longer punctuation run:
/// `--332.55kg` → `tokens { name: "--" } tokens { decimal { integer_part: "三百三十二" … } }`,
/// `\-8 98/46\` → `tokens { name: "\-" } tokens { fraction { integer_part: "八" … } }`.
pub const SIGNED_RANK: i32 = 1;

pub struct Cardinal {
    pub cost: Cost,
}

impl Cardinal {
    /// Only the signed readings are pushed: every unsigned `cardinal { integer: "N" }` span is also a date token
    /// `date { year: "N" }` (`individual_year_component_2` without era) at the same cost 1.1, and date comes first in
    /// the union (lower rank), so an unsigned cardinal is never on a best path (the oracle tags bare numbers as
    /// `date { year: … }` too).
    pub fn spans<S: Sink>(&self, t: &[char], start: usize, out: &mut S) {
        let Some(q) = sign_end(t, start) else { return };
        for end in ends(t, q) {
            out.push(end, self.cost, SIGNED_RANK, |tag| {
                tag.push_str("cardinal { negative: \"-\" integer: \"");
                spell_into(&t[q..end], tag);
                tag.push_str("\" }");
            });
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn sp(s: &str) -> Option<String> {
        let mut out = String::new();
        spell_into(&s.chars().collect::<Vec<_>>(), &mut out).then_some(out)
    }

    #[test]
    fn closed_form() {
        assert_eq!(sp("0").as_deref(), Some("零"));
        assert_eq!(sp("01"), None);
        assert_eq!(sp("1000").as_deref(), Some("千"));
        assert_eq!(sp("10000000").as_deref(), Some("一千万"));
        assert_eq!(sp("100010001").as_deref(), Some("一億一万一"));
        assert_eq!(sp("7056020").as_deref(), Some("七百五万六千二十"));
        assert_eq!(sp("1000000000000"), None);
    }
}

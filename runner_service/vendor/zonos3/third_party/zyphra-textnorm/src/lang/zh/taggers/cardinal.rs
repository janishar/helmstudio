//! `zh/taggers/cardinal.py`: `just_cardinals` (1-12 digits; functional, checked against the oracle's graph on 29 789
//! digit strings) and the `cardinal { [negative: "-"] integer: "…" }` token.
//!
//! Every component below mirrors one pynini sub-graph. The components carry no length restriction of their own (as
//! upstream: only the top-level `NEMO_DIGIT**n @` fixes the total), so each function consumes exactly the slice it is
//! given and the first succeeding union branch is the unique output.

use std::sync::LazyLock;

use super::{Gen, Sink};
use crate::text::Table;

crate::tsv!(ZERO, "zh/number/zero.tsv");
crate::tsv!(DIGIT, "zh/number/digit.tsv");
crate::tsv!(DIGIT_ALT, "zh/number/digit_alt.tsv");
crate::tsv!(TIES, "zh/number/ties.tsv");
crate::tsv!(TEEN, "zh/number/teen.tsv");
crate::tsv!(TEEN_ALT, "zh/number/teen_alt.tsv");

type Digits = [&'static str; 10];

struct Words {
    digit: Digits,
    digit_alt: Digits,
    /// `ties.tsv` by the tens digit.
    ties: Digits,
    /// `teen.tsv` / `teen_alt.tsv` by the units digit.
    teen: Digits,
    teen_alt: Digits,
    zero: &'static str,
}

fn by_digit(t: &'static Table, pos: usize) -> Digits {
    let mut a = [""; 10];
    for r in t.rows() {
        if let Some(c) = r.key.chars().nth(pos).filter(char::is_ascii_digit) {
            a[d(c)] = r.value.as_str();
        }
    }
    a
}

static W: LazyLock<Words> = LazyLock::new(|| Words {
    digit: by_digit(&DIGIT, 0),
    digit_alt: by_digit(&DIGIT_ALT, 0),
    ties: by_digit(&TIES, 0),
    teen: by_digit(&TEEN, 1),
    teen_alt: by_digit(&TEEN_ALT, 1),
    zero: ZERO.exact("0").next().map_or("", |r| r.value.as_str()),
});

#[inline]
fn d(c: char) -> usize {
    (c as u32).wrapping_sub('0' as u32) as usize
}

/// `digit.tsv | zero.tsv` for one ASCII digit (date years, decimal fractions).
pub fn digit_name(c: char) -> &'static str {
    if c == '0' { W.zero } else { W.digit[d(c)] }
}

fn push(out: &mut String, s: &str) -> bool {
    if s.is_empty() {
        return false;
    }
    out.push_str(s);
    true
}

fn zeros(s: &[char]) -> bool {
    s.iter().all(|&c| c == '0')
}

fn lead0(s: &[char]) -> usize {
    s.iter().take_while(|&&c| c == '0').count()
}

type Part = fn(&Words, &[char], &mut String) -> bool;

/// `graph_all` (`alt == false`, teens `一十x`) / `graph_all_alt` (teens `十x`): one or two digits.
fn all(w: &Words, s: &[char], alt: bool, out: &mut String) -> bool {
    match *s {
        [a] => push(out, w.digit[d(a)]),
        ['1', b] => push(out, if alt { w.teen[d(b)] } else { w.teen_alt[d(b)] }),
        [a, b] => {
            if !push(out, w.ties[d(a)]) {
                return false;
            }
            if b != '0' {
                let mark = out.len();
                if !push(out, w.digit[d(b)]) {
                    out.truncate(mark);
                    return false;
                }
            }
            true
        }
        _ => false,
    }
}

fn all_alt(w: &Words, s: &[char], out: &mut String) -> bool {
    all(w, s, true, out)
}

fn digit_alt_one(w: &Words, s: &[char], out: &mut String) -> bool {
    matches!(*s, [a] if push(out, w.digit_alt[d(a)]))
}

/// `closure(delete("0")) + insert("零") + graph_all`.
fn zeros_then_all(w: &Words, s: &[char], out: &mut String) -> bool {
    let k = lead0(s);
    let mark = out.len();
    out.push('零');
    if all(w, &s[k..], false, out) {
        return true;
    }
    out.truncate(mark);
    false
}

/// `graph_hundred_component`.
fn hundred(w: &Words, s: &[char], out: &mut String) -> bool {
    let Some((&h, rest)) = s.split_first() else { return false };
    let mark = out.len();
    if !push(out, w.digit[d(h)]) {
        return false;
    }
    out.push('百');
    if zeros(rest) {
        return true;
    }
    let k = lead0(rest);
    for _ in 0..k {
        out.push('零');
    }
    if all(w, &rest[k..], false, out) {
        return true;
    }
    out.truncate(mark);
    false
}

/// `graph_thousand_component`.
fn thousand(w: &Words, s: &[char], out: &mut String) -> bool {
    let Some((&h, rest)) = s.split_first() else { return false };
    let mark = out.len();
    if !push(out, w.digit_alt[d(h)]) {
        return false;
    }
    out.push('千');
    if zeros(rest) || hundred(w, rest, out) || zeros_then_all(w, rest, out) {
        return true;
    }
    out.truncate(mark);
    false
}

/// `delete("0"*n) + insert("零") + f`.
fn zero_prefixed(w: &Words, s: &[char], n: usize, f: Part, out: &mut String) -> bool {
    if s.len() < n || !zeros(&s[..n]) {
        return false;
    }
    let mark = out.len();
    out.push('零');
    if f(w, &s[n..], out) {
        return true;
    }
    out.truncate(mark);
    false
}

/// The tail union after `万`.
fn after_wan(w: &Words, s: &[char], out: &mut String) -> bool {
    zeros(s) || thousand(w, s, out) || zero_prefixed(w, s, 1, hundred, out) || zeros_then_all(w, s, out)
}

/// `f(s[..n]) + insert(unit) + tail(s[n..])`.
fn head(w: &Words, s: &[char], n: usize, f: Part, unit: char, tail: Part, out: &mut String) -> bool {
    if s.len() < n {
        return false;
    }
    let mark = out.len();
    if f(w, &s[..n], out) {
        out.push(unit);
        if tail(w, &s[n..], out) {
            return true;
        }
    }
    out.truncate(mark);
    false
}

fn ten_thousand(w: &Words, s: &[char], out: &mut String) -> bool {
    head(w, s, 1, digit_alt_one, '万', after_wan, out)
}

fn hundred_thousand(w: &Words, s: &[char], out: &mut String) -> bool {
    head(w, s, 2, all_alt, '万', after_wan, out)
}

fn million(w: &Words, s: &[char], out: &mut String) -> bool {
    head(w, s, 3, hundred, '万', after_wan, out)
}

fn ten_million(w: &Words, s: &[char], out: &mut String) -> bool {
    head(w, s, 4, thousand, '万', after_wan, out)
}

/// The tail union after `亿`.
fn after_yi(w: &Words, s: &[char], out: &mut String) -> bool {
    zeros(s)
        || ten_million(w, s, out)
        || zero_prefixed(w, s, 1, million, out)
        || zero_prefixed(w, s, 2, hundred_thousand, out)
        || zero_prefixed(w, s, 3, ten_thousand, out)
        || zero_prefixed(w, s, 4, thousand, out)
        || zero_prefixed(w, s, 5, hundred, out)
        || zeros_then_all(w, s, out)
}

/// `just_cardinals` on a slice of ASCII digits, appended to `out`; `false` (nothing appended) when the graph has no
/// path (leading zeros, more than 12 digits).
pub fn just_cardinal(s: &[char], out: &mut String) -> bool {
    let w = &*W;
    match s.len() {
        12 => head(w, s, 4, thousand, '亿', after_yi, out),
        11 => head(w, s, 3, hundred, '亿', after_yi, out),
        10 => head(w, s, 2, all_alt, '亿', after_yi, out),
        9 => head(w, s, 1, digit_alt_one, '亿', after_yi, out),
        8 => head(w, s, 4, thousand, '万', after_wan, out),
        7 => million(w, s, out),
        6 => hundred_thousand(w, s, out),
        5 => ten_thousand(w, s, out),
        4 => thousand(w, s, out),
        3 => hundred(w, s, out),
        1 if s[0] == '0' => push(out, w.zero),
        1 | 2 => all(w, s, true, out),
        _ => false,
    }
}

/// The longest slice `just_cardinals` can read.
pub const MAX_DIGITS: usize = 12;

/// `CardinalFst`: `cardinal { negative: "-" integer: "…" }` (sign `-` or `负`) or `cardinal { integer: "…" }`, every
/// digit prefix of the run.
pub struct Cardinal;

impl Gen for Cardinal {
    fn run(&self, t: &[char], start: usize, sink: &mut Sink) {
        let p = match t[start] {
            '-' | '负' => start + 1,
            c if c.is_ascii_digit() => start,
            _ => return,
        };
        for e in super::card_ends(t, p) {
            sink.raw(e, 0, 0, |s| write_tag(t, start, e, s));
        }
    }
}

/// The tag of the [`Cardinal`] span `t[start..end]` (a sign is its first char).
pub fn write_tag(t: &[char], start: usize, end: usize, s: &mut String) {
    let neg = matches!(t[start], '-' | '负');
    s.push_str("cardinal { ");
    if neg {
        s.push_str("negative: \"-\" ");
    }
    s.push_str("integer: \"");
    super::card(t, start + usize::from(neg), end, s);
    s.push_str("\" }");
}

#[cfg(test)]
mod tests {
    use super::*;

    fn card(s: &str) -> Option<String> {
        let c: Vec<char> = s.chars().collect();
        let mut out = String::new();
        just_cardinal(&c, &mut out).then_some(out)
    }

    #[test]
    fn readings() {
        assert_eq!(card("0").as_deref(), Some("零"));
        assert_eq!(card("10").as_deref(), Some("十"));
        assert_eq!(card("316").as_deref(), Some("三百一十六"));
        assert_eq!(card("2600").as_deref(), Some("两千六百"));
        assert_eq!(card("1000001").as_deref(), Some("一百万零一"));
        assert_eq!(card("101111111").as_deref(), Some("一亿零一百一十一万一千一百一十一"));
        assert_eq!(card("200348966").as_deref(), Some("两亿零三十四万八千九百六十六"));
        assert_eq!(card("05"), None);
        assert_eq!(card("1234567890123"), None);
    }

    /// `super::card_ends` is exactly the graph's domain: one digit, or 2-12 digits without a leading zero.
    #[test]
    fn domain() {
        let mut s = String::new();
        for n in 0..100_000u32 {
            for width in 1..=6 {
                let d: Vec<char> = format!("{n:0width$}").chars().collect();
                if d.len() != width {
                    continue;
                }
                s.clear();
                assert_eq!(just_cardinal(&d, &mut s), width == 1 || d[0] != '0', "{d:?}");
            }
        }
        for d in ["100000000000", "999999999999", "1000000", "10000001", "0123456789"] {
            let d: Vec<char> = d.chars().collect();
            s.clear();
            assert_eq!(just_cardinal(&d, &mut s), d[0] != '0');
        }
    }
}

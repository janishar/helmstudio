//! `es/taggers/cardinal.py`.

use crate::lang::es::graph_utils::{NUM_DIGIT, NUM_HUNDREDS, NUM_TEEN, NUM_TIES, NUM_TWENTIES, NUM_ZERO};
use crate::lang::es::{Lazy, Sink, ok_end};
use crate::text::{Cost, cost, is_digit};

fn inv(t: &'static crate::text::Table, digits: &str) -> Option<&'static str> {
    t.rows().iter().find(|r| r.value == digits).map(|r| r.key.as_str())
}

/// `digit` words by value (index 1..=9).
static DIGITS: std::sync::LazyLock<[Option<&'static str>; 10]> =
    std::sync::LazyLock::new(|| std::array::from_fn(|v| if v == 0 { None } else { inv(&NUM_DIGIT, &v.to_string()) }));

/// `graph_tens` words by value (index 10..=99).
static TENS: std::sync::LazyLock<Vec<Option<String>>> = std::sync::LazyLock::new(|| {
    (0..100u32)
        .map(|v| {
            if v < 10 {
                return None;
            }
            let st = v.to_string();
            if let Some(w) = inv(&NUM_TEEN, &st).or_else(|| inv(&NUM_TWENTIES, &st)) {
                return Some(w.to_string());
            }
            let t = inv(&NUM_TIES, &st[..1])?;
            if v % 10 == 0 { Some(t.to_string()) } else { Some(format!("{t} y {}", DIGITS[(v % 10) as usize]?)) }
        })
        .collect()
});

/// `digit` (1-9, "1" → "un").
pub fn digit(d: char) -> Option<&'static str> {
    let v = d.to_digit(10)? as usize;
    DIGITS[v]
}

/// `graph_tens` on exactly two digits.
pub fn tens(s: &[u8]) -> Option<String> {
    tens_str(s).map(str::to_string)
}

/// `graph_tens` on exactly two digits, borrowed.
pub fn tens_str(s: &[u8]) -> Option<&'static str> {
    let [a, b] = s else { return None };
    if !a.is_ascii_digit() || !b.is_ascii_digit() {
        return None;
    }
    TENS[((a - b'0') * 10 + (b - b'0')) as usize].as_deref()
}

/// `self.hundreds` (`graph_hundreds`) on exactly three digits.
pub fn hundreds(s: &[u8]) -> Option<String> {
    if s == b"100" {
        return Some("cien".into());
    }
    if s[0] == b'1' {
        let rest = if s[1] == b'0' { digit(s[2] as char)?.to_string() } else { tens(&s[1..])? };
        return Some(format!("ciento {rest}"));
    }
    let h = inv(&NUM_HUNDREDS, std::str::from_utf8(&s[..1]).ok()?)?;
    if &s[1..] == b"00" {
        return Some(h.to_string());
    }
    if s[1] == b'0' {
        return Some(format!("{h} {}", digit(s[2] as char)?));
    }
    Some(format!("{h} {}", tens(&s[1..])?))
}

/// Whether [`hundreds`] accepts three digits starting with a nonzero digit, without building the words.
pub fn hundreds_ok(s: &[u8]) -> bool {
    s.len() == 3 && s[0] != b'0' && s.iter().all(u8::is_ascii_digit) && h3_cached(s, false).is_some()
}

/// `two_digit_non_zero` on one or two digits.
pub fn two_digit_non_zero(s: &[u8]) -> Option<String> {
    match s {
        [d] => digit(*d as char).map(str::to_string),
        [b'0', d] => digit(*d as char).map(|w| format!(" {w}")),
        _ => tens(s),
    }
}

/// `two_digit_non_zero` words of a one/two-digit value (index = the digits as written: 1..=9, then "0X" as
/// 100 + X, then 10..=99), for callers that only need a borrow.
static TWO_DIGIT: std::sync::LazyLock<Vec<Option<String>>> = std::sync::LazyLock::new(|| {
    (0..110u32)
        .map(|i| match i {
            1..=9 => two_digit_non_zero(&[b'0' + i as u8]),
            10..=99 => two_digit_non_zero(&[b'0' + (i / 10) as u8, b'0' + (i % 10) as u8]),
            101..=109 => two_digit_non_zero(&[b'0', b'0' + (i - 100) as u8]),
            _ => None,
        })
        .collect()
});

/// Borrowed [`two_digit_non_zero`].
pub fn two_digit_non_zero_str(s: &[u8]) -> Option<&'static str> {
    let i = match s {
        [d] if d.is_ascii_digit() => (d - b'0') as usize,
        [b'0', d] if d.is_ascii_digit() => 100 + (d - b'0') as usize,
        [a, b] if a.is_ascii_digit() && b.is_ascii_digit() => ((a - b'0') * 10 + (b - b'0')) as usize,
        _ => return None,
    };
    TWO_DIGIT.get(i)?.as_deref()
}

/// `graph_hundreds_component_at_least_one_none_zero_digit[_no_one]` on three digits.
fn h3(s: &[u8], no_one: bool) -> Option<String> {
    if s[0] != b'0' {
        return hundreds(s);
    }
    if s[1] != b'0' {
        return tens(&s[1..]);
    }
    if no_one && s[2] == b'1' {
        return None;
    }
    digit(s[2] as char).map(str::to_string)
}

/// `number_uncached` for 1..=999 (index = value).
static SMALL: std::sync::LazyLock<Vec<Option<(String, Cost)>>> = std::sync::LazyLock::new(|| {
    (0..1000u32).map(|n| if n == 0 { None } else { number_uncached(&n.to_string()) }).collect()
});

/// `CardinalFst.graph` before `filter_punctuation`: a plain digit string.
pub fn number(d: &str) -> Option<(String, Cost)> {
    let b = d.as_bytes();
    if (1..=3).contains(&b.len()) && b[0] != b'0' && b.iter().all(u8::is_ascii_digit) {
        let v = b.iter().fold(0usize, |a, c| a * 10 + (c - b'0') as usize);
        return SMALL[v].clone();
    }
    number_uncached(d)
}

/// `h3` words of 0..=999 (index = value): `[at_least_one_none_zero_digit, …_no_one]`; `None` for 000.
static H3: std::sync::LazyLock<Vec<[Option<String>; 2]>> = std::sync::LazyLock::new(|| {
    (0..1000u32)
        .map(|v| {
            let d = format!("{v:03}");
            if v == 0 { [None, None] } else { [h3(d.as_bytes(), false), h3(d.as_bytes(), true)] }
        })
        .collect()
});

fn h3_cached(g: &[u8], no_one: bool) -> Option<&'static str> {
    let v = g.iter().fold(0usize, |a, c| a * 10 + (c - b'0') as usize);
    H3[v][no_one as usize].as_deref()
}

/// `h6` from the cached three-digit groups, appended to `out` (with a separating space when `out` is non-empty).
fn h6_into(s: &[u8], no_one: bool, out: &mut String) -> Option<()> {
    let (hi, lo) = s.split_at(3);
    let sep = |out: &mut String| {
        if !out.is_empty() {
            out.push(' ');
        }
    };
    if hi == b"000" {
        let w = h3_cached(lo, no_one)?;
        sep(out);
        out.push_str(w);
        return Some(());
    }
    sep(out);
    if hi == b"001" {
        out.push_str("mil");
    } else {
        out.push_str(h3_cached(hi, true)?);
        out.push_str(" mil");
    }
    if lo != b"000" {
        out.push(' ');
        out.push_str(h3_cached(lo, false)?);
    }
    Some(())
}

fn number_uncached(d: &str) -> Option<(String, Cost)> {
    let b = d.as_bytes();
    if d == "0" {
        return NUM_ZERO.rows().first().map(|r| (r.key.clone(), r.cost));
    }
    if b.is_empty() || b.len() > 24 || b[0] == b'0' || !b.iter().all(u8::is_ascii_digit) {
        return None;
    }
    let mut p = [b'0'; 24];
    p[24 - b.len()..].copy_from_slice(b);
    let mut out = String::with_capacity(64);
    let mut c = 0;
    // the leading all-zero magnitude groups produce nothing; start at the first group holding a digit
    let first = (24 - b.len()) / 6;
    for (i, (one, many)) in
        [("trillón", "trillones"), ("billón", "billones"), ("millón", "millones")].iter().enumerate().skip(first)
    {
        let g = &p[i * 6..i * 6 + 6];
        if g == b"000000" {
            continue;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        if g == b"000001" {
            out.push_str("un ");
            out.push_str(one);
            c += cost(-0.001);
        } else {
            h6_into(g, true, &mut out)?;
            out.push(' ');
            out.push_str(many);
        }
    }
    let last = &p[18..];
    if last != b"000000" {
        h6_into(last, false, &mut out)?;
    }
    Some((out, c))
}

/// `filter_punctuation`: digits, or 1-3 digits then `.`/` `-separated groups of three; returns the bare digits.
pub fn filter_punctuation(s: &[char]) -> Option<String> {
    let mut out = String::with_capacity(s.len());
    let mut group = 0usize;
    let mut groups = 0usize;
    let mut first = 0usize;
    for &c in s {
        if is_digit(c) {
            out.push(c);
            group += 1;
        } else if c == '.' || c == ' ' {
            if groups == 0 {
                first = group;
            } else if group != 3 {
                return None;
            }
            groups += 1;
            group = 0;
        } else {
            return None;
        }
    }
    if groups == 0 {
        return (!out.is_empty()).then_some(out);
    }
    (group == 3 && (1..=3).contains(&first)).then_some(out)
}

/// `CardinalFst.graph` (with `filter_punctuation`) on a span.
pub fn graph(s: &[char]) -> Option<(String, Cost)> {
    number(&filter_punctuation(s)?)
}

/// The words of [`graph`] on `text[s..e]` (tag building only).
pub fn words_at(text: &[char], s: usize, e: usize) -> String {
    graph(&text[s..e]).expect("words of an accepted span").0
}

/// Digits of a span accepted by `filter_punctuation`, on the stack (`None` beyond 24 digits: `number` rejects them).
pub struct Digits {
    buf: [u8; 24],
    len: usize,
}

impl Digits {
    pub fn as_bytes(&self) -> &[u8] {
        &self.buf[..self.len]
    }
}

/// [`filter_punctuation`] without allocating; `None` also for more than 24 digits.
pub fn filter_digits(s: &[char]) -> Option<Digits> {
    let mut d = Digits { buf: [0; 24], len: 0 };
    let mut group = 0usize;
    let mut groups = 0usize;
    let mut first = 0usize;
    for &c in s {
        if is_digit(c) {
            if d.len == 24 {
                return None;
            }
            d.buf[d.len] = c as u8;
            d.len += 1;
            group += 1;
        } else if c == '.' || c == ' ' {
            if groups == 0 {
                first = group;
            } else if group != 3 {
                return None;
            }
            groups += 1;
            group = 0;
        } else {
            return None;
        }
    }
    if groups == 0 {
        return (d.len > 0).then_some(d);
    }
    (group == 3 && (1..=3).contains(&first)).then_some(d)
}

/// The cost of [`number`] on plain digits without building the words: every digit string without a leading zero
/// (1-24 digits) and "0" are accepted; each magnitude group reading exactly "000001" ("un millón") costs −0.001.
pub fn number_cost(b: &[u8]) -> Option<Cost> {
    if b == b"0" {
        return NUM_ZERO.rows().first().map(|r| r.cost);
    }
    if b.is_empty() || b.len() > 24 || b[0] == b'0' || !b.iter().all(u8::is_ascii_digit) {
        return None;
    }
    let pad = 24 - b.len();
    let digit = |i: usize| if i < pad { b'0' } else { b[i - pad] };
    let mut c = 0;
    for g in 0..3 {
        if (g * 6..g * 6 + 5).all(|i| digit(i) == b'0') && digit(g * 6 + 5) == b'1' {
            c += cost(-0.001);
        }
    }
    Some(c)
}

/// The cost of [`graph`] on `text[s..e]` (`None` when the graph rejects the span), without building the words.
pub fn cost_at(text: &[char], s: usize, e: usize) -> Option<Cost> {
    number_cost(filter_digits(&text[s..e])?.as_bytes())
}

/// Spans `start..end` of `[0-9. ]` that `graph` could accept (ends on a digit).
/// Ends of the spans at `start` that `filter_punctuation` accepts and that are not followed by a digit (a digit
/// span followed by another digit is never usable: every caller needs a non-digit after a number).
pub fn number_ends(text: &[char], start: usize) -> impl Iterator<Item = usize> + '_ {
    let mut first = None;
    let mut grouped = None;
    if start < text.len() && is_digit(text[start]) {
        let mut e = start;
        while e < text.len() && is_digit(text[e]) {
            e += 1;
        }
        first = Some(e);
        // grouped: a first run of 1-3 digits, then (separator + exactly 3 digits)+; every complete prefix counts
        if e - start <= 3 {
            let mut g = e;
            while g + 4 <= text.len()
                && (text[g] == '.' || text[g] == ' ')
                && text[g + 1..g + 4].iter().all(|c| is_digit(*c))
                && !text.get(g + 4).is_some_and(|c| is_digit(*c))
            {
                g += 4;
            }
            if g > e {
                grouped = Some((e, g));
            }
        }
    }
    let groups = grouped.into_iter().flat_map(|(e, g)| (1..=(g - e) / 4).map(move |k| e + 4 * k));
    first.into_iter().chain(groups)
}

pub struct Cardinal;

impl Lazy for Cardinal {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        let (neg, s) = if text[start] == '-' { (true, start + 1) } else { (false, start) };
        if s >= text.len() {
            return;
        }
        for end in number_ends(text, s) {
            if !ok_end(text, end) {
                continue;
            }
            if let Some(c) = cost_at(text, s, end) {
                out.push(end, cost(1.1) + c, 0, || {
                    let w = words_at(text, s, end);
                    if neg {
                        format!("cardinal {{ negative: \"true\" integer: \"{w}\" }}")
                    } else {
                        format!("cardinal {{ integer: \"{w}\" }}")
                    }
                });
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn numbers() {
        assert_eq!(number("1").unwrap().0, "un");
        assert_eq!(number("21").unwrap().0, "veintiún");
        assert_eq!(number("100").unwrap().0, "cien");
        assert_eq!(number("1001").unwrap().0, "mil un");
        assert_eq!(number("2000000").unwrap().0, "dos millones");
        assert_eq!(number("1000000").unwrap(), ("un millón".into(), -1000));
        assert_eq!(number("201000").unwrap().0, "doscientos un mil");
        assert_eq!(graph(&"1.000.000".chars().collect::<Vec<_>>()).unwrap().0, "un millón");
    }

    #[test]
    fn cost_without_words_matches_number() {
        let mut x: u64 = 0x9E37_79B9_7F4A_7C15;
        let mut cases: Vec<String> = (0..20000u32).map(|v| v.to_string()).collect();
        for len in 1..=26 {
            for _ in 0..400 {
                let s: String = (0..len)
                    .map(|_| {
                        x ^= x << 13;
                        x ^= x >> 7;
                        x ^= x << 17;
                        // zero-heavy digits exercise the "000001" groups
                        if x % 3 == 0 {
                            '0'
                        } else if x % 5 == 0 {
                            '1'
                        } else {
                            (b'0' + (x % 10) as u8) as char
                        }
                    })
                    .collect();
                cases.push(s);
            }
        }
        cases.extend(["1000000", "1000001000000", "1000000000000000000", "1000001000001"].map(String::from));
        for d in cases {
            assert_eq!(number_cost(d.as_bytes()), number(&d).map(|x| x.1), "{d}");
        }
    }
}

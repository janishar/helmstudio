//! Marathi telephone, credit-card and postal-code digit graphs.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

const MOBILE: &str = include_str!("../../../data/mr/telephone/mobile_context.tsv");
const LANDLINE: &str = include_str!("../../../data/mr/telephone/landline_context.tsv");
const CREDIT: &str = include_str!("../../../data/mr/telephone/credit_context.tsv");
const PIN: &str = include_str!("../../../data/mr/telephone/pincode_context.tsv");

static KEYWORDS: LazyLock<super::address::KeywordIndex> = LazyLock::new(|| {
    let mut index = super::address::KeywordIndex::new();
    for word in [MOBILE, LANDLINE, CREDIT, PIN].into_iter().flat_map(|t| t.lines()) {
        index.insert(word.trim_end_matches('\r'));
    }
    index
});

fn whitespace(c: char) -> bool {
    matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}')
}
fn delete_space(s: &str) -> &str {
    s.trim_start_matches(whitespace)
}
pub(crate) fn plain_char(c: char) -> bool {
    numbers::digit(c).is_some() || whitespace(c) || matches!(c, '+' | '-' | '.' | '(' | ')')
}
pub(crate) fn possible_start(s: &str) -> bool {
    let Some(first) = s.chars().next() else { return false };
    numbers::digit(first).is_some()
        || matches!(first, '+' | '(')
        || whitespace(first)
        || KEYWORDS.get(first).iter().any(|word| s.starts_with(*word))
}
pub(crate) fn first_keyword_end(s: &str) -> Option<usize> {
    s.char_indices()
        .flat_map(|(i, c)| {
            KEYWORDS.get(c).iter().filter_map(move |word| s[i..].starts_with(*word).then_some(i + word.len()))
        })
        .min()
}
fn words(s: &str) -> bool {
    if s.is_empty() {
        return true;
    }
    let mut count = 0;
    s.split(' ').all(|word| {
        count += 1;
        count <= 5 && !word.is_empty() && word.chars().all(|c| numbers::digit(c).is_none() && !whitespace(c))
    })
}
fn before(s: &str, table: &str) -> bool {
    s.is_empty()
        || table.lines().any(|k| {
            let k = k.trim_end_matches('\r');
            s.strip_prefix(k)
                .and_then(|r| r.strip_prefix(' '))
                .and_then(|r| r.strip_suffix(' ').or_else(|| r.is_empty().then_some("")))
                .is_some_and(words)
        })
}
fn after(s: &str, table: &str) -> bool {
    s.is_empty()
        || s.strip_prefix(' ').is_some_and(|s| {
            table.lines().any(|k| {
                let k = k.trim_end_matches('\r');
                s == k || s.strip_suffix(k).and_then(|r| r.strip_suffix(' ')).is_some_and(words)
            })
        })
}
fn prefix_digits(s: &str, count: usize) -> Option<(&str, &str)> {
    let mut end = 0;
    let mut it = s.char_indices();
    for _ in 0..count {
        let (i, c) = it.next()?;
        numbers::digit(c)?;
        end = i + c.len_utf8();
    }
    Some((&s[..end], &s[end..]))
}
fn mobile_start(c: char) -> bool {
    matches!(c, '6' | '7' | '8' | '9' | '६' | '७' | '८' | '९')
}
fn landline_start(c: char) -> bool {
    matches!(c, '2' | '3' | '4' | '6' | '२' | '३' | '४' | '६')
}
fn emit(out: &mut Vec<Candidate>, end: usize, weight: f64, fields: impl FnOnce() -> String) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + weight),
        rank: 8,
        tag: super::lazy::tag(|| format!("telephone {{ {}  }}", fields())),
    });
}
fn spoken(s: &str) -> String {
    numbers::digitwise(s).unwrap_or_default()
}
fn after_text(s: &str) -> &str {
    s.strip_prefix(' ').unwrap_or(s)
}

// The mobile graph allows a context on the number, and another on an extension.
fn mobile_tail(prefix: &str, country: Option<&str>, num: &str, tail: &str, end: usize, out: &mut Vec<Candidate>) {
    for split in tail.char_indices().map(|(i, _)| i).chain(std::iter::once(tail.len())) {
        let (ctx, rem) = tail.split_at(split);
        if !after(ctx, MOBILE) {
            continue;
        }
        let rem = delete_space(rem);
        if rem.is_empty() {
            emit(out, end, 0.7, || {
                let n = spoken(num);
                let ctx = after_text(ctx);
                match country {
                    Some(code) => format!("country_code: \"{prefix}प्लस {} \" number_part: \"{n} {ctx}\"", spoken(code)),
                    None => format!("number_part: \"{prefix}शून्य {n} {ctx}\""),
                }
            });
        }
        for n in 1..=3 {
            let Some((ext, rest)) = prefix_digits(rem, n) else { continue };
            for j in rest.char_indices().map(|(i, _)| i).chain(std::iter::once(rest.len())) {
                let (extctx, trailing) = rest.split_at(j);
                if !after(extctx, MOBILE) || !delete_space(trailing).is_empty() {
                    continue;
                }
                emit(out, end, 0.7, || {
                    let n = spoken(num);
                    let ctx = after_text(ctx);
                    let extension = spoken(ext);
                    let extctx = after_text(extctx);
                    match country {
                        Some(code) => format!(
                            "country_code: \"{prefix}प्लस {} \" number_part: \"{n} {ctx}\" extension: \"{extension} {extctx}\"",
                            spoken(code)
                        ),
                        None => format!("number_part: \"{prefix}शून्य {n} {ctx}\" extension: \"{extension} {extctx}\""),
                    }
                });
            }
        }
    }
}
fn mobile(prefix: &str, core: &str, end: usize, out: &mut Vec<Candidate>) {
    if !before(prefix, MOBILE) {
        return;
    }
    if let Some(rest) = core.strip_prefix('+') {
        for len in 1..=3 {
            let Some((code, rest)) = prefix_digits(rest, len) else { continue };
            for rest in [rest, rest.strip_prefix(whitespace).unwrap_or(rest)] {
                let Some((num, tail)) = prefix_digits(rest, 10) else { continue };
                if num.chars().next().is_some_and(mobile_start) {
                    mobile_tail(prefix, Some(code), num, tail, end, out);
                }
            }
        }
    } else {
        for rest in [core, core.strip_prefix(['0', '०']).unwrap_or(core)] {
            let Some((num, tail)) = prefix_digits(rest, 10) else { continue };
            if num.chars().next().is_some_and(mobile_start) {
                mobile_tail(prefix, None, num, tail, end, out);
            }
        }
    }
}
fn landline_part(prefix: &str, code_input: &str, bracket: bool, end: usize, out: &mut Vec<Candidate>) {
    for zero in [false, true] {
        let input = if zero {
            let Some(r) = code_input.strip_prefix(['0', '०']) else { continue };
            r
        } else {
            code_input
        };
        for len in 2..=7 {
            let Some((std, mut rest)) = prefix_digits(input, len) else { continue };
            if bracket {
                rest = delete_space(rest);
                let Some(r) = rest.strip_prefix(')') else { continue };
                rest = r;
            }
            if let Some(r) = rest.strip_prefix(['-', '.']) {
                rest = r;
            }
            rest = delete_space(rest);
            let Some((num, tail)) = prefix_digits(rest, 10 - len) else { continue };
            if !num.chars().next().is_some_and(landline_start) || !after(tail, LANDLINE) {
                continue;
            }
            emit(out, end, 0.8, || {
                format!("number_part: \"{prefix}शून्य {} {} {}\"", spoken(std), spoken(num), after_text(tail))
            });
        }
    }
}
fn landline(prefix: &str, core: &str, end: usize, out: &mut Vec<Candidate>) {
    if !before(prefix, LANDLINE) {
        return;
    }
    landline_part(prefix, core, false, end, out);
    for zero in [false, true] {
        let rest = if zero {
            let Some(r) = core.strip_prefix(['0', '०']) else { continue };
            r
        } else {
            core
        };
        let rest = delete_space(rest);
        if let Some(rest) = rest.strip_prefix('(') {
            landline_part(prefix, delete_space(rest), true, end, out);
        }
    }
}
fn digit_sequence(
    prefix: &str,
    core: &str,
    table: &str,
    min: usize,
    weight: f64,
    end: usize,
    out: &mut Vec<Candidate>,
) {
    if !before(prefix, table) {
        return;
    }
    let digit_end =
        core.char_indices().find_map(|(i, c)| numbers::digit(c).is_none().then_some(i)).unwrap_or(core.len());
    let (digits, tail) = core.split_at(digit_end);
    let tail = tail.trim_end_matches(whitespace);
    if digits.chars().count() < min || !after(tail, table) {
        return;
    }
    emit(out, end, weight, || format!("number_part: \"{prefix}{} {}\"", spoken(digits), after_text(tail)));
}
fn international(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let Some(rest) = s.strip_prefix('+') else { return };
    for len in 1..=3 {
        let Some((code, rest)) = prefix_digits(rest, len) else { continue };
        for number in [rest, rest.strip_prefix([' ', '-']).unwrap_or(rest)] {
            let mut prev_digit = false;
            let mut count = 0;
            let mut valid = true;
            for c in number.chars() {
                if numbers::digit(c).is_some() {
                    count += 1;
                    prev_digit = true;
                } else if matches!(c, ' ' | '-' | '.') && prev_digit {
                    prev_digit = false;
                } else {
                    valid = false;
                    break;
                }
            }
            if !valid || !prev_digit || !(6..=12).contains(&count) {
                continue;
            }
            emit(out, end, 1.0, || {
                let rendered = number.chars().filter_map(numbers::digit).collect::<Vec<_>>().join(" ");
                format!("country_code: \"प्लस {}\" number_part: \"{rendered}\"", spoken(code))
            });
        }
    }
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    international(s, end, out);
    for split in s.char_indices().map(|(i, _)| i).chain(std::iter::once(s.len())) {
        let (prefix, core) = s.split_at(split);
        mobile(prefix, core, end, out);
        landline(prefix, core, end, out);
        digit_sequence(prefix, core, CREDIT, 4, 0.9, end, out);
        digit_sequence(prefix, core, PIN, 6, 1.0, end, out);
    }
}
pub fn classify_hinted(s: &str, end: usize, out: &mut Vec<Candidate>, _plain: bool) {
    classify(s, end, out);
}

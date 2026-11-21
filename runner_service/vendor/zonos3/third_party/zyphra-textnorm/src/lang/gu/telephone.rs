//! Telephone, credit-card and postal-code digit graphs.
use super::numbers;
use crate::lattice::Candidate;
use crate::text::cost;
fn emit(out: &mut Vec<Candidate>, end: usize, fields: impl FnOnce() -> String, w: f64) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + w),
        rank: 8,
        tag: super::lazy::tag(|| format!("telephone {{ {} }}", fields())),
    });
}
const MOBILE: &str = include_str!("../../../data/gu/telephone/mobile_context.tsv");
const LANDLINE: &str = include_str!("../../../data/gu/telephone/landline_context.tsv");
const CREDIT: &str = include_str!("../../../data/gu/telephone/credit_context.tsv");
const PIN: &str = include_str!("../../../data/gu/telephone/pincode_context.tsv");
static KEYWORDS: std::sync::LazyLock<super::address::KeywordIndex> = std::sync::LazyLock::new(|| {
    let mut index = super::address::KeywordIndex::new();
    for word in [MOBILE, LANDLINE, CREDIT, PIN]
        .into_iter()
        .flat_map(|table| table.lines().map(|line| line.trim_end_matches('\r')))
    {
        index.insert(word);
    }
    index
});
pub(crate) fn possible_start(s: &str) -> bool {
    let Some(first) = s.chars().next() else { return false };
    if numbers::digit(first).is_some() || matches!(first, '+' | '(') || ws(first) {
        // The shortest number branch is four consecutive credit digits; the
        // other branches require at least six. Context may follow the digits,
        // but it cannot interrupt the number's plain-character prefix.
        let mut digits = 0;
        for c in s.chars().take_while(|&c| plain_char(c)) {
            if numbers::digit(c).is_some() {
                digits += 1;
                if digits == 4 {
                    return true;
                }
            }
        }
        false
    } else {
        KEYWORDS.get(first).iter().any(|word| s.starts_with(*word))
    }
}
pub(crate) fn first_keyword_end(s: &str) -> Option<usize> {
    let mut earliest: Option<usize> = None;
    for (i, c) in s.char_indices() {
        if earliest.is_some_and(|end| i >= end) {
            break;
        }
        for word in KEYWORDS.get(c) {
            if s[i..].starts_with(*word) {
                let end = i + word.len();
                earliest = Some(earliest.map_or(end, |old| old.min(end)));
            }
        }
    }
    earliest
}
pub(crate) fn plain_char(c: char) -> bool {
    numbers::digit(c).is_some() || ws(c) || matches!(c, '+' | '-' | '.' | '(' | ')')
}
fn context_words(s: &str) -> bool {
    let mut count = 0;
    s.split(' ').all(|word| {
        count += 1;
        count <= 5 && !word.is_empty() && !word.chars().any(|c| numbers::digit(c).is_some() || ws(c))
    })
}
fn context(prefix: &str, suffix: &str, table: &str) -> bool {
    let before = if prefix.is_empty() {
        true
    } else if let Some(p) = prefix.strip_suffix(' ') {
        table.lines().any(|k| {
            let k = k.trim_end_matches('\r');
            p == k || p.strip_prefix(k).and_then(|r| r.strip_prefix(' ')).is_some_and(context_words)
        })
    } else {
        false
    };
    let after = if suffix.is_empty() {
        true
    } else if let Some(p) = suffix.strip_prefix(' ') {
        table.lines().any(|k| {
            let k = k.trim_end_matches('\r');
            p == k || p.strip_suffix(k).and_then(|r| r.strip_suffix(' ')).is_some_and(context_words)
        })
    } else {
        false
    };
    before && after
}
fn spell(s: &str) -> Option<String> {
    numbers::digitwise(s)
}
fn spell_separated(s: &str) -> String {
    let mut out = String::new();
    for word in s.chars().filter_map(numbers::digit) {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(word);
    }
    out
}
fn mobile_start(c: char) -> bool {
    matches!(c, '6' | '7' | '8' | '9' | '૬' | '૭' | '૮' | '૯')
}
fn landline_start(c: char) -> bool {
    matches!(c, '2' | '3' | '4' | '6' | '૨' | '૩' | '૪' | '૬')
}
fn digit_prefix(s: &str, n: usize) -> Option<(&str, &str)> {
    let mut it = s.char_indices();
    let mut end = 0;
    for _ in 0..n {
        let (i, c) = it.next()?;
        if numbers::digit(c).is_none() {
            return None;
        }
        end = i + c.len_utf8();
    }
    Some((&s[..end], &s[end..]))
}
fn ws(c: char) -> bool {
    matches!(c, ' ' | '\t' | '\n' | '\r' | '\u{a0}')
}
fn after_context<'a>(s: &'a str, table: &'a str) -> impl Iterator<Item = (&'a str, &'a str)> + 'a {
    let matches = s
        .char_indices()
        .map(|(i, _)| i)
        .chain(std::iter::once(s.len()))
        .filter(move |&end| end > 0 && s.starts_with(' '))
        .filter_map(move |end| {
            let part = &s[..end];
            context("", part, table).then(|| (part.strip_prefix(' ').unwrap_or(part), &s[end..]))
        });
    std::iter::once(("", s)).chain(matches)
}
fn finish_mobile(rest: &str, prefix: &str, country: Option<&str>, number: &str, end: usize, out: &mut Vec<Candidate>) {
    if !number.chars().all(|c| numbers::digit(c).is_some()) {
        return;
    }
    for (ctx, after) in after_context(rest, MOBILE) {
        let base = || {
            if let Some(cc) = country {
                format!(
                    "country_code: \"{prefix}પ્લસ {} \" number_part: \"{} {ctx}\"",
                    spell(cc).unwrap(),
                    spell(number).unwrap()
                )
            } else {
                format!("number_part: \"{prefix}{} {ctx}\"", spell(number).unwrap())
            }
        };
        let after = after.trim_start_matches(ws);
        if after.is_empty() {
            emit(out, end, || base(), 0.7);
        }
        for n in 1..=3 {
            if let Some((ext, tail)) = digit_prefix(after, n) {
                for (ectx, remain) in after_context(tail, MOBILE) {
                    if remain.chars().all(ws) {
                        emit(out, end, || format!("{} extension: \"{} {ectx}\"", base(), spell(ext).unwrap()), 0.7);
                    }
                }
            }
        }
    }
}
fn mobile(s: &str, end: usize, out: &mut Vec<Candidate>, allow_context: bool) {
    for boundary in s.char_indices().map(|(i, _)| i).chain(std::iter::once(s.len())) {
        if boundary != 0 && !allow_context {
            break;
        }
        let prefix = &s[..boundary];
        if !context(prefix, "", MOBILE) {
            continue;
        }
        let rest = &s[boundary..];
        if let Some(rest) = rest.strip_prefix('+') {
            for n in 1..=3 {
                if let Some((cc, tail)) = digit_prefix(rest, n) {
                    let tail = tail.trim_start_matches(ws);
                    if let Some((number, after)) = digit_prefix(tail, 10) {
                        if number.chars().next().is_some_and(mobile_start) {
                            finish_mobile(after, prefix, Some(cc), number, end, out);
                        }
                    }
                }
            }
        } else {
            for z in [false, true] {
                let number = if z { rest.strip_prefix('0').or_else(|| rest.strip_prefix('૦')) } else { Some(rest) };
                if let Some(number) = number {
                    if let Some((main, after)) = digit_prefix(number, 10) {
                        if main.chars().next().is_some_and(mobile_start) {
                            let digits = if z { &rest[..rest.len() - after.len()] } else { main };
                            finish_mobile(after, prefix, None, digits, end, out);
                        }
                    }
                }
            }
        }
    }
}
fn std_codes(s: &str, n: usize) -> [Option<(&str, &str)>; 6] {
    let mut out = [None; 6];
    let mut count = 0;
    for optional_zero in [false, true] {
        let tail = if optional_zero { s.strip_prefix('0').or_else(|| s.strip_prefix('૦')) } else { Some(s) };
        if let Some(tail) = tail {
            if let Some((code, after)) = digit_prefix(tail, n) {
                out[count] = Some((code, after));
                count += 1;
            }
        }
    }
    for outside_zero in [false, true] {
        let tail = if outside_zero { s.strip_prefix('0').or_else(|| s.strip_prefix('૦')) } else { Some(s) };
        let Some(tail) = tail else { continue };
        let Some(tail) = tail.trim_start_matches(ws).strip_prefix('(') else { continue };
        let tail = tail.trim_start_matches(ws);
        for inside_zero in [false, true] {
            let tail =
                if inside_zero { tail.strip_prefix('0').or_else(|| tail.strip_prefix('૦')) } else { Some(tail) };
            if let Some(tail) = tail {
                if let Some((code, after)) = digit_prefix(tail, n) {
                    if let Some(after) = after.trim_start_matches(ws).strip_prefix(')') {
                        out[count] = Some((code, after));
                        count += 1;
                    }
                }
            }
        }
    }
    out
}
fn landline(s: &str, end: usize, out: &mut Vec<Candidate>, allow_context: bool) {
    for boundary in s.char_indices().map(|(i, _)| i).chain(std::iter::once(s.len())) {
        if boundary != 0 && !allow_context {
            break;
        }
        let prefix = &s[..boundary];
        if !context(prefix, "", LANDLINE) {
            continue;
        }
        for std in 2..=7 {
            for (code, after) in std_codes(&s[boundary..], std).into_iter().flatten() {
                let after =
                    after.strip_prefix('-').or_else(|| after.strip_prefix('.')).unwrap_or(after).trim_start_matches(ws);
                if let Some((number, after)) = digit_prefix(after, 10 - std) {
                    if !number.chars().next().is_some_and(landline_start) {
                        continue;
                    }
                    for (ctx, remain) in after_context(after, LANDLINE) {
                        if remain.is_empty() {
                            emit(
                                out,
                                end,
                                || {
                                    format!(
                                        "number_part: \"{prefix}શૂન્ય {} {} {ctx}\"",
                                        spell(code).unwrap(),
                                        spell(number).unwrap()
                                    )
                                },
                                0.8,
                            );
                        }
                    }
                }
            }
        }
    }
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let plain = s.chars().all(plain_char);
    if !plain && first_keyword_end(s).is_none() {
        return;
    }
    classify_hinted(s, end, out, plain);
}
pub(crate) fn classify_hinted(s: &str, end: usize, out: &mut Vec<Candidate>, plain: bool) {
    if s.chars().filter(|c| numbers::digit(*c).is_some()).take(10).count() == 10 {
        mobile(s, end, out, !plain);
        landline(s, end, out, !plain);
    }
    if let Some(rest) = s.strip_prefix('+') {
        let mut ascii_digits = 0;
        let mut ascii_valid = true;
        for c in rest.chars() {
            if c.is_ascii_digit() {
                ascii_digits += 1;
            } else if !matches!(c, ' ' | '-' | '.' | '(' | ')') {
                ascii_valid = false;
            }
        }
        if ascii_valid && (7..=15).contains(&ascii_digits) {
            out.push(Candidate {
                end,
                cost: cost(1.1),
                rank: 8,
                tag: super::lazy::tag(|| {
                    format!("name: \"પ્લસ\u{a0}{}\"", spell_separated(rest).replace(' ', "\u{a0}"))
                }),
            });
        }
        for country_len in 1..=3 {
            let Some((cc, rem)) = digit_prefix(rest, country_len) else { continue };
            let rem = rem.strip_prefix(' ').or_else(|| rem.strip_prefix('-')).unwrap_or(rem);
            let mut count = 0;
            let mut previous_digit = false;
            let mut valid = true;
            for c in rem.chars() {
                if numbers::digit(c).is_some() {
                    count += 1;
                    previous_digit = true;
                } else if matches!(c, ' ' | '-' | '.') && previous_digit {
                    previous_digit = false;
                } else {
                    valid = false;
                    break;
                }
            }
            if valid && previous_digit && (6..=12).contains(&count) {
                emit(
                    out,
                    end,
                    || {
                        format!(
                            "country_code: \"પ્લસ {}\" number_part: \"{}\"",
                            spell(cc).unwrap(),
                            spell_separated(rem)
                        )
                    },
                    1.0,
                );
            }
        }
    }
    // Source contexts are copied into the telephone field around the digit run.
    for (begin, c) in s.char_indices() {
        if numbers::digit(c).is_none() {
            continue;
        }
        if begin > 0 && s[..begin].chars().last().is_some_and(|c| numbers::digit(c).is_some()) {
            continue;
        }
        let prefix = &s[..begin];
        let rest = &s[begin..];
        let bytes =
            rest.char_indices().find_map(|(i, c)| numbers::digit(c).is_none().then_some(i)).unwrap_or(rest.len());
        let n = &rest[..bytes];
        let suffix = &rest[bytes..];
        let len = n.chars().count();
        let Some(v) = spell(n) else {
            continue;
        };
        let suffix = suffix.trim_end_matches(ws);
        if len >= 4 && context(prefix, suffix, CREDIT) {
            emit(
                out,
                end,
                || format!("number_part: \"{prefix}{v} {}\"", suffix.strip_prefix(' ').unwrap_or(suffix)),
                0.9,
            );
        }
        if len >= 6 && context(prefix, suffix, PIN) {
            emit(
                out,
                end,
                || format!("number_part: \"{prefix}{v} {}\"", suffix.strip_prefix(' ').unwrap_or(suffix)),
                1.0,
            );
        }
    }
}

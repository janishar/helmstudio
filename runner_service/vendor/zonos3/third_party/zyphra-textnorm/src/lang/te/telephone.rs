//! Telugu telephone, mobile, landline and context-number classifiers.
use super::{lazy, numbers};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

const CREDIT: &str = include_str!("../../../data/te/telephone/credit_context.tsv");
const PINCODE: &str = include_str!("../../../data/te/telephone/pincode_context.tsv");

static CONTEXT_STARTS: std::sync::LazyLock<std::collections::HashMap<char, Vec<&'static str>>> =
    std::sync::LazyLock::new(|| {
        let mut index: std::collections::HashMap<char, Vec<&'static str>> = std::collections::HashMap::new();
        for word in CREDIT.lines().chain(PINCODE.lines()) {
            if let Some(first) = word.chars().next() {
                index.entry(first).or_default().push(word);
            }
        }
        index
    });

fn context_prefix(input: &str) -> impl Iterator<Item = &'static str> + '_ {
    input.chars().next().and_then(|c| CONTEXT_STARTS.get(&c)).into_iter().flatten().copied()
}

pub fn possible_start(input: &str, first: char) -> bool {
    numbers::is_digit(first)
        || matches!(first, '+' | '(')
        || context_prefix(input).any(|keyword| input.strip_prefix(keyword).is_some_and(|rest| rest.starts_with(' ')))
}

static CONTEXT_ENDS: std::sync::LazyLock<std::collections::HashSet<char>> = std::sync::LazyLock::new(|| {
    CREDIT.lines().chain(PINCODE.lines()).filter_map(|word| word.chars().next_back()).collect()
});

pub fn possible_end(c: char) -> bool {
    numbers::is_digit(c) || CONTEXT_ENDS.contains(&c)
}

// `context_after` admits at most five intervening non-digit words before
// the terminal keyword. A keyword prefix is kept even when more text follows
// it, since punctuation can make that prefix an eligible lattice endpoint.
pub fn possible_context_after(s: &str) -> bool {
    for word in s.split(' ').take(6) {
        if context_prefix(word).any(|keyword| word.starts_with(keyword)) {
            return true;
        }
        if !context_word(word) {
            return false;
        }
    }
    false
}

fn read_digits(s: &str) -> Option<String> {
    let mut out = String::new();
    for c in s.chars() {
        if matches!(c, ' ' | '-' | '.' | '(' | ')') {
            continue;
        }
        out.push_str(numbers::digit_word(c)?);
        out.push(' ');
    }
    (!out.is_empty()).then_some(out)
}

fn emit(out: &mut Vec<Candidate>, end: usize, weight: Cost, country: Option<&str>, number: &str) {
    out.push(Candidate {
        end,
        cost: cost(1.1) + weight,
        rank: 9,
        tag: lazy::tag(|| {
            let number = read_digits(number).unwrap();
            match country {
                Some(cc) => format!(
                    "telephone {{ country_code: \"ప్లస్ {}\" number_part: \"{number}\" }}",
                    read_digits(cc).unwrap()
                ),
                None => format!("telephone {{ number_part: \"{number}\" }}"),
            }
        }),
    });
}

fn grouped(s: &str, min: usize, max: usize) -> bool {
    let mut n = 0;
    let mut digit_last = false;
    for c in s.chars() {
        if numbers::is_digit(c) {
            n += 1;
            digit_last = true;
        } else if matches!(c, ' ' | '-' | '.') && digit_last {
            digit_last = false;
        } else {
            return false;
        }
    }
    digit_last && (min..=max).contains(&n)
}

fn context_word(s: &str) -> bool {
    !s.is_empty() && s.chars().all(|c| !numbers::is_digit(c) && !c.is_whitespace())
}
fn context_before(s: &str, keywords: &str) -> bool {
    if s.is_empty() {
        return true;
    }
    let Some(s) = s.strip_suffix(' ') else { return false };
    let mut parts = s.split(' ');
    let Some(first) = parts.next() else { return false };
    if !keywords.lines().any(|k| k == first) {
        return false;
    }
    let mut count = 0;
    for part in parts {
        if !context_word(part) {
            return false;
        }
        count += 1;
    }
    count <= 5
}
fn context_after(s: &str, keywords: &str) -> bool {
    if s.is_empty() {
        return true;
    }
    let Some(s) = s.strip_prefix(' ') else { return false };
    let mut parts = s.rsplit(' ');
    let Some(last) = parts.next() else { return false };
    if !keywords.lines().any(|k| k == last) {
        return false;
    }
    let mut count = 0;
    for part in parts {
        if !context_word(part) {
            return false;
        }
        count += 1;
    }
    count <= 5
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    if let Some(rest) = s.strip_prefix('+') {
        for (pos, _) in rest.char_indices().skip(1).chain(std::iter::once((rest.len(), '\0'))) {
            let cc = &rest[..pos];
            if !(1..=3).contains(&cc.chars().count()) || !cc.chars().all(numbers::is_digit) {
                break;
            }
            let number = &rest[pos..];
            let number = number.strip_prefix(' ').or_else(|| number.strip_prefix('-')).unwrap_or(number);
            if grouped(number, 6, 12) {
                emit(out, end, cost(0.7), Some(cc), number);
            }
        }
    }
    if s.chars().next().is_some_and(numbers::is_digit) {
        let mobile_start = |c: char| matches!(numbers::digit_val(c), Some(6..=9));
        let mut chars = s.chars();
        let a = chars.next();
        let b = chars.next();
        let mobile_plain = s.chars().all(numbers::is_digit)
            && ((s.chars().count() == 10 && a.is_some_and(mobile_start))
                || (s.chars().count() == 11 && a.is_some_and(numbers::is_zero) && b.is_some_and(mobile_start)));
        let mut parts = s.split([' ', '-']);
        let a = parts.next().unwrap_or("");
        let b = parts.next();
        let c = parts.next();
        let mobile_split = match (b, c, parts.next()) {
            (Some(b), None, None) => {
                a.chars().count() == 5
                    && b.chars().count() == 5
                    && a.chars().all(numbers::is_digit)
                    && b.chars().all(numbers::is_digit)
            }
            (Some(b), Some(c), None) => {
                a.chars().count() == 3
                    && b.chars().count() == 3
                    && c.chars().count() == 4
                    && a.chars().all(numbers::is_digit)
                    && b.chars().all(numbers::is_digit)
                    && c.chars().all(numbers::is_digit)
            }
            _ => false,
        };
        if mobile_plain || (mobile_split && a.chars().next().is_some_and(mobile_start)) {
            emit(out, end, -cost(0.2), None, s);
        }
    }
    let landline = if let Some(rest) = s.strip_prefix('(') {
        rest.split_once(')').is_some_and(|(std, sub)| {
            let sub = sub.strip_prefix(' ').unwrap_or(sub);
            (3..=6).contains(&std.chars().count())
                && std.chars().next().is_some_and(numbers::is_zero)
                && std.chars().all(numbers::is_digit)
                && (6..=8).contains(&sub.chars().count())
                && sub.chars().all(numbers::is_digit)
        })
    } else if s.chars().next().is_some_and(numbers::is_zero) {
        (3..=6).any(|n| {
            let Some((pos, _)) = s.char_indices().nth(n) else { return false };
            let std = &s[..pos];
            let sub = &s[pos..];
            let sub = sub
                .strip_prefix(' ')
                .or_else(|| sub.strip_prefix('-'))
                .or_else(|| sub.strip_prefix('.'))
                .unwrap_or(sub);
            let total = n + sub.chars().count();
            std.chars().next().is_some_and(numbers::is_zero)
                && std.chars().all(numbers::is_digit)
                && (6..=8).contains(&sub.chars().count())
                && (10..=12).contains(&total)
                && sub.chars().all(numbers::is_digit)
        })
    } else {
        false
    };
    if landline {
        emit(out, end, cost(0.8), None, s);
    }
    // Context words contain no digits, and the number is followed either by
    // the end of the input or by a space. Consequently only the first,
    // maximal digit run can satisfy these branches.
    if let Some((start, _)) = s.char_indices().find(|&(_, c)| numbers::is_digit(c)) {
        let before = &s[..start];
        let credit_before = context_before(before, CREDIT);
        let pin_before = context_before(before, PINCODE);
        if credit_before || pin_before {
            let rest = &s[start..];
            let stop = rest.char_indices().find(|&(_, c)| !numbers::is_digit(c)).map_or(rest.len(), |(pos, _)| pos);
            let number = &rest[..stop];
            let count = number.chars().count();
            let after = &rest[stop..];
            for (keywords, valid_before, min, weight) in
                [(CREDIT, credit_before, 4, cost(0.9)), (PINCODE, pin_before, 6, cost(1.0))]
            {
                if valid_before && (min..=19).contains(&count) && context_after(after, keywords) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1) + weight,
                        rank: 9,
                        tag: lazy::tag(|| {
                            let num = read_digits(number).unwrap();
                            let spoken = format!("{before}{num}{}", after.strip_prefix(' ').unwrap_or(after));
                            format!("telephone {{ number_part: \"{spoken}\" }}")
                        }),
                    });
                }
            }
        }
    }
}

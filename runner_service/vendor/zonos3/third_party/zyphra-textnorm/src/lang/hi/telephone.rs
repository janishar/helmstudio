//! Hindi telephone, landline, credit and pincode classifier.
use crate::lattice::Candidate;
use crate::text::cost;
use std::sync::LazyLock;

static MOBILE: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| words(include_str!("../../../data/hi/telephone/mobile_context.tsv")).collect());
static LANDLINE: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| words(include_str!("../../../data/hi/telephone/landline_context.tsv")).collect());
static CREDIT: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| words(include_str!("../../../data/hi/telephone/credit_context.tsv")).collect());
static PIN: LazyLock<Vec<&'static str>> =
    LazyLock::new(|| words(include_str!("../../../data/hi/telephone/pincode_context.tsv")).collect());
static CONTEXT_STARTS: LazyLock<super::trie::Trie> = LazyLock::new(|| {
    super::trie::Trie::from_keys(MOBILE.iter().chain(LANDLINE.iter()).chain(CREDIT.iter()).chain(PIN.iter()).copied())
});

pub(crate) fn context_start(s: &str) -> bool {
    let mut found = false;
    CONTEXT_STARTS.matching_prefixes(s, |_| found = true);
    found
}
pub(crate) fn last_context_start(s: &str) -> Option<usize> {
    CONTEXT_STARTS.last_start(s)
}
fn digit(c: char) -> bool {
    super::numbers::is_digit(c)
}
pub(crate) fn possible_start(s: &str) -> bool {
    let mut context_start = false;
    if !s.chars().next().is_some_and(|c| digit(c) || matches!(c, '+' | '(')) {
        CONTEXT_STARTS.matching_prefixes(s, |_| context_start = true);
    } else {
        context_start = true;
    }
    if !context_start {
        return false;
    }
    // Credit and pincode cores contain at least four consecutive digits.
    // Mobile and landline cores contain at least ten digits; international
    // numbers start with '+' and contain at least one country-code digit and
    // six number digits, possibly separated.
    let mut digits = 0;
    let mut run = 0;
    let international = s.starts_with('+');
    for c in s.chars() {
        if digit(c) {
            digits += 1;
            run += 1;
            if run >= 4 || digits >= 10 || (international && digits >= 7) {
                return true;
            }
        } else {
            run = 0;
        }
    }
    false
}
fn emit(
    out: &mut Vec<Candidate>,
    end: usize,
    weight: f64,
    country: Option<&str>,
    number: &str,
    extension: Option<&str>,
) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + weight),
        rank: 8,
        tag: super::lazy::tag(|| {
            let country = country.map(|v| format!("country_code: \"{v}\" ")).unwrap_or_default();
            let extension = extension.map(|v| format!(" extension: \"{v}\" ")).unwrap_or_default();
            format!("telephone {{ {country}number_part: \"{number}\"{extension} }}")
        }),
    });
}
fn words(data: &'static str) -> impl Iterator<Item = &'static str> {
    data.lines().map(|s| s.trim_end_matches('\r')).filter(|s| !s.is_empty())
}
fn context_before(prefix: &str, data: &[&str]) -> bool {
    if prefix.is_empty() {
        return true;
    }
    data.iter().any(|keyword| prefix.strip_prefix(*keyword).and_then(|s| s.strip_prefix(' ')).is_some_and(window))
}
fn context_after(suffix: &str, data: &[&str]) -> bool {
    if suffix.is_empty() {
        return true;
    }
    let Some(s) = suffix.strip_prefix(' ') else { return false };
    data.iter().any(|keyword| s.strip_suffix(*keyword).is_some_and(window))
}
fn window(s: &str) -> bool {
    if s.is_empty() {
        return true;
    }
    if !s.ends_with(' ') {
        return false;
    }
    let parts = s[..s.len() - 1].split(' ');
    let mut count = 0;
    for p in parts {
        if p.is_empty() || p.chars().any(|c| digit(c) || crate::text::is_nemo_space(c)) {
            return false;
        }
        count += 1;
    }
    count <= 5
}
fn contexts<'a>(s: &'a str, data: &[&str], mut f: impl FnMut(&'a str, &'a str, &'a str)) {
    for start in s.char_indices().map(|(i, _)| i).chain(std::iter::once(s.len())) {
        if start > 0 && s.as_bytes()[start - 1] != b' ' {
            continue;
        }
        if start == s.len() {
            continue;
        }
        let first = s[start..].chars().next().unwrap();
        if !digit(first) && !matches!(first, '+' | '(') && !crate::text::is_nemo_space(first) {
            continue;
        }
        let before = &s[..start];
        if !context_before(before, data) {
            continue;
        }
        for end in s[start..].char_indices().map(|(i, _)| start + i).chain(std::iter::once(s.len())) {
            if end < s.len() && !s[end..].starts_with(' ') {
                continue;
            }
            let after = &s[end..];
            if context_after(after, data) {
                f(before, &s[start..end], after);
            }
        }
    }
}
fn core_digits(s: &str) -> bool {
    !s.is_empty() && s.chars().all(digit)
}
fn number_with_context(before: &str, spoken: &str, after: &str, trailing_space: bool) -> String {
    super::lazy::tag(|| {
        format!(
            "{before}{spoken}{}{after}",
            if trailing_space { " " } else { "" },
            after = after.strip_prefix(' ').unwrap_or(after)
        )
    })
}
fn international(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let Some(rest) = s.strip_prefix('+') else { return };
    for (index, ch) in rest.char_indices() {
        if !digit(ch) || rest[..index].chars().count() == 3 {
            break;
        }
        let cc_end = index + ch.len_utf8();
        let cc = &rest[..cc_end];
        let remaining = &rest[cc_end..];
        for number in [Some(remaining), remaining.strip_prefix(' '), remaining.strip_prefix('-')].into_iter().flatten()
        {
            let mut count = 0;
            let mut separator = false;
            let mut valid = true;
            for c in number.chars() {
                if digit(c) {
                    count += 1;
                    separator = false;
                } else if matches!(c, ' ' | '-' | '.') && count != 0 && !separator {
                    separator = true;
                } else {
                    valid = false;
                    break;
                }
            }
            if valid && !separator && (6..=12).contains(&count) {
                let country = super::lazy::tag(|| format!("प्लस {}", super::numbers::digitwise(cc).unwrap()));
                let spoken = super::lazy::tag(|| {
                    let digits: String = number.chars().filter(|&c| digit(c)).collect();
                    super::numbers::digitwise(&digits).unwrap()
                });
                emit(out, end, 1.0, Some(&country), &spoken, None);
            }
        }
    }
}
pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    international(s, end, out);
    for (data, length, weight) in [(CREDIT.as_slice(), 4, 0.9), (PIN.as_slice(), 6, 1.0)] {
        contexts(s, data, |before, core, after| {
            let core = if after.is_empty() { core.trim_end_matches(crate::text::is_nemo_space) } else { core };
            if core.chars().count() == length && core_digits(core) {
                if let Some(spoken) = super::numbers::digitwise(core) {
                    let value = number_with_context(before, &spoken, after, true);
                    emit(out, end, weight, None, &value, None);
                }
            }
        });
    }
    contexts(s, MOBILE.as_slice(), |before, core, after| {
        let core = if after.is_empty() { core.trim_end_matches(crate::text::is_nemo_space) } else { core };
        let mut starts = [None; 3];
        let mut count = 0;
        if let Some(rest) = core.strip_prefix('+') {
            for (i, c) in rest.char_indices() {
                if !digit(c) || rest[..i].chars().count() == 3 {
                    break;
                }
                let cc = &rest[..i + c.len_utf8()];
                let number = rest[i + c.len_utf8()..].trim_start_matches(crate::text::is_nemo_space);
                starts[count] = Some((Some(cc), number));
                count += 1;
            }
        } else {
            starts[0] = Some((None, core.strip_prefix('0').or_else(|| core.strip_prefix('०')).unwrap_or(core)));
        }
        for (country, number) in starts.into_iter().flatten() {
            let mut iter = number.char_indices();
            let Some((last, ch)) = iter.nth(9) else { continue };
            let main = &number[..last + ch.len_utf8()];
            if !core_digits(main) || !matches!(main.chars().next(), Some('6' | '7' | '8' | '9' | '६' | '७' | '८' | '९'))
            {
                continue;
            }
            let rest = &number[last + ch.len_utf8()..];
            let Some(main_spoken) = super::numbers::digitwise(main) else { continue };
            let country_spoken = country.and_then(super::numbers::digitwise);
            let country_value = country_spoken.as_ref().map(|v| super::lazy::tag(|| format!("{before}प्लस {v} ")));
            let leading = if country.is_none() { "शून्य " } else { "" };
            let prefix = if country.is_none() { before } else { "" };
            if rest.chars().all(crate::text::is_nemo_space) {
                let value =
                    number_with_context(prefix, &super::lazy::tag(|| format!("{leading}{main_spoken}")), after, true);
                emit(out, end, 0.7, country_value.as_deref(), &value, None);
            }
            for pos in rest.char_indices().map(|(i, _)| i) {
                let extension = &rest[pos..];
                if !(1..=3).contains(&extension.chars().count()) || !core_digits(extension) {
                    continue;
                }
                let middle = rest[..pos].trim_end_matches(crate::text::is_nemo_space);
                if !context_after(middle, MOBILE.as_slice()) {
                    continue;
                }
                let Some(extension) = super::numbers::digitwise(extension) else { continue };
                let base = super::lazy::tag(|| format!("{leading}{main_spoken}"));
                let value = number_with_context(prefix, &base, middle, true);
                let extension = number_with_context("", &extension, after, true);
                emit(out, end, 0.7, country_value.as_deref(), &value, Some(&extension));
            }
        }
    });
    contexts(s, LANDLINE.as_slice(), |before, core, after| {
        let unprefixed = core.strip_prefix('0').or_else(|| core.strip_prefix('०')).unwrap_or(core);
        let unprefixed = unprefixed.trim_start_matches(crate::text::is_nemo_space);
        if let Some(bracketed) = unprefixed.strip_prefix('(') {
            if let Some((inside, after_bracket)) = bracketed.split_once(')') {
                let inside = inside.trim_matches(crate::text::is_nemo_space);
                let std = inside.strip_prefix('0').or_else(|| inside.strip_prefix('०')).unwrap_or(inside);
                let length = std.chars().count();
                let rest = after_bracket
                    .strip_prefix('-')
                    .or_else(|| after_bracket.strip_prefix('.'))
                    .unwrap_or(after_bracket)
                    .trim_start_matches(crate::text::is_nemo_space);
                if (2..=7).contains(&length)
                    && core_digits(std)
                    && rest.chars().count() == 10 - length
                    && core_digits(rest)
                    && matches!(rest.chars().next(), Some('2' | '3' | '4' | '6' | '२' | '३' | '४' | '६'))
                {
                    if let (Some(a), Some(b)) = (super::numbers::digitwise(std), super::numbers::digitwise(rest)) {
                        let spoken = super::lazy::tag(|| format!("शून्य {a} {b}"));
                        let value = number_with_context(before, &spoken, after, true);
                        emit(out, end, 0.8, None, &value, None);
                    }
                }
            }
        }
        for std_len in 2..=7 {
            for split in core.char_indices().map(|(i, _)| i).chain(std::iter::once(core.len())) {
                let std = &core[..split];
                let rest = &core[split..];
                let std = std.strip_prefix('0').or_else(|| std.strip_prefix('०')).unwrap_or(std);
                if std.chars().count() != std_len || !core_digits(std) {
                    continue;
                }
                let rest = rest
                    .strip_prefix('-')
                    .or_else(|| rest.strip_prefix('.'))
                    .unwrap_or(rest)
                    .trim_start_matches(crate::text::is_nemo_space);
                if rest.chars().count() != 10 - std_len
                    || !core_digits(rest)
                    || !matches!(rest.chars().next(), Some('2' | '3' | '4' | '6' | '२' | '३' | '४' | '६'))
                {
                    continue;
                }
                if let (Some(a), Some(b)) = (super::numbers::digitwise(std), super::numbers::digitwise(rest)) {
                    let spoken = super::lazy::tag(|| format!("शून्य {a} {b}"));
                    let value = number_with_context(before, &spoken, after, true);
                    emit(out, end, 0.8, None, &value, None);
                }
            }
        }
    });
}

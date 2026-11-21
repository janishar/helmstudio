use super::graph_utils::Gender;
use super::numeric::{decimal, number};
use std::collections::HashMap;
use std::sync::LazyLock;

type Unit = (&'static str, &'static str, &'static str, bool);
static UNITS: LazyLock<HashMap<char, Vec<Unit>>> = LazyLock::new(|| {
    let mut index = HashMap::<char, Vec<Unit>>::new();
    for line in include_str!("../../../data/bg/units.tsv").lines() {
        if line.starts_with('#') {
            continue;
        }
        let mut fields = line.split('\t');
        if let (Some(unit), Some(one), Some(plural), Some(gender), None) =
            (fields.next(), fields.next(), fields.next(), fields.next(), fields.next())
        {
            if let Some(last) = unit.chars().last() {
                index.entry(last).or_default().push((unit, one, plural, gender == "f"));
            }
        }
    }
    index
});

/// End characters of unit, percentage, and quoted-percentage branches.
pub(crate) fn possible_end(c: char) -> bool {
    matches!(c, '%' | '‰' | '"') || UNITS.contains_key(&c)
}

fn numeric_body(s: &str) -> bool {
    s.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '\u{a0}' | '.' | ',' | '-' | '−' | '–' | '—' | '~' | '±'))
}

/// Every measure ends in a unit or percent marker, with only numeric/range
/// syntax before it. Unit suffixes come directly from the embedded source table.
pub(crate) fn possible_body(s: &str) -> bool {
    let unquoted = if let Some(inner) = s.strip_prefix('"').and_then(|v| v.strip_suffix('"')) { inner } else { s };
    if let Some(n) = unquoted.strip_suffix('%').or_else(|| unquoted.strip_suffix('‰')) {
        return numeric_body(n);
    }
    let Some(units) = s.chars().last().and_then(|c| UNITS.get(&c)) else { return false };
    units.iter().any(|&(unit, _, _, _)| s.strip_suffix(unit).is_some_and(numeric_body))
}

/// Recognize the direct integer-unit branches without spelling the number.
pub(crate) fn simple_integer(s: &str) -> Option<(f64, usize)> {
    let split = s.bytes().take_while(u8::is_ascii_digit).count();
    let (number, suffix) = s.split_at(split);
    if super::plain_cardinal(number) != Some(1.2) {
        return None;
    }
    let suffix = suffix.strip_prefix(' ').unwrap_or(suffix);
    let count = if number == "1" { 2 } else { 1 };
    if matches!(suffix, "%" | "‰") {
        return Some((1.0, count));
    }
    let units = suffix.chars().last().and_then(|c| UNITS.get(&c))?;
    let matches = units.iter().filter(|(key, _, _, _)| *key == suffix).count();
    (matches != 0).then_some((1.05, count * matches))
}

pub(crate) fn render_simple_integer(s: &str) -> Option<String> {
    let split = s.bytes().take_while(u8::is_ascii_digit).count();
    let (n, suffix) = s.split_at(split);
    let suffix = suffix.strip_prefix(' ').unwrap_or(suffix);
    let (one, plural, feminine) = match suffix {
        "%" => ("процент", "процента", false),
        "‰" => ("промил", "промила", false),
        _ => {
            let units = suffix.chars().last().and_then(|c| UNITS.get(&c))?;
            let &(_, one, plural, feminine) = units.iter().find(|(key, _, _, _)| *key == suffix)?;
            (one, plural, feminine)
        }
    };
    let mut value = number(n, if feminine { Gender::Feminine } else { Gender::Masculine })?;
    value.push(' ');
    value.push_str(if n == "1" { one } else { plural });
    Some(value)
}

fn base(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let Some(units) = s.chars().last().and_then(|c| UNITS.get(&c)) else { return out };
    for &(unit, one, plural, feminine) in units {
        if let Some(n) = s.strip_suffix(unit) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            let g = if feminine { Gender::Feminine } else { Gender::Masculine };
            if let Some(v) = number(n, g) {
                if n == "1" {
                    out.push(format!("{v} {one}"));
                }
                out.push(format!("{v} {plural}"));
            }
            if !feminine {
                if let Some(v) = decimal(n) {
                    out.push(format!("{v} {plural}"));
                }
                if let Some(n) = n.strip_prefix('-').or_else(|| n.strip_prefix('−')) {
                    if let Some(v) = number(n, Gender::Masculine).or_else(|| decimal(n)) {
                        out.push(format!("минус {v} {plural}"));
                    }
                }
            }
        }
    }
    out
}
fn percent(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for (sym, singular, plural) in [("%", "процент", "процента"), ("‰", "промил", "промила")]
    {
        if let Some(n) = s.strip_suffix(sym) {
            let n = n.strip_suffix(' ').unwrap_or(n);
            if let Some(v) = number(n, Gender::Masculine) {
                if n == "1" {
                    out.push(format!("{v} {singular}"));
                }
                out.push(format!("{v} {plural}"));
            }
            if sym == "%" {
                if let Some(v) = decimal(n) {
                    out.push(format!("{v} {plural}"));
                }
            }
        }
    }
    out
}
pub(crate) fn classify(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    for v in base(s) {
        out.push((v, 1.05));
    }
    for v in percent(s) {
        out.push((v, 1.0));
    }
    if let Some(s) = s.strip_prefix('~') {
        for v in base(s) {
            out.push((format!("около {v}"), 0.95));
        }
    }
    for (sign, prefix) in [("-", "минус "), ("−", "минус "), ("±", "плюс-минус ")] {
        if let Some(n) = s.strip_prefix(sign) {
            for v in percent(n) {
                out.push((format!("{prefix}{v}"), 1.0));
            }
        }
    }
    if let Some(s) = s.strip_prefix('"').and_then(|s| s.strip_suffix('"')) {
        for v in percent(s) {
            out.push((format!("\"{v}\""), 1.0));
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—') {
            let a = s[..i].strip_suffix(' ').unwrap_or(&s[..i]);
            let r = &s[i + c.len_utf8()..];
            let b = r.strip_prefix(' ').unwrap_or(r);
            if let Some(v) = number(a, Gender::Masculine) {
                for w in base(b).into_iter().chain(percent(b)) {
                    out.push((format!("{v} до {w}"), 0.96));
                }
            }
        }
    }
    out
}

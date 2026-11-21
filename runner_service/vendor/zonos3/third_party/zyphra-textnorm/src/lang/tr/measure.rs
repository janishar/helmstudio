//! Measurement branches and lexical units from `tr/taggers/measure.py`.
use super::{
    decimal,
    graph_utils::{append_positive, is_positive, positive},
};
use std::sync::LazyLock;
type Unit = (&'static str, &'static str);
static UNITS: LazyLock<[Vec<Unit>; 256]> = LazyLock::new(|| {
    let mut index: [Vec<Unit>; 256] = std::array::from_fn(|_| Vec::new());
    for row in include_str!("../../../data/tr/unit_map.tsv").lines() {
        if let Some((unit, spoken)) = row.split_once('\t') {
            if let Some(&last) = unit.as_bytes().last() {
                index[last as usize].push((unit, spoken));
            }
        }
    }
    index
});
fn units(s: &str) -> &'static [Unit] {
    s.as_bytes().last().map_or(&[], |&last| UNITS[last as usize].as_slice())
}
pub fn is_quantity(s: &str) -> bool {
    matches!(
        s,
        "milyon" | "milyonluk" | "milyar" | "milyarlık" | "trilyon" | "katrilyon" | "bin" | "bilyon" | "biljoner"
    )
}
/// Only the decimal quantity lexicon can introduce letters in the numeric
/// part of a measurement, including either side of a range.
fn possible_number_part(s: &str) -> bool {
    let mut word = None;
    for (i, c) in s.char_indices().chain(std::iter::once((s.len(), '\0'))) {
        if c.is_ascii_alphabetic() || c == 'ı' {
            if word.is_none() {
                word = Some(i);
            }
            continue;
        }
        if let Some(start) = word.take() {
            if !is_quantity(&s[start..i]) {
                return false;
            }
        }
        if c != '\0' && !c.is_ascii_digit() && !matches!(c, '.' | ',' | ' ' | '\u{202f}' | '-' | '−' | '–' | '—')
        {
            return false;
        }
    }
    true
}
fn number_count(s: &str) -> usize {
    let unsigned = s.strip_prefix('-').or_else(|| s.strip_prefix('−')).unwrap_or(s);
    decimal::count(s) + usize::from(is_positive(unsigned))
}
pub fn count(s: &str) -> usize {
    let mut total = 0;
    for &(unit, _) in units(s) {
        let Some(prefix) = s.strip_suffix(unit) else { continue };
        let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
        if !possible_number_part(prefix) {
            continue;
        }
        total += number_count(prefix);
        for (i, c) in prefix.char_indices() {
            if matches!(c, '-' | '–' | '—') {
                let a = prefix[..i].strip_suffix(' ').unwrap_or(&prefix[..i]);
                let b = &prefix[i + c.len_utf8()..];
                let b = b.strip_prefix(' ').unwrap_or(b);
                total += number_count(a) * number_count(b);
            }
        }
    }
    total
}
/// Write the first integer/unit path directly, leaving ambiguous earlier
/// decimal and range branches to the general renderer.
pub fn simple_tag(s: &str) -> Option<String> {
    for &(unit, spoken) in units(s) {
        let Some(prefix) = s.strip_suffix(unit) else { continue };
        let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
        if decimal::count(prefix) != 0 {
            return None;
        }
        let (sign, number) = match prefix.strip_prefix('-').or_else(|| prefix.strip_prefix('−')) {
            Some(v) => (true, v),
            None => (false, prefix),
        };
        if is_positive(number) {
            let mut tag = String::with_capacity(s.len() * 4 + spoken.len() + 16);
            tag.push_str("name: \"");
            if sign {
                tag.push_str("eksi\u{a0}");
            }
            append_positive(&mut tag, number, '\u{a0}')?;
            tag.push('\u{a0}');
            for c in spoken.chars() {
                tag.push(if c == ' ' { '\u{a0}' } else { c });
            }
            tag.push('"');
            return Some(tag);
        }
        if prefix.chars().any(|c| matches!(c, '-' | '–' | '—')) {
            return None;
        }
    }
    None
}
fn numbers(s: &str) -> Vec<String> {
    let mut out = decimal::readings(s);
    let (sign, s) = match s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        Some(s) => ("eksi ", s),
        None => ("", s),
    };
    if let Some(n) = positive(s) {
        out.push(format!("{sign}{n}"));
    }
    out
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    for &(unit, spoken) in units(s) {
        let Some(prefix) = s.strip_suffix(unit) else { continue };
        let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
        for n in numbers(prefix) {
            out.push(format!("{n} {spoken}"));
        }
        for (i, c) in prefix.char_indices() {
            if matches!(c, '-' | '–' | '—') {
                let a = prefix[..i].strip_suffix(' ').unwrap_or(&prefix[..i]);
                let b = &prefix[i + c.len_utf8()..];
                let b = b.strip_prefix(' ').unwrap_or(b);
                for x in numbers(a) {
                    for y in numbers(b) {
                        out.push(format!("{x} ila {y} {spoken}"));
                    }
                }
            }
        }
    }
    out
}

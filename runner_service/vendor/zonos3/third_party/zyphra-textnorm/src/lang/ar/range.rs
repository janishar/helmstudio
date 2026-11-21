//! Arabic range classifier.
//! Matching `ar/taggers/range.py`.

use super::cardinal::{parse_cardinal_span, spell_cardinal};
use super::char_util::{char_at, token_end, token_start};
use super::money::{CURRENCY_SYMBOLS, get_range_currency_name};
use super::time::{self, TimeClassifier};
use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

static SYMBOL_INITIALS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index = HashMap::<char, Vec<&'static str>>::new();
    for sym in CURRENCY_SYMBOLS.iter() {
        if let Some(ch) = sym.chars().next() {
            index.entry(ch).or_default().push(sym.as_str());
        }
    }
    index
});

fn initial_symbols(text: &[char], start: usize) -> impl Iterator<Item = &'static str> {
    text.get(start).and_then(|c| SYMBOL_INITIALS.get(c)).into_iter().flatten().copied()
}

fn number_paths(text: &[char], start: usize, max_digits: usize, mut emit: impl FnMut(usize, u64, f64)) {
    let mut end = start;
    let mut value = 0u64;
    while end < text.len() && end - start < max_digits && char_at(text, end).is_ascii_digit() {
        value = value * 10 + (char_at(text, end) as u8 - b'0') as u64;
        end += 1;
        if end - start > 1 && char_at(text, start) == '0' {
            break;
        }
        emit(end, value, super::cardinal::zero_suffix_cost(value));
        if end - start <= 3 && value != 0 && end < text.len() {
            let factor = match text[end] {
                'k' | 'K' => 1_000,
                'm' | 'M' => 1_000_000,
                'b' | 'B' => 1_000_000_000,
                _ => 0,
            };
            if factor != 0 {
                let expanded = value * factor;
                emit(end + 1, expanded, super::cardinal::zero_suffix_cost(expanded));
            }
        }
    }
}

fn dash_end(text: &[char], start: usize) -> Option<usize> {
    let (dash, spaced) = if text.get(start) == Some(&' ') { (start + 1, true) } else { (start, false) };
    if !matches!(text.get(dash), Some('-' | '–')) {
        return None;
    }
    if spaced { (text.get(dash + 1) == Some(&' ')).then_some(dash + 2) } else { Some(dash + 1) }
}

fn suffix_symbols(text: &[char], start: usize, mut emit: impl FnMut(usize)) {
    let mut visit = |sym: &str| {
        let ascii_alpha = sym.is_ascii() && sym.chars().any(|c| c.is_ascii_alphabetic());
        for space in 0..=1 {
            if (ascii_alpha && space == 0) || (space == 1 && text.get(start) != Some(&' ')) {
                continue;
            }
            let begin = start + space;
            let end = begin + sym.chars().count();
            if end <= text.len() && text[begin..end].iter().copied().eq(sym.chars()) {
                emit(end);
            }
        }
    };
    let direct = text.get(start).and_then(|c| SYMBOL_INITIALS.get(c));
    let spaced =
        if text.get(start) == Some(&' ') { text.get(start + 1).and_then(|c| SYMBOL_INITIALS.get(c)) } else { None };
    match (direct, spaced) {
        (Some(_), Some(_)) => {
            // A symbol itself can start with space; retain global symbol order.
            for sym in CURRENCY_SYMBOLS.iter() {
                visit(sym);
            }
        }
        (Some(symbols), None) | (None, Some(symbols)) => {
            for sym in symbols {
                visit(sym);
            }
        }
        (None, None) => {}
    }
}

fn amount_value(s: &str) -> u64 {
    let s = s.trim();
    let (digits, factor) = match s.chars().last() {
        Some('k' | 'K') => (&s[..s.len() - 1], 1_000),
        Some('m' | 'M') => (&s[..s.len() - 1], 1_000_000),
        Some('b' | 'B') => (&s[..s.len() - 1], 1_000_000_000),
        _ => (s, 1),
    };
    digits.parse::<u64>().expect("recognized range amount") * factor
}

pub struct RangeClassifier<'a> {
    pub punct: &'a Punctuation,
}

thread_local! {
    static LEFT_TIMES: std::cell::Cell<Vec<Candidate>> = const { std::cell::Cell::new(Vec::new()) };
    static RIGHT_TIMES: std::cell::Cell<Vec<Candidate>> = const { std::cell::Cell::new(Vec::new()) };
}

impl Classifier for RangeClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }

        let starts_digit = text.get(start).is_some_and(|_| char_at(text, start).is_ascii_digit());
        if !starts_digit && initial_symbols(text, start).next().is_none() {
            return;
        }
        if starts_digit
            && (text.get(start + 1) == Some(&':')
                || (text.get(start + 1).is_some_and(|_| char_at(text, start + 1).is_ascii_digit())
                    && text.get(start + 2) == Some(&':')))
        {
            let time_classifier = TimeClassifier { punct: self.punct };
            let mut left_times = LEFT_TIMES.take();
            left_times.clear();
            time_classifier.candidates(text, start, &mut left_times);
            let mut right_times = RIGHT_TIMES.take();
            for left in &left_times {
                if text[start..left.end].iter().filter(|&&c| c == ':').count() != 1 {
                    continue;
                }
                let Some(right_start) = dash_end(text, left.end) else { continue };
                right_times.clear();
                time_classifier.candidates(text, right_start, &mut right_times);
                for right in &right_times {
                    if text[right_start..right.end].iter().filter(|&&c| c == ':').count() != 1 {
                        continue;
                    }
                    let rank = 8 | ((left.rank & 1) << 4) | ((right.rank & 1) << 5);
                    out.push(Candidate {
                        end: right.end,
                        cost: cost(1.05) + left.cost + right.cost - 2 * cost(1.1),
                        rank,
                        tag: String::new(),
                    });
                }
            }
            left_times.clear();
            right_times.clear();
            LEFT_TIMES.set(left_times);
            RIGHT_TIMES.set(right_times);
        }

        // Branch 2: Prefix money range: "$10-$20" or "$10-20"
        for sym in initial_symbols(text, start) {
            let n = sym.chars().count();
            if start + n > text.len() || !text[start..start + n].iter().copied().eq(sym.chars()) {
                continue;
            }
            let mut n1 = start + n;
            if text.get(n1) == Some(&' ') {
                n1 += 1;
            }
            number_paths(text, n1, 6, |left_end, _, left_cost| {
                let Some(right) = dash_end(text, left_end) else { return };
                if !sym.chars().all(char::is_alphabetic) && left_end - n1 <= 4 {
                    number_paths(text, right, 4, |end, _, right_cost| {
                        if token_end(text, end, self.punct) {
                            out.push(Candidate {
                                end,
                                cost: cost(1.05 + left_cost + right_cost),
                                rank: 1,
                                tag: String::new(),
                            });
                        }
                    });
                }
                for right_sym in initial_symbols(text, right) {
                    let len = right_sym.chars().count();
                    if right + len > text.len() || !text[right..right + len].iter().copied().eq(right_sym.chars()) {
                        continue;
                    }
                    let mut n2 = right + len;
                    if text.get(n2) == Some(&' ') {
                        n2 += 1;
                    }
                    number_paths(text, n2, 4, |end, _, right_cost| {
                        if token_end(text, end, self.punct) {
                            out.push(Candidate {
                                end,
                                cost: cost(1.05 + left_cost + right_cost),
                                rank: 5,
                                tag: String::new(),
                            });
                        }
                    });
                }
            });
        }

        number_paths(text, start, 6, |left_end, _, left_cost| {
            let mut left_suffix = |after_left: usize| {
                let Some(right_start) = dash_end(text, after_left) else { return };
                number_paths(text, right_start, 6, |right_end, _, right_cost| {
                    suffix_symbols(text, right_end, |end| {
                        if token_end(text, end, self.punct) {
                            out.push(Candidate {
                                end,
                                cost: cost(1.05 + left_cost + right_cost),
                                rank: 6,
                                tag: String::new(),
                            });
                        }
                    });
                });
            };
            left_suffix(left_end);
            suffix_symbols(text, left_end, &mut left_suffix);
        });

        // Branch 3: Numeric range or Comma list or Suffix money range
        let mut idx = start;
        let n1_start = idx;
        while idx < text.len() && char_at(text, idx).is_ascii_digit() {
            idx += 1;
        }
        let n1_len = idx - n1_start;
        if n1_len == 0 || n1_len > 6 || (n1_len > 1 && char_at(text, n1_start) == '0') {
            return;
        }
        let Some((_, left_cost)) = parse_cardinal_span(text, n1_start, idx, false) else {
            return;
        };

        // Check comma list: e.g. "1,2,3" -> at least 2 commas
        if idx < text.len() && text[idx] == ',' {
            let mut l_idx = idx;
            let mut list_cost = left_cost;
            let mut valid_list = true;
            for _ in 0..2 {
                if text.get(l_idx) != Some(&',') {
                    valid_list = false;
                    break;
                }
                l_idx += 1;
                let item_start = l_idx;
                while l_idx < text.len() && char_at(text, l_idx).is_ascii_digit() && l_idx - item_start < 2 {
                    l_idx += 1;
                }
                if l_idx == item_start || (l_idx - item_start == 2 && char_at(text, item_start) == '0') {
                    valid_list = false;
                    break;
                }
                if let Some((_, c)) = parse_cardinal_span(text, item_start, l_idx, false) {
                    list_cost += c;
                } else {
                    valid_list = false;
                    break;
                }
            }
            if valid_list && token_end(text, l_idx, self.punct) {
                out.push(Candidate { end: l_idx, cost: cost(1.05 + list_cost), rank: 2, tag: String::new() });
            }
        }

        // Check number range: num1 - num2 (exclude 3-digit - 4-digit phone shape)
        let mut d_idx = idx;
        if d_idx < text.len() && text[d_idx] == ' ' {
            d_idx += 1;
        }
        let dash_start = d_idx;
        if d_idx < text.len() && (text[d_idx] == '-' || text[d_idx] == '–') {
            d_idx += 1;
            if dash_start != idx {
                if text.get(d_idx) != Some(&' ') {
                    return;
                }
                d_idx += 1;
            } else if text.get(d_idx) == Some(&' ') {
                return;
            }
            let n2_start = d_idx;
            while d_idx < text.len() && char_at(text, d_idx).is_ascii_digit() {
                d_idx += 1;
            }
            let n2_len = d_idx - n2_start;
            if n2_len > 0 && n2_len <= 6 && (n2_len == 1 || char_at(text, n2_start) != '0') {
                let Some((_, right_cost)) = parse_cardinal_span(text, n2_start, d_idx, false) else { return };
                if !(n1_len == 3 && n2_len == 4 && dash_start == idx && text[dash_start] == '-')
                    && token_end(text, d_idx, self.punct)
                {
                    out.push(Candidate {
                        end: d_idx,
                        cost: cost(1.05 + left_cost + right_cost),
                        rank: 4,
                        tag: String::new(),
                    });
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if candidate.rank & 8 != 0 {
            let classifier = TimeClassifier { punct: self.punct };
            let mut left_times = Vec::new();
            classifier.candidates(text, start, &mut left_times);
            for mut left in left_times {
                if left.rank != ((candidate.rank >> 4) & 1)
                    || text[start..left.end].iter().filter(|&&c| c == ':').count() != 1
                {
                    continue;
                }
                let Some(right_start) = dash_end(text, left.end) else { continue };
                let mut right_times = Vec::new();
                classifier.candidates(text, right_start, &mut right_times);
                for mut right in right_times {
                    if right.end != candidate.end
                        || right.rank != ((candidate.rank >> 5) & 1)
                        || text[right_start..right.end].iter().filter(|&&c| c == ':').count() != 1
                    {
                        continue;
                    }
                    classifier.materialize(text, start, &mut left);
                    classifier.materialize(text, right_start, &mut right);
                    let spoken = |tag: &str| {
                        let parsed = crate::tag::parse(tag).expect("recognized time tag");
                        let (_, value) = crate::lang::common::class(&parsed[0]).expect("time class");
                        let crate::tag::Value::Dict(fields) = value else { unreachable!("time fields") };
                        time::verbalize(fields).expect("recognized time verbalization")
                    };
                    let full = format!("{} إلى {}", spoken(&left.tag), spoken(&right.tag)).replace(' ', "\u{a0}");
                    candidate.tag = format!("name: \"{full}\"");
                    return;
                }
            }
            unreachable!("recognized time range");
        }

        // Branch 1: Prefix money range: "$10-$20" or "$10-20"
        if candidate.rank == 1 || candidate.rank == 5 {
            let s: String = (start..candidate.end).map(|i| char_at(text, i)).collect();
            let dash_pos = s.find('-').or_else(|| s.find('–')).expect("recognized range dash");
            let left = s[..dash_pos].trim();
            let mut right = s[dash_pos + 1..].trim();
            let left_sym =
                CURRENCY_SYMBOLS.iter().find(|sym| left.starts_with(sym.as_str())).expect("recognized currency symbol");
            let mut name_sym = left_sym;
            if candidate.rank == 5 {
                name_sym = CURRENCY_SYMBOLS
                    .iter()
                    .find(|sym| right.starts_with(sym.as_str()))
                    .expect("recognized currency symbol");
                right = right[name_sym.len()..].trim_start();
            }
            let cur_name = get_range_currency_name(name_sym).expect("recognized currency name");
            let v1 = amount_value(&left[left_sym.len()..]);
            let v2 = amount_value(right);
            let full_spoken =
                format!("{} إلى {} {}", spell_cardinal(v1), spell_cardinal(v2), cur_name).replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Branch 2: Comma list: "1,2,3"
        if candidate.rank == 2 {
            let s: String = (start..candidate.end).map(|i| char_at(text, i)).collect();
            let items: Vec<&str> = s.split(',').collect();
            let mut spoken_items: Vec<String> = Vec::new();
            for it in items {
                let v: u64 = it.trim().parse().unwrap_or(0);
                spoken_items.push(spell_cardinal(v));
            }
            let full_spoken = spoken_items.join("، ").replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Branch 3: Suffix money range: "10-20 €"
        if candidate.rank == 6 {
            let s: String = (start..candidate.end).map(|i| char_at(text, i)).collect();
            let dash_pos = s.find('-').or_else(|| s.find('–')).expect("recognized range dash");
            let left = s[..dash_pos].trim();
            let right = s[dash_pos + 1..].trim();
            let left_digits = left.bytes().take_while(u8::is_ascii_digit).count();
            let left_end = left_digits
                + usize::from(
                    left.as_bytes()
                        .get(left_digits)
                        .is_some_and(|c| matches!(c, b'k' | b'K' | b'm' | b'M' | b'b' | b'B')),
                );
            let right_digits = right.bytes().take_while(u8::is_ascii_digit).count();
            let mut right_end = right_digits;
            if right.as_bytes().get(right_digits).is_some_and(|c| matches!(c, b'k' | b'K' | b'm' | b'M' | b'b' | b'B'))
                && get_range_currency_name(right[right_digits + 1..].trim()).is_some()
            {
                right_end += 1;
            }
            let cur_name = get_range_currency_name(right[right_end..].trim()).expect("recognized currency name");
            let v1 = amount_value(&left[..left_end]);
            let v2 = amount_value(&right[..right_end]);
            let full_spoken =
                format!("{} إلى {} {}", spell_cardinal(v1), spell_cardinal(v2), cur_name).replace(' ', "\u{a0}");
            candidate.tag = format!("name: \"{full_spoken}\"");
            return;
        }

        // Branch 4: Plain number range: "5-10"
        let s: String = (start..candidate.end).map(|i| char_at(text, i)).collect();
        let dash_pos = s.find('-').or_else(|| s.find('–')).unwrap_or(0);
        let n1_part: String = s[..dash_pos].chars().filter(|c| c.is_ascii_digit()).collect();
        let n2_part: String = s[dash_pos + 1..].chars().filter(|c| c.is_ascii_digit()).collect();

        let v1: u64 = n1_part.parse().unwrap_or(0);
        let v2: u64 = n2_part.parse().unwrap_or(0);
        let full_spoken = format!("{} إلى {}", spell_cardinal(v1), spell_cardinal(v2)).replace(' ', "\u{a0}");
        candidate.tag = format!("name: \"{full_spoken}\"");
    }
}

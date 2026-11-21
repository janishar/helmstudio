//! Productive Urdu ordinal grammar.

use super::Sink;
use super::cardinal::enumerate_without_leading_zeros;
use crate::text::cost;
use std::sync::LazyLock;

const EXCEPTIONS_DATA: &str = include_str!("../../../../data/ur/ordinal/exceptions.tsv");

static EXCEPTIONS: LazyLock<[Vec<(Vec<char>, &'static str)>; 10]> = LazyLock::new(|| {
    let mut index: [Vec<(Vec<char>, &'static str)>; 10] = std::array::from_fn(|_| Vec::new());
    for (key, value) in EXCEPTIONS_DATA.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t')) {
        if let Some(first) = key.bytes().next().filter(u8::is_ascii_digit) {
            index[(first - b'0') as usize].push((key.chars().collect(), value));
        }
    }
    index
});

fn matches_prefix(text: &[char], key: &str) -> Option<usize> {
    let mut n = 0;
    for c in key.chars() {
        if text.get(n) != Some(&c) {
            return None;
        }
        n += 1;
    }
    Some(n)
}

const SUFFIXES: &[&str] = &["واں", "ویں"];

pub fn classify(text: &[char], start: usize, sink: &mut Sink) {
    if start >= text.len() || !text[start].is_ascii_digit() {
        return;
    }
    let remaining = &text[start..];

    // 1. Exceptions (weight -0.1)
    for (key, val) in &EXCEPTIONS[(remaining[0] as u8 - b'0') as usize] {
        if remaining.starts_with(key) {
            let end = start + key.len();
            let c = cost(1.1) + cost(-0.1);
            sink.push(end, c, 0, move || format!("ordinal {{ integer: \"{}\" }}", val));
        }
    }

    // 2. Regular: cardinal (up to ten thousands) + suffix
    let mut num_end = 0;
    while num_end < remaining.len() && remaining[num_end].is_ascii_digit() {
        num_end += 1;
    }
    if num_end > 0 && num_end <= 5 {
        let num_chars = &remaining[..num_end];
        let after = &remaining[num_end..];
        for &suf in SUFFIXES {
            if let Some(suf_len) = matches_prefix(after, suf) {
                let end = start + num_end + suf_len;
                enumerate_without_leading_zeros(num_chars, &mut |num_spoken, num_cost| {
                    let c = cost(1.1) + num_cost;
                    sink.push(end, c, 0, || format!("ordinal {{ integer: \"{}{}\" }}", num_spoken, suf));
                });
            }
        }
    }
}

pub(crate) fn enumerate_graph(input: &[char], emit: &mut dyn FnMut(String, crate::text::Cost)) {
    if let Some(&first) = input.first().filter(|c| c.is_ascii_digit()) {
        for (key, value) in &EXCEPTIONS[(first as u8 - b'0') as usize] {
            if input == key {
                emit(crate::lang::ur::lazy::tag(|| (*value).to_owned()), cost(-0.1));
            }
        }
    }
    for suffix in SUFFIXES {
        let suffix_len = suffix.chars().count();
        if input.len() >= suffix_len && input[input.len() - suffix_len..].iter().copied().eq(suffix.chars()) {
            let digits = &input[..input.len() - suffix_len];
            if digits.len() <= 5 {
                enumerate_without_leading_zeros(digits, &mut |spoken, weight| {
                    emit(crate::lang::ur::lazy::tag(|| format!("{spoken}{suffix}")), weight);
                });
            }
        }
    }
}

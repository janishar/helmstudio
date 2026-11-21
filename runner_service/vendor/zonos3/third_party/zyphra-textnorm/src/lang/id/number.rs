// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
// Number names derive from Unicode CLDR release 49 (Unicode-3.0 license).

const DIGITS: [&str; 10] = ["nol", "satu", "dua", "tiga", "empat", "lima", "enam", "tujuh", "delapan", "sembilan"];

/// A validated unpadded or period-grouped source integer, at most fifteen digits.
#[derive(Clone, Copy)]
pub(crate) struct Integer(u64);

impl Integer {
    pub(crate) fn plain(s: &str) -> Option<Self> {
        if s == "0" {
            return Some(Self(0));
        }
        if s.is_empty() || s.len() > 15 || s.starts_with('0') {
            return None;
        }
        let mut value = 0u64;
        for byte in s.bytes() {
            let digit = byte.wrapping_sub(b'0');
            if digit > 9 {
                return None;
            }
            value = value * 10 + u64::from(digit);
        }
        Some(Self(value))
    }

    pub(crate) fn grouped(s: &str) -> Option<Self> {
        if let Some(value) = Self::plain(s) {
            return Some(value);
        }
        let mut groups = s.split('.');
        let first = groups.next()?;
        if first.is_empty() || first.len() > 3 || first.starts_with('0') {
            return None;
        }
        let mut value = Self::plain(first)?.0;
        let mut count = 0;
        for group in groups {
            if count == 4 || group.len() != 3 {
                return None;
            }
            let mut next = 0u64;
            for byte in group.bytes() {
                let digit = byte.wrapping_sub(b'0');
                if digit > 9 {
                    return None;
                }
                next = next * 10 + u64::from(digit);
            }
            value = value * 1000 + next;
            count += 1;
        }
        (count != 0).then_some(Self(value))
    }

    pub(crate) fn value(self) -> u64 {
        self.0
    }

    pub(crate) fn write(self, out: &mut String) {
        let base = out.len();
        if self.0 == 0 {
            out.push_str(DIGITS[0]);
            return;
        }
        for (scale, suffix) in
            [(1_000_000_000_000, "triliun"), (1_000_000_000, "miliar"), (1_000_000, "juta"), (1_000, "ribu"), (1, "")]
        {
            let group = (self.0 / scale % 1000) as usize;
            if group == 0 {
                continue;
            }
            if scale == 1000 && group == 1 {
                append(out, "seribu", base);
            } else {
                three(out, group, base);
                if !suffix.is_empty() {
                    append(out, suffix, base);
                }
            }
        }
    }
}

pub(crate) fn cardinal(s: &str) -> Option<String> {
    let value = Integer::plain(s)?;
    let mut out = String::new();
    if !super::lazy::recognizing() {
        out.reserve(s.len() * 8);
        value.write(&mut out);
    }
    Some(out)
}

fn append(out: &mut String, word: &str, base: usize) {
    if out.len() > base {
        out.push(' ');
    }
    out.push_str(word);
}

fn three(out: &mut String, n: usize, base: usize) {
    let hundreds = n / 100;
    if hundreds == 1 {
        append(out, "seratus", base);
    } else if hundreds > 1 {
        append(out, DIGITS[hundreds], base);
        append(out, "ratus", base);
    }
    let rest = n % 100;
    match rest {
        0 => {}
        1..=9 => append(out, DIGITS[rest], base),
        10 => append(out, "sepuluh", base),
        11 => append(out, "sebelas", base),
        12..=19 => {
            append(out, DIGITS[rest - 10], base);
            append(out, "belas", base);
        }
        _ => {
            append(out, DIGITS[rest / 10], base);
            append(out, "puluh", base);
            if rest % 10 != 0 {
                append(out, DIGITS[rest % 10], base);
            }
        }
    }
}

/// Number graph with the source's optional thousands separators (period only in Indonesian).
pub(crate) fn grouped(s: &str) -> Option<String> {
    let value = Integer::grouped(s)?;
    let mut out = String::new();
    if !super::lazy::recognizing() {
        out.reserve(s.len() * 8);
        value.write(&mut out);
    }
    Some(out)
}

pub(crate) fn digits(s: &str) -> Option<String> {
    if s.is_empty() || !s.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if super::lazy::recognizing() {
        return Some(String::new());
    }
    let mut out = String::with_capacity(s.len() * 7);
    for b in s.bytes() {
        append(&mut out, DIGITS[(b - b'0') as usize], 0);
    }
    Some(out)
}

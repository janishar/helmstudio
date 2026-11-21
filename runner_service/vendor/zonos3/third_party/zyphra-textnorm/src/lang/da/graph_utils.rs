// Copyright (c) 2026 Zyphra, Inc. Licensed under the Apache License, Version 2.0.
// Productive equivalents of the cardinal subgraphs in da/taggers/cardinal.py.

const DIGITS: [&str; 10] = ["nul", "et", "to", "tre", "fire", "fem", "seks", "syv", "otte", "ni"];
const COMMON: [&str; 10] = ["nul", "en", "to", "tre", "fire", "fem", "seks", "syv", "otte", "ni"];
const TEENS: [&str; 10] =
    ["ti", "elleve", "tolv", "tretten", "fjorten", "femten", "seksten", "sytten", "atten", "nitten"];
const TENS: [&str; 10] = ["", "", "tyve", "tredive", "fyrre", "halvtreds", "tres", "halvfjerds", "firs", "halvfems"];
pub fn digit(c: char) -> Option<&'static str> {
    c.to_digit(10).filter(|_| c.is_ascii_digit()).map(|n| DIGITS[n as usize])
}

fn write_under_thousand(out: &mut String, n: u16, common: bool) {
    let units = if common { &COMMON } else { &DIGITS };
    if n < 10 {
        out.push_str(units[n as usize]);
    } else if n < 20 {
        out.push_str(TEENS[(n - 10) as usize]);
    } else if n < 100 {
        if n % 10 != 0 {
            out.push_str(COMMON[(n % 10) as usize]);
            out.push_str("og");
        }
        out.push_str(TENS[(n / 10) as usize]);
    } else {
        let h = n / 100;
        if h != 1 {
            out.push_str(DIGITS[h as usize]);
        }
        out.push_str("hundrede");
        if n % 100 != 0 {
            out.push_str(" og ");
            write_under_thousand(out, n % 100, common);
        }
    }
}
pub fn write_small_number(out: &mut String, n: u16, common: bool) {
    debug_assert!(n < 1000);
    write_under_thousand(out, n, common);
}
fn under_thousand(n: u16, common: bool) -> String {
    let mut out = String::with_capacity(32);
    write_under_thousand(&mut out, n, common);
    out
}
/// The separate common-gender 1–999 graph used by minor currency units.
pub fn small_common(input: &str) -> Option<String> {
    if input.is_empty() || input.len() > 3 || input.starts_with('0') || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let n = input.parse::<u16>().ok()?;
    if n == 0 || n > 999 {
        return None;
    }
    Some(under_thousand(n, true))
}

/// Append an ungrouped integer without allocating an intermediate number.
pub fn append_integer(out: &mut String, input: &str, common: bool) -> Option<()> {
    if input.is_empty()
        || input.len() > 15
        || !input.bytes().all(|b| b.is_ascii_digit())
        || (input.len() > 1 && input.starts_with('0'))
    {
        return None;
    }
    let n: u64 = input.parse().ok()?;
    out.reserve(input.len().saturating_mul(10));
    if n < 1000 {
        write_under_thousand(out, n as u16, common && n == 1);
        return Some(());
    }
    let start = out.len();
    for (scale, singular, plural) in [
        (1_000_000_000_000, "billion", "billioner"),
        (1_000_000_000, "milliard", "milliarder"),
        (1_000_000, "million", "millioner"),
    ] {
        let group = (n / scale) % 1000;
        if group == 0 {
            continue;
        }
        if out.len() != start {
            out.push(' ');
        }
        if group == 1 {
            out.push_str("en ");
            out.push_str(singular);
        } else {
            write_under_thousand(out, group as u16, true);
            out.push(' ');
            out.push_str(plural);
        }
    }
    let thousands = (n / 1000) % 1000;
    let rem = (n % 1000) as u16;
    if thousands > 0 {
        if out.len() != start {
            out.push(' ');
        }
        if thousands != 1 {
            write_under_thousand(out, thousands as u16, false);
            out.push(' ');
        }
        out.push_str("tusind");
        if rem > 0 {
            out.push_str(if rem < 100 { " og " } else { " " });
            write_under_thousand(out, rem, false);
        }
    } else if rem > 0 {
        if out.len() != start {
            out.push(' ');
        }
        write_under_thousand(out, rem, false);
    }
    Some(())
}

/// The source's ungrouped integer graph, with optional common gender for one.
pub fn integer(input: &str, common: bool) -> Option<String> {
    let mut out = String::new();
    append_integer(&mut out, input, common)?;
    Some(out)
}

/// Accept one consistent thousands separator, as in `grouped_to_digits`.
pub fn grouped_integer(input: &str, common: bool) -> Option<String> {
    let separator = if input.contains('.') {
        '.'
    } else if input.contains(' ') {
        ' '
    } else {
        return integer(input, common);
    };
    let mut groups = input.split(separator);
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut digits = first.to_owned();
    for group in groups {
        if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        digits.push_str(group);
    }
    integer(&digits, common)
}

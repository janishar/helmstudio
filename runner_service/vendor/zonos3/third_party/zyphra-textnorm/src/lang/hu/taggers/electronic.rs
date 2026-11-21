// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.

use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};
use std::collections::HashMap;
use std::sync::LazyLock;

static TLDS: LazyLock<HashMap<char, Vec<&'static str>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<&'static str>> = HashMap::new();
    let rows = include_str!(concat!(env!("CARGO_MANIFEST_DIR"), "/data/hu/electronic/domain.tsv"));
    for key in rows.lines().filter_map(|line| line.split('\t').next().and_then(|key| key.strip_prefix('.'))).chain([
        "biz", "xyz", "tech", "online", "cloud", "store", "tv", "cc", "gg", "ch", "at", "ro", "sk", "pl", "cz", "nl",
        "se", "no", "dk", "fi", "ca", "au", "jp", "cn", "kr",
    ]) {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push(key);
        }
    }
    index
});

pub const WEIGHT: Cost = cost(1.11);

fn accepted(c: char) -> bool {
    c.is_ascii_alphanumeric() || ".-_!#$%&'*+/\\=?^`{|}~,:".contains(c)
}

fn domain(chars: &[char], multiple_periods: bool, require_period: bool) -> bool {
    if chars.is_empty() || chars[0] == '.' || *chars.last().unwrap() == '.' {
        return false;
    }
    let mut dot = false;
    let mut previous_dot = false;
    for &c in chars {
        if !accepted(c) {
            return false;
        }
        if c == '.' {
            if previous_dot && !multiple_periods {
                return false;
            }
            dot = true;
            previous_dot = true;
        } else {
            previous_dot = false;
        }
    }
    !require_period || dot
}

fn bare_host(chars: &[char]) -> bool {
    let slash = chars.iter().position(|&c| c == '/').unwrap_or(chars.len());
    let host = &chars[..slash];
    if host.is_empty() || !host[0].is_ascii_alphanumeric() {
        return false;
    }
    let Some(dot) = host.iter().rposition(|&c| c == '.') else {
        return false;
    };
    if dot == 0
        || host[..dot].iter().any(|&c| !(c.is_ascii_alphanumeric() || c == '-' || c == '_' || c == '.'))
        || host[..dot].windows(2).any(|w| w == ['.', '.'])
    {
        return false;
    }
    let tld = &host[dot + 1..];
    let known = tld
        .first()
        .and_then(|c| TLDS.get(c))
        .is_some_and(|keys| keys.iter().any(|key| key.chars().eq(tld.iter().copied())));
    known && (slash == chars.len() || domain(&chars[slash + 1..], true, false))
}

pub struct Electronic;
impl Lazy for Electronic {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        if start >= text.len() {
            return;
        }
        // Email and bare hosts require '@' or '.', while an HTTP protocol
        // can introduce a domain without either marker.
        let mut has_marker = false;
        for &c in &text[start..] {
            if c == '@' || c == '.' {
                has_marker = true;
                break;
            }
            if !accepted(c) {
                break;
            }
        }
        if !has_marker
            && !text.get(start..start + 7).is_some_and(|part| part.iter().copied().eq("http://".chars()))
            && !text.get(start..start + 8).is_some_and(|part| part.iter().copied().eq("https://".chars()))
        {
            return;
        }
        let mut at_count = 0;
        for end in start + 1..=text.len() {
            let c = text[end - 1];
            if c == '@' {
                at_count += 1;
            } else if !accepted(c) {
                break;
            }
            if at_count > 1 {
                break;
            }
            let part = &text[start..end];
            if part[0] == '@' && domain(&part[1..], false, false) {
                out.push(end, WEIGHT, 0, || {
                    format!(
                        "electronic {{ username: \"{}\" preserve_order: true }}",
                        part[1..].iter().collect::<String>()
                    )
                });
            }
            for at in 1..part.len() {
                if part[at] == '@' && part[..at].iter().all(|&c| accepted(c)) && domain(&part[at + 1..], false, true) {
                    out.push(end, WEIGHT, 0, || {
                        format!(
                            "electronic {{ username: \"{}\" domain: \"{}\" preserve_order: true }}",
                            part[..at].iter().collect::<String>(),
                            part[at + 1..].iter().collect::<String>()
                        )
                    });
                }
            }
            for (prefix, multiple, needs_dot) in [
                ("http://www.", true, false),
                ("https://www.", true, false),
                ("http://", true, false),
                ("https://", true, false),
                ("www.", false, true),
            ] {
                let n = prefix.len();
                if part.len() > n
                    && part[..n].iter().copied().eq(prefix.chars())
                    && domain(&part[n..], multiple, needs_dot)
                {
                    out.push(end, WEIGHT, 0, || {
                        format!(
                            "electronic {{ protocol: \"{prefix}\" domain: \"{}\" preserve_order: true }}",
                            part[n..].iter().collect::<String>()
                        )
                    });
                }
            }
            if domain(part, false, true) && bare_host(part) {
                out.push(end, WEIGHT, 0, || {
                    format!("electronic {{ domain: \"{}\" preserve_order: true }}", part.iter().collect::<String>())
                });
            }
        }
    }
}
